package connector

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/palchrb/matrix-xplora/internal/fcm"
	"github.com/palchrb/matrix-xplora/internal/xplora"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"
	"maunium.net/go/mautrix/bridgev2/status"
	"maunium.net/go/mautrix/event"
)

// XploraClient is the per-login network client.
// It manages the Xplora GraphQL connection, FCM push notifications,
// and a polling fallback when FCM is unavailable.
type XploraClient struct {
	connector *XploraConnector
	userLogin *bridgev2.UserLogin
	meta      *UserLoginMetadata
	auth      *xplora.Auth
	gql       *xplora.Client
	fcmClient *fcm.Client
	log       zerolog.Logger

	// pollCancel cancels the polling goroutine when FCM is connected.
	pollCancel context.CancelFunc
	pollMu     sync.Mutex

	// fcmCancel cancels the FCM listener goroutine started in Connect().
	// Called by Disconnect() so that logout+re-login without restart does not
	// leave a stale listener competing with the new one.
	fcmCancel context.CancelFunc
}

var _ bridgev2.NetworkAPI = (*XploraClient)(nil)

func newXploraClient(xc *XploraConnector, login *bridgev2.UserLogin, auth *xplora.Auth, gql *xplora.Client, meta *UserLoginMetadata) *XploraClient {
	return &XploraClient{
		connector: xc,
		userLogin: login,
		meta:      meta,
		auth:      auth,
		gql:       gql,
		log:       login.Log.With().Str("component", "xplora-client").Logger(),
	}
}

// ─── bridgev2.NetworkAPI ──────────────────────────────────────────────────────

// Connect validates the session, syncs portals, and starts FCM or polling.
func (c *XploraClient) Connect(ctx context.Context) {
	// Proactively refresh the token if it is near expiry.
	if c.auth.NeedsRefresh() {
		if err := c.tryRefreshToken(ctx); err != nil {
			c.log.Warn().Err(err).Msg("Token refresh failed on connect")
		}
	}

	// Validate session with a lightweight API call.
	if _, err := c.gql.GetMyInfo(ctx); err != nil {
		// Token may have expired mid-session; try refreshing once before giving up.
		if rfErr := c.tryRefreshToken(ctx); rfErr != nil {
			c.userLogin.BridgeState.Send(status.BridgeState{
				StateEvent: status.StateBadCredentials,
				Error:      "xplora-auth-error",
				Message:    "Failed to connect to Xplora API: " + err.Error(),
			})
			return
		}
		if _, err2 := c.gql.GetMyInfo(ctx); err2 != nil {
			c.userLogin.BridgeState.Send(status.BridgeState{
				StateEvent: status.StateBadCredentials,
				Error:      "xplora-auth-error",
				Message:    "Failed to connect to Xplora API: " + err2.Error(),
			})
			return
		}
	}

	// Sync portals (one per child watch) on every connect.
	go c.syncWatches(ctx)

	// Keep the space room avatar in sync with the bot's configured avatar.
	go c.ensureSpaceAvatar(ctx)

	// Try FCM. Fall back to polling on any failure.
	sessDir := c.connector.sessionDir(c.userLogin.ID)
	c.fcmClient = fcm.NewClient(sessDir)

	c.fcmClient.OnMessage(func(msg fcm.NewMessage) {
		c.handleFCMMessage(msg)
	})
	c.fcmClient.OnConnected(func() {
		c.log.Info().Msg("FCM MCS connected")
		c.userLogin.BridgeState.Send(status.BridgeState{StateEvent: status.StateConnected})
		c.stopPolling()
	})
	c.fcmClient.OnDisconnected(func() {
		c.log.Warn().Msg("FCM MCS disconnected")
		c.userLogin.BridgeState.Send(status.BridgeState{
			StateEvent: status.StateTransientDisconnect,
			Error:      "xplora-fcm-disconnected",
		})
	})

	fcmToken, err := c.fcmClient.Register(ctx)
	if err != nil {
		c.log.Warn().Err(err).Msg("FCM registration failed, starting polling fallback")
		c.userLogin.BridgeState.Send(status.BridgeState{StateEvent: status.StateConnected})
		c.startPolling(ctx)
		return
	}

	// Register the FCM token with the Xplora backend.
	// If this fails with a cached token (e.g. after logout+login), refresh the
	// FCM token using the existing androidId and retry once before falling back.
	if err := c.gql.SetFCMToken(ctx, c.meta.ClientID, fcmToken); err != nil {
		c.log.Warn().Err(err).Msg("SetFCMToken failed with cached token, attempting FCM refresh")
		refreshed, refreshErr := c.fcmClient.Refresh(ctx)
		if refreshErr != nil {
			c.log.Warn().Err(refreshErr).Msg("FCM refresh failed, starting polling fallback")
			c.userLogin.BridgeState.Send(status.BridgeState{StateEvent: status.StateConnected})
			c.startPolling(ctx)
			return
		}
		fcmToken = refreshed
		if err := c.gql.SetFCMToken(ctx, c.meta.ClientID, fcmToken); err != nil {
			c.log.Warn().Err(err).Msg("SetFCMToken failed after refresh, starting polling fallback")
			c.userLogin.BridgeState.Send(status.BridgeState{StateEvent: status.StateConnected})
			c.startPolling(ctx)
			return
		}
	}
	c.meta.FCMToken = fcmToken
	c.userLogin.Save(ctx)

	c.userLogin.BridgeState.Send(status.BridgeState{StateEvent: status.StateConnected})

	// Create a cancellable context for the FCM listener so Disconnect() can
	// stop it cleanly without affecting the caller's context.
	fcmCtx, fcmCancel := context.WithCancel(context.Background())
	c.fcmCancel = fcmCancel

	// Start FCM listener in background with exponential backoff.
	go func() {
		defer fcmCancel()
		backoff := 5 * time.Second
		const maxBackoff = 5 * time.Minute
		for fcmCtx.Err() == nil {
			if err := c.fcmClient.Listen(fcmCtx); err != nil && fcmCtx.Err() == nil {
				c.log.Warn().Err(err).Dur("retry_in", backoff).Msg("FCM Listen returned error, retrying")
				select {
				case <-fcmCtx.Done():
					return
				case <-time.After(backoff):
				}
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			} else {
				backoff = 5 * time.Second // reset on clean disconnect
			}
		}
	}()
}

