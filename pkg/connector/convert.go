package connector

import (
	"context"

	"github.com/palchrb/matrix-xplora/internal/xplora"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

// convertChatMessage converts an xplora.ChatMessage into Matrix events.
// Called by the simplevent.Message handler inside the bridge event loop.
func (c *XploraClient) convertChatMessage(
	ctx context.Context,
	_ *bridgev2.Portal,
	_ bridgev2.MatrixAPI,
	msg xplora.ChatMessage,
) (*bridgev2.ConvertedMessage, error) {
	body := ""
	if msg.Text != nil {
		body = *msg.Text
	}
	if body == "" {
		body = "[Empty Xplora message]"
	}

	return &bridgev2.ConvertedMessage{
		Parts: []*bridgev2.ConvertedMessagePart{{
			Type: event.EventMessage,
			Content: &event.MessageEventContent{
				MsgType: event.MsgText,
				Body:    body,
			},
		}},
	}, nil
}
