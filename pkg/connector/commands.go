package connector

import (
	"fmt"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/commands"
)

// RegisterCommands registers Xplora-specific bot commands with the bridge processor.
// Called from main.go via BridgeMain.PostInit after the bridge is initialised.
func RegisterCommands(br *bridgev2.Bridge) {
	br.Commands.(*commands.Processor).AddHandler(cmdLocate)
}

var cmdLocate = &commands.FullHandler{
	Name:           "locate",
	RequiresLogin:  true,
	RequiresPortal: true,
	Help: commands.HelpMeta{
		Section:     commands.HelpSectionChats,
		Description: "Request the current GPS location of the linked watch and return a map link.",
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

		mapsURL := fmt.Sprintf("https://maps.google.com/?q=%.6f,%.6f", loc.Lat, loc.Lng)
		var sb strings.Builder
		fmt.Fprintf(&sb, "📍 [Open in Google Maps](%s)\n", mapsURL)
		fmt.Fprintf(&sb, "_As of %s_\n", ts.Format("15:04, 2 Jan 2006"))
		if loc.Addr != "" {
			fmt.Fprintf(&sb, "%s\n", loc.Addr)
		}
		if loc.Battery > 0 {
			if loc.IsCharging {
				fmt.Fprintf(&sb, "🔋 Battery: %d%% ⚡", loc.Battery)
			} else {
				fmt.Fprintf(&sb, "🔋 Battery: %d%%", loc.Battery)
			}
		}
		ce.Reply(strings.TrimRight(sb.String(), "\n"))
	},
}