// Disconnect stops background goroutines cleanly.
// Must cancel the FCM listener explicitly — its context is not the caller's
// context, so it would otherwise outlive the login and race with any new
// connection started by a subsequent re-login without a bridge restart.
func (c *XploraClient) Disconnect() {
	c.stopPolling()
	if c.fcmCancel != nil {
		c.fcmCancel()
	}
}

// ensureSpaceAvatar updates the space room's m.room.avatar to match the
// NetworkIcon returned by GetName() (which uses the bot's configured avatar).
// Called on every Connect() so that avatar changes in config.yaml take effect
// on the next restart without needing to recreate the space room.
func (c *XploraClient) ensureSpaceAvatar(ctx context.Context) {
	icon := c.connector.br.Network.GetName().NetworkIcon
	if icon == "" {
		return
	}
	spaceRoom, err := c.userLogin.GetSpaceRoom(ctx)
	if err != nil || spaceRoom == "" {
		return
	}
	if _, err := c.userLogin.Bridge.Bot.SendState(ctx, spaceRoom, event.StateRoomAvatar, "", &event.Content{
		Parsed: &event.RoomAvatarEventContent{URL: icon},
	}, time.Now()); err != nil {
		c.log.Warn().Err(err).Msg("Failed to update space room avatar")
	}
}

// IsLoggedIn returns true if a token is present.
func (c *XploraClient) IsLoggedIn() bool {
	return c.auth.Token() != ""
}

