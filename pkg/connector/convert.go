package connector

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/palchrb/matrix-xplora/internal/xplora"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

// xploraEmoticonMap maps Xplora emoticon IDs to Unicode emoji characters.
// Source: pyxplora_api status.py — M<id> enum values.
var xploraEmoticonMap = map[int]string{
	1001: "😄", 1002: "😏", 1003: "😘", 1004: "😅", 1005: "😂",
	1006: "😭", 1007: "😍", 1008: "😎", 1009: "😜", 1010: "😳",
	1011: "🥱", 1012: "👏", 1013: "😡", 1014: "👍", 1015: "😏",
	1016: "😓", 1017: "🍧", 1018: "😮", 1020: "🎁", 1022: "☺️", 1024: "🌹",
}

// emojiToXploraEmoticon is the reverse of xploraEmoticonMap: maps a Unicode
// emoji to the Xplora ChatEmoticonType enum value (e.g. "M1001").
// Used when a Matrix user sends a single emoji that matches an Xplora sticker.
var emojiToXploraEmoticon = func() map[string]string {
	m := make(map[string]string, len(xploraEmoticonMap))
	for id, emoji := range xploraEmoticonMap {
		key := fmt.Sprintf("M%d", id)
		if _, exists := m[emoji]; !exists { // keep first mapping on duplicate emoji
			m[emoji] = key
		}
	}
	return m
}()

// convertChatMessage converts an xplora.ChatMessage into Matrix events.
// IMAGE and VOICE messages are downloaded from the Xplora CDN and reuploaded
// to the Matrix media repository. Other types fall back to text.
func (c *XploraClient) convertChatMessage(
	ctx context.Context,
	portal *bridgev2.Portal,
	intent bridgev2.MatrixAPI,
	msg xplora.ChatMessage,
) (*bridgev2.ConvertedMessage, error) {
	msgType := derefStr(msg.Type)

	ts := time.Now()
	if msg.Create != nil {
		ts = time.Unix(*msg.Create, 0) // Xplora chatsNew create is Unix seconds
	}

	extra := map[string]any{
		"com.xplora.msg_type": msgType,
		"com.xplora.msg_id":   msg.MsgID,
		"com.xplora.ts":       ts.UnixMilli(),
	}

	// IMAGE and VOICE: fetch media URL, download, reupload to Matrix.
	switch msgType {
	case "IMAGE", "VOICE":
		part, err := c.bridgeIncomingMedia(ctx, portal, intent, msg, msgType)
		if err != nil {
			c.log.Warn().Err(err).
				Str("msg_id", msg.MsgID).
				Str("msg_type", msgType).
				Msg("Failed to bridge media, using placeholder text")
			// Fall through to text path below.
		} else {
			part.Extra = extra
			return &bridgev2.ConvertedMessage{Parts: []*bridgev2.ConvertedMessagePart{part}}, nil
		}

	case "TRACKER_UPDATE":
		if part := convertLocationUpdate(msg, extra); part != nil {
			return &bridgev2.ConvertedMessage{Parts: []*bridgev2.ConvertedMessagePart{part}}, nil
		}
		// Fall through to text path if parsing fails.
	}

	body := extractMessageText(msg)
	return &bridgev2.ConvertedMessage{
		Parts: []*bridgev2.ConvertedMessagePart{{
			Type: event.EventMessage,
			Content: &event.MessageEventContent{
				MsgType: event.MsgText,
				Body:    body,
			},
			Extra: extra,
		}},
	}, nil
}

