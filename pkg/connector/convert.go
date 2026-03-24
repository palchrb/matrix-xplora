package connector

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/palchrb/matrix-xplora/internal/xplora"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

// convertChatMessage converts an xplora.ChatMessage into Matrix events.
// The message body is extracted from the `data` JSON blob in the API response.
// Called by the simplevent.Message handler inside the bridge event loop.
func (c *XploraClient) convertChatMessage(
	ctx context.Context,
	_ *bridgev2.Portal,
	_ bridgev2.MatrixAPI,
	msg xplora.ChatMessage,
) (*bridgev2.ConvertedMessage, error) {
	body := extractMessageText(msg)

	ts := time.Now()
	if msg.Create != nil {
		ts = time.UnixMilli(*msg.Create)
	}

	msgType := derefStr(msg.Type)

	extra := map[string]any{
		"com.xplora.msg_type": msgType,
		"com.xplora.msg_id":   msg.MsgID,
		"com.xplora.ts":       ts.UnixMilli(),
	}

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

// extractMessageText parses the `data` JSON blob from the Xplora API response.
// The data field is a JSON string containing type-specific content.
// For TEXT messages it contains the text body; other types get a fallback label.
func extractMessageText(msg xplora.ChatMessage) string {
	msgType := derefStr(msg.Type)

	if msg.Data != nil && *msg.Data != "" {
		// data is a JSON-encoded string: first try plain string value
		var text string
		if err := json.Unmarshal([]byte(*msg.Data), &text); err == nil && text != "" {
			return text
		}

		// data may be a JSON object with a "text" field
		var obj map[string]json.RawMessage
		if err := json.Unmarshal([]byte(*msg.Data), &obj); err == nil {
			if raw, ok := obj["text"]; ok {
				var text string
				if err := json.Unmarshal(raw, &text); err == nil && text != "" {
					return text
				}
			}
		}

		// Return raw data for unknown structure so nothing is silently lost
		return fmt.Sprintf("[%s: %s]", msgType, *msg.Data)
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

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