// LogoutRemote clears Xplora session credentials.
// FCM credentials are intentionally kept: they represent a persistent "virtual
// Android device" identity used for GCM registration. Deleting them forces a
// fresh GCM checkin on the next login, which triggers PHONE_REGISTRATION_ERROR
// due to Google rate-limiting re-registrations for the same sender/cert pair.
// On re-login, Connect() calls SetFCMToken() again to re-associate the existing
// FCM token with the new Xplora session.
func (c *XploraClient) LogoutRemote(_ context.Context) {
	sessDir := c.connector.sessionDir(c.userLogin.ID)
	removeFile(sessDir + "/xplora_credentials.json")
}

// GetCapabilities returns Matrix room feature limits for Xplora.
// Xplora v1 supports text messages only.
func (c *XploraClient) GetCapabilities(_ context.Context, _ *bridgev2.Portal) *event.RoomFeatures {
	return &event.RoomFeatures{
		MaxTextLength: 1000, // Xplora limit TBD; update after API testing
	}
}

// GetChatInfo returns the Matrix room configuration for an Xplora watch portal.
func (c *XploraClient) GetChatInfo(_ context.Context, portal *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
	meta, ok := portal.Metadata.(*PortalMetadata)
	if !ok {
		return nil, fmt.Errorf("invalid portal metadata type")
	}
	name := meta.ChildName
	if name == "" {
		name = meta.WUID
	}
	// Include ghost profile info so the avatar is set when the ghost joins.
	ghostInfo := &bridgev2.UserInfo{Name: &name}
	for _, w := range c.meta.Children {
		if w.ChildUID() == meta.WUID && w.AvatarURL != "" {
			ghostInfo.Avatar = makeURLAvatar(w.AvatarURL)
			break
		}
	}
	members := []bridgev2.ChatMember{
		{
			EventSender: bridgev2.EventSender{IsFromMe: true},
			Membership:  event.MembershipJoin,
		},
		{
			EventSender: bridgev2.EventSender{Sender: ghostIDFromWUID(meta.WUID)},
			Membership:  event.MembershipJoin,
			UserInfo:    ghostInfo,
		},
	}
	return &bridgev2.ChatInfo{
		Name:    &name,
		Members: &bridgev2.ChatMemberList{IsFull: true, Members: members},
	}, nil
}

// GetUserInfo returns ghost profile data for a child's watch.
func (c *XploraClient) GetUserInfo(_ context.Context, ghost *bridgev2.Ghost) (*bridgev2.UserInfo, error) {
	wuid := string(ghost.ID)
	for _, w := range c.meta.Children {
		if w.ChildUID() == wuid {
			info := &bridgev2.UserInfo{Name: ptrStr(w.ChildName())}
			if w.AvatarURL != "" {
				info.Avatar = makeURLAvatar(w.AvatarURL)
			}
			return info, nil
		}
	}
	return &bridgev2.UserInfo{Name: ptrStr(wuid)}, nil
}

// IsThisUser checks whether a ghost ID belongs to the logged-in parent user.
func (c *XploraClient) IsThisUser(_ context.Context, userID networkid.UserID) bool {
	// Ghost IDs are watch UIDs (children). The parent user is never a ghost.
	// Return false because all ghosts represent children's watches.
	return false
}

// ─── Matrix → Xplora ─────────────────────────────────────────────────────────

