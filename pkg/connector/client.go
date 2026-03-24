package connector

import (
	"context"
	"fmt"
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

	// Start FCM listener in background with exponential backoff.
	go func() {
		backoff := 5 * time.Second
		const maxBackoff = 5 * time.Minute
		for ctx.Err() == nil {
			if err := c.fcmClient.Listen(ctx); err != nil && ctx.Err() == nil {
				c.log.Warn().Err(err).Dur("retry_in", backoff).Msg("FCM Listen returned error, retrying")
				select {
				case <-ctx.Done():
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
func (c *XploraClient) Disconnect() {
	c.stopPolling()
	// FCM client stops when the context passed to Listen() is cancelled.
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
	members := []bridgev2.ChatMember{
		{
			EventSender: bridgev2.EventSender{IsFromMe: true},
			Membership:  event.MembershipJoin,
		},
		{
			EventSender: bridgev2.EventSender{Sender: ghostIDFromWUID(meta.WUID)},
			Membership:  event.MembershipJoin,
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
			return &bridgev2.UserInfo{Name: ptrStr(w.ChildName())}, nil
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
	default:
		return nil, fmt.Errorf("unsupported message type %s (Xplora supports text only)", msg.Content.MsgType)
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
// Since the Xplora FCM payload format is unknown until the fcm-probe tool
// discovers it, this implementation falls back to polling all watches.
// Update after running fcm-probe to parse the payload and dispatch directly.
func (c *XploraClient) handleFCMMessage(msg fcm.NewMessage) {
	c.log.Debug().RawJSON("fcm_payload", msg.Raw).Msg("Received FCM push from Xplora")

	// TODO: After fcm-probe reveals the payload structure, parse the watch UID
	// and message content here and call dispatchChatMessage directly.
	// For now, trigger a poll on all watches so nothing is lost.
	ctx := c.userLogin.Log.WithContext(context.Background())
	c.pollAllWatches(ctx)
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

	// Find new messages (those with MsgID > lastMsgID, or all if lastMsgID is empty).
	var newMsgs []xplora.ChatMessage
	for _, msg := range msgs {
		if lastMsgID == "" || msg.MsgID > lastMsgID {
			newMsgs = append(newMsgs, msg)
		}
	}

	// Dispatch in chronological order (reverse of typical API response).
	for i := len(newMsgs) - 1; i >= 0; i-- {
		msg := newMsgs[i]
		isFromMe := msg.Sender != nil && msg.Sender.ID == c.meta.UserID
		c.dispatchChatMessage(wuid, msg, isFromMe)
	}

	// Update last seen message ID.
	if len(msgs) > 0 && portal != nil {
		if meta, ok := portal.Metadata.(*PortalMetadata); ok {
			newest := msgs[0].MsgID
			if newest > meta.LastMsgID {
				meta.LastMsgID = newest
				portal.Save(ctx)
			}
		}
	}
}

// dispatchChatMessage queues a chat message as a Matrix remote event.
func (c *XploraClient) dispatchChatMessage(wuid string, msg xplora.ChatMessage, isFromMe bool) {
	tm := time.Now()
	if msg.Create != nil {
		tm = time.UnixMilli(*msg.Create)
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
		c.log.Info().Str("wuid", w.ChildUID()).Str("name", w.ChildName()).Msg("Ensuring portal for watch")
		c.ensureWatchPortal(ctx, w)
	}
}

// ensureWatchPortal creates or updates the portal for a child's watch.
func (c *XploraClient) ensureWatchPortal(ctx context.Context, w xplora.WatchInfo) {
	wuid := w.ChildUID()
	childName := w.ChildName()
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
			members := []bridgev2.ChatMember{
				{
					EventSender: bridgev2.EventSender{IsFromMe: true},
					Membership:  event.MembershipJoin,
				},
				{
					EventSender: bridgev2.EventSender{Sender: ghostIDFromWUID(wuid)},
					Membership:  event.MembershipJoin,
				},
			}
			name := childName
			return &bridgev2.ChatInfo{
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
			}, nil
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
