package connector

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/commands"
	"maunium.net/go/mautrix/event"
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

		// Ask the watch to push a fresh GPS fix (best-effort; location update is async).
		if err := client.gql.AskWatchLocate(ce.Ctx, meta.WUID); err != nil {
			client.log.Warn().Err(err).Str("wuid", meta.WUID).Msg("locate: askWatchLocate failed")
		}

		loc, err := client.gql.GetWatchLastLocation(ce.Ctx, meta.WUID)
		if err != nil {
			ce.Reply("Failed to get location: %v", err)
			return
		}
		if loc == nil || (loc.Lat == 0 && loc.Lng == 0) {
			ce.Reply("No location data available for this watch.")
			return
		}

		// Tm may be Unix seconds or Unix milliseconds depending on API version.
		var ts time.Time
		if loc.Tm > 1e12 {
			ts = time.UnixMilli(loc.Tm)
		} else {
			ts = time.Unix(loc.Tm, 0)
		}

		// Send as a native Matrix location event. Element renders this as an
		// interactive map with a pin. The body acts as fallback text for
		// clients that don't support m.location.
		geoURI := fmt.Sprintf("geo:%.6f,%.6f", loc.Lat, loc.Lng)
		var bodyParts []string
		if loc.Addr != "" {
			bodyParts = append(bodyParts, loc.Addr)
		} else {
			bodyParts = append(bodyParts, fmt.Sprintf("%.6f, %.6f", loc.Lat, loc.Lng))
		}
		bodyParts = append(bodyParts, formatAge(ts))
		if loc.Battery > 0 {
			if loc.IsCharging {
				bodyParts = append(bodyParts, fmt.Sprintf("🔋 %d%% ⚡", loc.Battery))
			} else {
				bodyParts = append(bodyParts, fmt.Sprintf("🔋 %d%%", loc.Battery))
			}
		}

		_, _ = ce.Bot.SendMessage(ce.Ctx, ce.Portal.MXID, event.EventMessage, &event.Content{
			Parsed: &event.MessageEventContent{
				MsgType: event.MsgLocation,
				Body:    strings.Join(bodyParts, " — "),
				GeoURI:  geoURI,
			},
		}, nil)
	},
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