// HandleMatrixMessage sends a Matrix text message to an Xplora child's watch.
func (c *XploraClient) HandleMatrixMessage(ctx context.Context, msg *bridgev2.MatrixMessage) (*bridgev2.MatrixMessageResponse, error) {
	meta, ok := msg.Portal.Metadata.(*PortalMetadata)
	if !ok || meta.WUID == "" {
		return nil, fmt.Errorf("portal has no watch UID — cannot send")
	}

	switch msg.Content.MsgType {
	case event.MsgText, event.MsgNotice, event.MsgEmote:
		if err := c.gql.SendChatText(ctx, meta.WUID, msg.Content.Body); err != nil {
			return nil, fmt.Errorf("sendChatText: %w", err)
		}
		// Xplora sendChatText returns a boolean, not a message ID.
		// Use a synthetic ID based on timestamp for deduplication.
		syntheticID := fmt.Sprintf("sent-%d", time.Now().UnixMilli())
		return &bridgev2.MatrixMessageResponse{
			DB: &database.Message{
				ID:       networkid.MessageID(syntheticID),
				SenderID: networkid.UserID(c.meta.UserID),
			},
		}, nil
	case event.MsgImage, event.MsgAudio, event.MsgVideo, event.MsgFile:
		// Media sending to Xplora is not yet implemented — mutation unknown.
		// Log the full content so we can investigate what the API might accept.
		c.log.Info().
			Str("wuid", meta.WUID).
			Str("mx_msg_type", string(msg.Content.MsgType)).
			Str("mime_type", msg.Content.GetInfo().MimeType).
			Str("filename", msg.Content.Body).
			Int("size", msg.Content.GetInfo().Size).
			Msg("Matrix→Xplora: received media message (sending not yet supported)")
		return nil, fmt.Errorf("sending %s to Xplora is not yet supported", msg.Content.MsgType)
	default:
		return nil, fmt.Errorf("unsupported message type %s", msg.Content.MsgType)
	}
}

// HandleMatrixReadReceipt marks a message as read on the Xplora side.
func (c *XploraClient) HandleMatrixReadReceipt(ctx context.Context, msg *bridgev2.MatrixReadReceipt) error {
	if msg.ExactMessage == nil {
		return nil
	}
	meta, ok := msg.Portal.Metadata.(*PortalMetadata)
	if !ok || meta.WUID == "" {
		return nil
	}
	return c.gql.SetReadChatMsg(ctx, meta.WUID, string(msg.ExactMessage.ID))
}

// ─── FCM and polling ──────────────────────────────────────────────────────────

// handleFCMMessage is called by the FCM OnMessage callback.
// It tries to parse the Xplora FCM payload directly (discovered via fcm-probe):
//
//	{"content":{"msg_id":<int64>,"msg_type":"chat_text","receiver":"<wuid>",
//	            "sender":"<userID>","text":"...","time":<unix_ms>},...}
//
// On success, the message is dispatched without a full poll, preventing
// history replay. Falls back to polling all watches if parsing fails.
func (c *XploraClient) handleFCMMessage(msg fcm.NewMessage) {
	c.log.Debug().RawJSON("fcm_payload", msg.Raw).Msg("Received FCM push from Xplora")

	if chatMsg, wuid, ok := c.parseFCMPayload(msg.Raw); ok {
		isFromMe := chatMsg.Sender != nil && chatMsg.Sender.ID == c.meta.UserID
		c.dispatchChatMessage(wuid, chatMsg, isFromMe)
		c.updateLastMsgID(wuid, chatMsg.MsgID)
		// sender_icon is the SENDER's avatar. Only update the child's avatar when
		// the child is the sender (child→parent direction). For parent→child messages
		// sender_icon is the parent's avatar and must not overwrite the child's.
		if icon := extractFCMSenderIcon(msg.Raw); icon != "" && chatMsg.Sender != nil {
			for _, w := range c.meta.Children {
				if w.ChildUID() == wuid &&
					(chatMsg.Sender.ID == w.ChildUID() || chatMsg.Sender.ID == w.FCMID) {
					c.updateChildAvatar(wuid, icon)
					break
				}
			}
		}
		return
	}

	// Unknown payload type — fall back to polling so nothing is lost.
	ctx := c.userLogin.Log.WithContext(context.Background())
	c.pollAllWatches(ctx)
}