// convertLocationUpdate parses a TRACKER_UPDATE chat message and returns an
// m.location event part. Returns nil if lat/lng cannot be parsed.
func convertLocationUpdate(msg xplora.ChatMessage, extra map[string]any) *bridgev2.ConvertedMessagePart {
	if len(msg.Data) == 0 || string(msg.Data) == "null" {
		return nil
	}
	var loc struct {
		Lat  float64 `json:"lat"`
		Lng  float64 `json:"lng"`
		Addr string  `json:"addr"`
	}
	if err := json.Unmarshal(msg.Data, &loc); err != nil || (loc.Lat == 0 && loc.Lng == 0) {
		return nil
	}
	geoURI := fmt.Sprintf("geo:%.6f,%.6f", loc.Lat, loc.Lng)
	body := loc.Addr
	if body == "" {
		body = fmt.Sprintf("%.6f, %.6f", loc.Lat, loc.Lng)
	}
	return &bridgev2.ConvertedMessagePart{
		Type: event.EventMessage,
		Content: &event.MessageEventContent{
			MsgType: event.MsgLocation,
			Body:    body,
			GeoURI:  geoURI,
		},
		Extra: extra,
	}
}

// bridgeIncomingMedia fetches an image or voice message from the Xplora CDN
// and uploads it to the Matrix media repository via the ghost user's intent.
func (c *XploraClient) bridgeIncomingMedia(
	ctx context.Context,
	portal *bridgev2.Portal,
	intent bridgev2.MatrixAPI,
	msg xplora.ChatMessage,
	msgType string,
) (*bridgev2.ConvertedMessagePart, error) {
	meta, ok := portal.Metadata.(*PortalMetadata)
	if !ok || meta.WUID == "" {
		return nil, fmt.Errorf("portal has no WUID")
	}

	// FetchChatImage / FetchChatVoice return base64-encoded binary data,
	// not an HTTP URL. Decode the payload directly.
	var b64data string
	var fetchErr error
	switch msgType {
	case "IMAGE":
		b64data, fetchErr = c.gql.FetchChatImage(ctx, meta.WUID, msg.MsgID)
	case "VOICE":
		b64data, fetchErr = c.gql.FetchChatVoice(ctx, meta.WUID, msg.MsgID)
	default:
		return nil, fmt.Errorf("unsupported media type: %s", msgType)
	}
	if fetchErr != nil {
		return nil, fmt.Errorf("fetch media data: %w", fetchErr)
	}
	if b64data == "" {
		return nil, fmt.Errorf("empty media data returned by API")
	}

	data, err := base64.StdEncoding.DecodeString(b64data)
	if err != nil {
		// Try RawStdEncoding (no padding) as a fallback.
		data, err = base64.RawStdEncoding.DecodeString(b64data)
		if err != nil {
			return nil, fmt.Errorf("decode base64 media data: %w", err)
		}
	}

	// Detect MIME type from content; fall back to sensible defaults.
	mimeType := http.DetectContentType(data)
	var mxMsgType event.MessageType
	var filename string
	switch msgType {
	case "IMAGE":
		mxMsgType = event.MsgImage
		filename = "image.jpg"
		if !strings.HasPrefix(mimeType, "image/") {
			mimeType = "image/jpeg"
		}
	case "VOICE":
		mxMsgType = event.MsgAudio
		oggData, transcodeErr := FromXploraAMR(ctx, data)
		if transcodeErr != nil {
			c.log.Warn().Err(transcodeErr).Msg("AMR→OGG transcode failed, uploading raw AMR")
			filename = "voice.amr"
			mimeType = "audio/amr"
		} else {
			data = oggData
			filename = "voice.ogg"
			mimeType = "audio/ogg"
		}
	}

	mxcURI, encryptedFile, err := intent.UploadMedia(ctx, "", data, filename, mimeType)
	if err != nil {
		return nil, fmt.Errorf("upload to Matrix: %w", err)
	}

	content := &event.MessageEventContent{
		MsgType: mxMsgType,
		Body:    filename,
		URL:     mxcURI,
		File:    encryptedFile,
		Info: &event.FileInfo{
			MimeType: mimeType,
			Size:     len(data),
		},
	}
	if mxMsgType == event.MsgAudio {
		content.MSC3245Voice = &event.MSC3245Voice{}
		if mimeType == "audio/ogg" {
			waveform, durationMS := ExtractWaveformAndDuration(ctx, data, "ogg")
			content.Info.Duration = durationMS
			content.MSC1767Audio = &event.MSC1767Audio{
				Duration: durationMS,
				Waveform: waveform,
			}
		} else {
			content.MSC1767Audio = &event.MSC1767Audio{Waveform: []int{}}
		}
	}

	return &bridgev2.ConvertedMessagePart{
		Type:    event.EventMessage,
		Content: content,
	}, nil
}

