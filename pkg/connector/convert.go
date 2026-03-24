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
		ts = time.Unix(*msg.Create, 0) // Xplora chatsNew create is Unix seconds
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

// formatEmoticon parses EMOTICON data and returns a human-readable description.
// Expected fields: sender_name, emoticon_id.
func formatEmoticon(data json.RawMessage) string {
	var d struct {
		SenderName string `json:"sender_name"`
	}
	if err := json.Unmarshal(data, &d); err != nil {
		return ""
	}
	if d.SenderName != "" {
		return fmt.Sprintf("[Sticker from %s]", d.SenderName)
	}
	return "[Sticker]"
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