// parseFCMPayload parses an Xplora FCM push notification payload.
// Returns the synthetic ChatMessage, the target watch UID, and true on success.
// The wuid is determined by matching sender/receiver against known children.
func (c *XploraClient) parseFCMPayload(raw json.RawMessage) (xplora.ChatMessage, string, bool) {
	var envelope struct {
		Content *struct {
			MsgID    int64  `json:"msg_id"`
			MsgType  string `json:"msg_type"`
			Receiver string `json:"receiver"`
			Sender   string `json:"sender"`
			Text     string `json:"text"`
			Time     int64  `json:"time"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil || envelope.Content == nil {
		return xplora.ChatMessage{}, "", false
	}
	ct := envelope.Content
	if ct.MsgID == 0 || ct.MsgType == "" {
		return xplora.ChatMessage{}, "", false
	}

	// Emoticon FCM payloads don't include the emoticon_id; the full data is only
	// available via chatsNew. Returning false triggers a poll which fetches it.
	if ct.MsgType == "chat_emoticon" {
		return xplora.ChatMessage{}, "", false
	}

	// Identify the watch UID: the child's ID is either the receiver
	// (parent→child message) or the sender (child→parent message).
	// The Xplora signIn API returns the watch DEVICE ID as the ward ID, but
	// FCM messages use the child's user ACCOUNT ID (a shorter hex string).
	// We match against both the stored ChildUID and any previously learned FCMID.
	// On first match we record the FCMID so subsequent messages match immediately.
	wuid := ""
	matchIdx := -1
	for i, w := range c.meta.Children {
		childUID := w.ChildUID()
		if ct.Receiver == childUID || ct.Sender == childUID ||
			(w.FCMID != "" && (ct.Receiver == w.FCMID || ct.Sender == w.FCMID)) {
			wuid = childUID
			matchIdx = i
			break
		}
	}
	// Fallback: if we couldn't match and there's exactly one child, assume the
	// message is for them and learn their FCM user ID from the payload.
	if wuid == "" && len(c.meta.Children) == 1 {
		wuid = c.meta.Children[0].ChildUID()
		matchIdx = 0
		c.log.Debug().
			Str("sender", ct.Sender).
			Str("receiver", ct.Receiver).
			Str("assumed_wuid", wuid).
			Msg("FCM direct parse: single child, assuming message is for them")
	}
	if wuid == "" {
		knownUIDs := make([]string, 0, len(c.meta.Children))
		for _, w := range c.meta.Children {
			knownUIDs = append(knownUIDs, w.ChildUID())
		}
		c.log.Debug().
			Str("sender", ct.Sender).
			Str("receiver", ct.Receiver).
			Strs("known_child_uids", knownUIDs).
			Msg("FCM direct parse: could not match sender/receiver to a known child")
		return xplora.ChatMessage{}, "", false
	}
	// Learn the child's FCM user ID if we don't have it yet.
	// In a parent→child message the receiver IS the child's FCM account ID.
	// In a child→parent message the sender is the child's FCM account ID.
	if matchIdx >= 0 && c.meta.Children[matchIdx].FCMID == "" {
		fcmID := ""
		if ct.Receiver != wuid && ct.Receiver != "" {
			fcmID = ct.Receiver
		} else if ct.Sender != wuid && ct.Sender != c.meta.UserID && ct.Sender != "" {
			fcmID = ct.Sender
		}
		if fcmID != "" {
			c.meta.Children[matchIdx].FCMID = fcmID
			ctx := c.userLogin.Log.WithContext(context.Background())
			c.userLogin.Save(ctx)
			c.log.Debug().Str("wuid", wuid).Str("fcm_id", fcmID).Msg("Learned FCM user ID for child")
		}
	}

	msgIDStr := fmt.Sprintf("%d", ct.MsgID)
	msgType := fcmMsgTypeToXplora(ct.MsgType)

	// Encode text as a JSON string so extractMessageText can parse it.
	var dataJSON json.RawMessage
	if ct.Text != "" {
		dataJSON, _ = json.Marshal(ct.Text)
	}

	var senderRef *xplora.UserRef
	if ct.Sender != "" {
		senderRef = &xplora.UserRef{ID: ct.Sender}
	}

	// FCM time is Unix milliseconds; convert to seconds to match chatsNew.
	createSec := ct.Time / 1000

	return xplora.ChatMessage{
		ID:     msgIDStr,
		MsgID:  msgIDStr,
		Type:   &msgType,
		Sender: senderRef,
		Data:   dataJSON,
		Create: &createSec,
	}, wuid, true
}

// fcmMsgTypeToXplora maps an FCM msg_type value to the Xplora API type label
// used in chatsNew responses, so both paths produce consistent message types.
func fcmMsgTypeToXplora(fcmType string) string {
	switch fcmType {
	case "chat_text":
		return "TEXT"
	case "chat_image":
		return "IMAGE"
	case "chat_voice":
		return "VOICE"
	case "chat_emoticon":
		return "EMOTICON"
	default:
		return fcmType
	}
}

// extractFCMSenderIcon returns the sender_icon URL from an Xplora FCM payload.
// In Xplora pushes, sender_icon is always the child's avatar (the watch user
// the chat is with), regardless of message direction.
func extractFCMSenderIcon(raw json.RawMessage) string {
	var envelope struct {
		Content *struct {
			SenderIcon string `json:"sender_icon"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil || envelope.Content == nil {
		return ""
	}
	return envelope.Content.SenderIcon
}

// updateChildAvatar stores a new avatar URL for the child identified by wuid,
// persists it to login metadata, and triggers a portal/ghost resync.
func (c *XploraClient) updateChildAvatar(wuid, avatarURL string) {
	idx := -1
	for i, w := range c.meta.Children {
		if w.ChildUID() == wuid {
			idx = i
			break
		}
	}
	if idx < 0 || c.meta.Children[idx].AvatarURL == avatarURL {
		return // unknown child or nothing changed
	}
	c.meta.Children[idx].AvatarURL = avatarURL
	ctx := c.userLogin.Log.WithContext(context.Background())
	c.userLogin.Save(ctx)
	c.log.Debug().Str("wuid", wuid).Str("url", avatarURL).Msg("Updated child avatar URL")
	// Re-sync the portal so the room avatar and ghost avatar are refreshed.
	c.ensureWatchPortal(ctx, c.meta.Children[idx])
}

// makeURLAvatar builds a bridgev2.Avatar that downloads an image from url.
func makeURLAvatar(avatarURL string) *bridgev2.Avatar {
	return &bridgev2.Avatar{
		ID: networkid.AvatarID(avatarURL),
		Get: func(ctx context.Context) ([]byte, error) {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, avatarURL, nil)
			if err != nil {
				return nil, err
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return nil, err
			}
			defer resp.Body.Close()
			return io.ReadAll(resp.Body)
		},
	}
}

// updateLastMsgID advances the LastMsgID cursor in portal metadata if msgID is newer.
func (c *XploraClient) updateLastMsgID(wuid, msgID string) {
	if msgID == "" {
		return
	}
	ctx := c.userLogin.Log.WithContext(context.Background())
	portalKey := networkid.PortalKey{
		ID:       portalIDFromWUID(wuid),
		Receiver: c.userLogin.ID,
	}
	portal, err := c.userLogin.Bridge.GetExistingPortalByKey(ctx, portalKey)
	if err != nil || portal == nil {
		return
	}
	if meta, ok := portal.Metadata.(*PortalMetadata); ok && msgID > meta.LastMsgID {
		meta.LastMsgID = msgID
		portal.Save(ctx)
	}
}

// startPolling begins a 30-second polling loop over chatsNew for all watches.
func (c *XploraClient) startPolling(ctx context.Context) {
	c.pollMu.Lock()
	defer c.pollMu.Unlock()
	if c.pollCancel != nil {
		return // already polling
	}
	pollCtx, cancel := context.WithCancel(ctx)
	c.pollCancel = cancel
	go c.pollLoop(pollCtx)
}

// stopPolling cancels the polling goroutine.
func (c *XploraClient) stopPolling() {
	c.pollMu.Lock()
	defer c.pollMu.Unlock()
	if c.pollCancel != nil {
		c.pollCancel()
		c.pollCancel = nil
	}
}

// pollLoop polls all watches every 30 seconds.
func (c *XploraClient) pollLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.pollAllWatches(ctx)
		}
	}
}