// extractMessageText parses the `data` JSON value from the Xplora API response.
// data can be a JSON string, a JSON object with a "text" field, or absent.
// For TEXT messages it contains the text body; other types get a fallback label.
func extractMessageText(msg xplora.ChatMessage) string {
	msgType := derefStr(msg.Type)

	if len(msg.Data) > 0 && string(msg.Data) != "null" {
		// Type-specific structured parsing before the generic fallback.
		switch msgType {
		case "CALL_LOG":
			if text := formatCallLog(msg.Data); text != "" {
				return text
			}
		case "EMOTICON":
			if text := formatEmoticon(msg.Data); text != "" {
				return text
			}
		}

		// data may be a plain JSON string
		var text string
		if err := json.Unmarshal(msg.Data, &text); err == nil && text != "" {
			return text
		}

		// data may be a JSON object with a "text" field
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(msg.Data, &obj); err == nil {
			if raw, ok := obj["text"]; ok {
				var t string
				if err := json.Unmarshal(raw, &t); err == nil && t != "" {
					return t
				}
			}
		}

		// Return raw data for unknown structure so nothing is silently lost
		return fmt.Sprintf("[%s: %s]", msgType, string(msg.Data))
	}

	// No data — use human-readable type label as fallback
	switch msgType {
	case "TEXT":
		return "[Empty text message]"
	case "IMAGE":
		return "[Image]"
	case "VOICE":
		return "[Voice message]"
	case "EMOTICON":
		return "[Sticker]"
	case "SOS":
		return "[SOS alert]"
	case "LEAVE_SAFE_ZONE":
		return "[Left safe zone]"
	case "LOW_BATTERY":
		return "[Low battery]"
	case "TRACKER_UPDATE":
		return "[Location update]"
	default:
		if msgType != "" {
			return fmt.Sprintf("[%s]", msgType)
		}
		return "[Xplora message]"
	}
}

// formatCallLog parses CALL_LOG data and returns a human-readable description.
// Expected fields: call_type ("incoming"/"outgoing"/"missed"), call_name, duration (seconds).
func formatCallLog(data json.RawMessage) string {
	var d struct {
		CallType string `json:"call_type"`
		CallName string `json:"call_name"`
		Duration int    `json:"duration"`
	}
	if err := json.Unmarshal(data, &d); err != nil || d.CallName == "" {
		return ""
	}
	switch d.CallType {
	case "incoming":
		if d.Duration > 0 {
			return fmt.Sprintf("[Incoming call from %s (%ds)]", d.CallName, d.Duration)
		}
		return fmt.Sprintf("[Incoming call from %s]", d.CallName)
	case "outgoing":
		if d.Duration > 0 {
			return fmt.Sprintf("[Call to %s (%ds)]", d.CallName, d.Duration)
		}
		return fmt.Sprintf("[Call to %s]", d.CallName)
	case "missed":
		return fmt.Sprintf("[Missed call from %s]", d.CallName)
	default:
		return fmt.Sprintf("[Call with %s]", d.CallName)
	}
}

// formatEmoticon parses EMOTICON data and returns the corresponding Unicode emoji.
// The emoticon_id field (int or string) is looked up in xploraEmoticonMap.
func formatEmoticon(data json.RawMessage) string {
	var d struct {
		EmoticonID any `json:"emoticon_id"`
	}
	if err := json.Unmarshal(data, &d); err != nil || d.EmoticonID == nil {
		return ""
	}

	var id int
	switch v := d.EmoticonID.(type) {
	case float64:
		id = int(v)
	case string:
		fmt.Sscanf(v, "%d", &id)
	}
	if emoji, ok := xploraEmoticonMap[id]; ok {
		return emoji
	}
	return "[Sticker]"
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
