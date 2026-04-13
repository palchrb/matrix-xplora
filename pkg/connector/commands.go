package connector

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/commands"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// RegisterCommands registers Xplora-specific bot commands with the bridge processor.
// Called from main.go via BridgeMain.PostInit after the bridge is initialised.
func RegisterCommands(br *bridgev2.Bridge) {
	br.Commands.(*commands.Processor).AddHandler(cmdLocate)
	br.Commands.(*commands.Processor).AddHandler(cmdStickers)
}

var cmdLocate = &commands.FullHandler{
	Name:           "locate",
	RequiresLogin:  true,
	RequiresPortal: true,
	Help: commands.HelpMeta{
		Section:     commands.HelpSectionChats,
		Description: "Request the current GPS location of the linked watch.",
	},
	Func: func(ce *commands.Event) {
		meta, ok := ce.Portal.Metadata.(*PortalMetadata)
		if !ok || meta.WUID == "" {
			ce.Reply("This room is not linked to a watch.")
			return
		}
		login := ce.User.GetDefaultLogin()
		if login == nil {
			ce.Reply("You are not logged in.")
			return
		}
		client, ok := login.Client.(*XploraClient)
		if !ok {
			ce.Reply("Internal error: unexpected client type.")
			return
		}

		// Ask the watch to push a fresh GPS fix (result arrives via FCM).
		_ = client.gql.AskWatchLocate(ce.Ctx, meta.WUID)
		ce.React("✅")

		// Wait up to 60s for the FCM location_update to arrive. The watch may take
		// up to ~25s to push the GPS fix via FCM, so 60s gives ample headroom.
		// If no FCM push arrives within the window, fall back to the last cached location.
		locateCh := client.beginLocate(meta.WUID)
		wuid := meta.WUID
		go func() {
			select {
			case <-locateCh:
				// FCM response arrived and is being collected — nothing more to do.
				return
			case <-time.After(60 * time.Second):
			}
			// Fallback: show the last known cached location.
			loc, err := client.gql.GetWatchLastLocation(context.Background(), wuid)
			if err != nil || loc == nil || (loc.Lat == 0 && loc.Lng == 0) {
				client.log.Debug().Str("wuid", wuid).Msg("locate: no location available after FCM timeout")
				sendLocateFailure(ce.Bot, ce.OrigRoomID, ce.EventID)
				return
			}
			var ts time.Time
			if loc.Tm > 1e12 {
				ts = time.UnixMilli(loc.Tm)
			} else {
				ts = time.Unix(loc.Tm, 0)
			}
			client.dispatchLocationMessage(wuid, loc, ts)
		}()
	},
}

// sendLocateFailure sends a notice to the Matrix room when a !locate command
// produced no result — either the watch didn't respond via FCM and there is no
// cached location available (watch likely offline or out of coverage).
// Uses context.Background() because ce.Ctx may already be cancelled by the
// time the 60s goroutine timer fires.
func sendLocateFailure(bot bridgev2.MatrixAPI, roomID id.RoomID, cmdEventID id.EventID) {
	content := event.MessageEventContent{
		MsgType: event.MsgNotice,
		Body:    "📍 No location available — watch may be offline or out of coverage.",
		RelatesTo: &event.RelatesTo{
			InReplyTo: &event.InReplyTo{EventID: cmdEventID},
		},
	}
	_, _ = bot.SendMessage(context.Background(), roomID, event.EventMessage, &event.Content{Parsed: &content}, nil)
}

var cmdStickers = &commands.FullHandler{
	Name:           "stickers",
	RequiresLogin:  false,
	RequiresPortal: false,
	Help: commands.HelpMeta{
		Section:     commands.HelpSectionUnclassified,
		Description: "List native Xplora stickers you can send from Matrix.",
	},
	Func: func(ce *commands.Event) {
		keys := make([]int, 0, len(xploraEmoticonMap))
		for k := range xploraEmoticonMap {
			keys = append(keys, k)
		}
		sort.Ints(keys)
		seen := map[string]bool{}
		stickers := make([]string, 0, len(keys))
		for _, k := range keys {
			e := xploraEmoticonMap[k]
			if !seen[e] {
				stickers = append(stickers, e)
				seen[e] = true
			}
		}
		ce.Reply("Native Xplora stickers — send one of these as a single emoji:\n%s",
			strings.Join(stickers, " "))
	},
}

// formatAge returns a human-readable relative time string (e.g. "3 minutes ago",
// "8 hours ago"). Timezone-independent — important because the bridge runs in UTC
// while users may be in any timezone.
func formatAge(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		m := int(d.Minutes())
		if m == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", m)
	case d < 24*time.Hour:
		h := int(d.Hours())
		if h == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", h)
	default:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	}
}