// pollAllWatches fetches new messages for all linked watches.
func (c *XploraClient) pollAllWatches(ctx context.Context) {
	c.log.Debug().Int("count", len(c.meta.Children)).Msg("Polling Xplora watches")
	for _, w := range c.meta.Children {
		c.pollWatch(ctx, w.ChildUID())
	}
}

// pollWatch fetches recent messages for a single watch and dispatches new ones.
// On the first call (LastMsgID == ""), it only initialises the cursor to the
// newest message without dispatching history. This prevents a flood of old
// messages from appearing in the Matrix room on first connection.
func (c *XploraClient) pollWatch(ctx context.Context, wuid string) {
	portalKey := networkid.PortalKey{
		ID:       portalIDFromWUID(wuid),
		Receiver: c.userLogin.ID,
	}

	// Get the portal to check last seen message ID.
	portal, err := c.userLogin.Bridge.GetExistingPortalByKey(ctx, portalKey)
	lastMsgID := ""
	if err == nil && portal != nil {
		if meta, ok := portal.Metadata.(*PortalMetadata); ok {
			lastMsgID = meta.LastMsgID
		}
	}

	msgs, err := c.gql.GetChats(ctx, wuid, 0, 20, lastMsgID)
	if err != nil {
		c.log.Warn().Err(err).Str("wuid", wuid).Msg("Poll: failed to get chats")
		return
	}
	if len(msgs) == 0 {
		return
	}

	newest := msgs[0].MsgID

	// Find messages newer than the cursor and dispatch them in chronological order.
	// On the very first poll (lastMsgID == "") we dispatch all fetched messages so
	// that the message which triggered the poll (e.g. an FCM push we couldn't
	// match directly) is not silently dropped.
	var newMsgs []xplora.ChatMessage
	for _, msg := range msgs {
		if lastMsgID == "" || msg.MsgID > lastMsgID {
			newMsgs = append(newMsgs, msg)
		}
	}
	if lastMsgID == "" && len(newMsgs) == 0 {
		// No messages at all on first poll — just record the cursor position.
		if portal != nil {
			if meta, ok := portal.Metadata.(*PortalMetadata); ok && newest > meta.LastMsgID {
				meta.LastMsgID = newest
				portal.Save(ctx)
			}
		}
		return
	}
	for i := len(newMsgs) - 1; i >= 0; i-- {
		msg := newMsgs[i]
		isFromMe := msg.Sender != nil && msg.Sender.ID == c.meta.UserID
		c.dispatchChatMessage(wuid, msg, isFromMe)
	}

	// Advance the cursor.
	if portal != nil {
		if meta, ok := portal.Metadata.(*PortalMetadata); ok && newest > meta.LastMsgID {
			meta.LastMsgID = newest
			portal.Save(ctx)
		}
	}
}

// dispatchChatMessage queues a chat message as a Matrix remote event.
func (c *XploraClient) dispatchChatMessage(wuid string, msg xplora.ChatMessage, isFromMe bool) {
	tm := time.Now()
	if msg.Create != nil {
		tm = time.Unix(*msg.Create, 0) // Xplora chatsNew create is Unix seconds
	}

	sender := ghostIDFromWUID(wuid)
	if isFromMe {
		sender = networkid.UserID(c.meta.UserID)
	}

	c.userLogin.Bridge.QueueRemoteEvent(c.userLogin, &simplevent.Message[xplora.ChatMessage]{
		EventMeta: simplevent.EventMeta{
			Type: bridgev2.RemoteEventMessage,
			PortalKey: networkid.PortalKey{
				ID:       portalIDFromWUID(wuid),
				Receiver: c.userLogin.ID,
			},
			CreatePortal: true,
			Sender: bridgev2.EventSender{
				Sender:   sender,
				IsFromMe: isFromMe,
			},
			Timestamp: tm,
		},
		Data:               msg,
		ID:                 networkid.MessageID(msg.MsgID),
		ConvertMessageFunc: c.convertChatMessage,
	})
}

// syncWatches ensures a portal exists for each child watch stored in metadata.
func (c *XploraClient) syncWatches(ctx context.Context) {
	c.log.Info().Int("count", len(c.meta.Children)).Msg("Syncing watches from metadata")
	for _, w := range c.meta.Children {
		c.log.Info().
			Str("wuid", w.ChildUID()).
			Str("name", w.ChildName()).
			Str("avatar_url", w.AvatarURL).
			Msg("Ensuring portal for watch")
		c.ensureWatchPortal(ctx, w)
	}
}

// ensureWatchPortal creates or updates the portal for a child's watch.
func (c *XploraClient) ensureWatchPortal(ctx context.Context, w xplora.WatchInfo) {
	wuid := w.ChildUID()
	childName := w.ChildName()
	avatarURL := w.AvatarURL
	portalKey := networkid.PortalKey{
		ID:       portalIDFromWUID(wuid),
		Receiver: c.userLogin.ID,
	}
	// Queue a portal info update event to ensure the room exists.
	c.userLogin.Bridge.QueueRemoteEvent(c.userLogin, &simplevent.ChatResync{
		EventMeta: simplevent.EventMeta{
			Type:         bridgev2.RemoteEventChatResync,
			PortalKey:    portalKey,
			CreatePortal: true,
		},
		GetChatInfoFunc: func(ctx context.Context, _ *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
			ghostInfo := &bridgev2.UserInfo{Name: ptrStr(childName)}
			if avatarURL != "" {
				ghostInfo.Avatar = makeURLAvatar(avatarURL)
			}
			members := []bridgev2.ChatMember{
				{
					EventSender: bridgev2.EventSender{IsFromMe: true},
					Membership:  event.MembershipJoin,
				},
				{
					EventSender: bridgev2.EventSender{Sender: ghostIDFromWUID(wuid)},
					Membership:  event.MembershipJoin,
					UserInfo:    ghostInfo,
				},
			}
			name := childName
			info := &bridgev2.ChatInfo{
				Name:    &name,
				Members: &bridgev2.ChatMemberList{IsFull: true, Members: members},
				ExtraUpdates: func(_ context.Context, portal *bridgev2.Portal) bool {
					meta, ok := portal.Metadata.(*PortalMetadata)
					if !ok {
						meta = &PortalMetadata{}
						portal.Metadata = meta
					}
					changed := false
					if meta.WUID != wuid {
						meta.WUID = wuid
						changed = true
					}
					if meta.ChildName != childName {
						meta.ChildName = childName
						changed = true
					}
					return changed
				},
			}
			if avatarURL != "" {
				info.Avatar = makeURLAvatar(avatarURL)
			}
			return info, nil
		},
	})
}

// tryRefreshToken exchanges the stored refresh token for a new access token
// and saves the updated credentials to disk.
func (c *XploraClient) tryRefreshToken(ctx context.Context) error {
	uid := c.auth.UserID()
	refreshToken := c.auth.RefreshToken()
	if uid == "" || refreshToken == "" {
		return fmt.Errorf("no refresh token available")
	}
	resp, err := c.gql.RefreshToken(ctx, uid, refreshToken)
	if err != nil {
		return fmt.Errorf("refreshToken: %w", err)
	}
	if !resp.Valid || resp.Token == "" {
		return fmt.Errorf("refreshToken: server returned invalid token")
	}
	creds := &xplora.Credentials{
		Token:        resp.Token,
		RefreshToken: resp.RefreshToken,
		ExpireDate:   string(resp.ExpireDate),
		UserID:       uid,
	}
	if err := c.auth.SetCredentials(creds); err != nil {
		return fmt.Errorf("saving refreshed credentials: %w", err)
	}
	c.log.Info().Msg("Access token refreshed successfully")
	return nil
}

// ptrStr returns a pointer to a string (helper for bridgev2 APIs).
func ptrStr(s string) *string { return &s }

// removeFile removes a file, ignoring not-exist errors.
func removeFile(path string) {
	_ = removeFileImpl(path)
}
