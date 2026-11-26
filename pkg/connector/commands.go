package connector

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/palchrb/matrix-xplora/internal/xplora"
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
	br.Commands.(*commands.Processor).AddHandler(cmdRenew)
	br.Commands.(*commands.Processor).AddHandler(cmdSchema)
}

// authKeywords selects the root operations shown by `schema` without an
// argument: everything that looks related to tokens, sign-in or sessions.
var authKeywords = []string{"token", "sign", "login", "logout", "refresh", "auth", "session", "issue", "password", "client"}

// cmdSchema asks the Xplora API, via GraphQL introspection, how its auth
// operations are defined. Read-only: it never calls a mutation.
var cmdSchema = &commands.FullHandler{
	Name:          "schema",
	RequiresLogin: true,
	Help: commands.HelpMeta{
		Section:     commands.HelpSectionUnclassified,
		Description: "Show how Xplora's token and sign-in operations are defined (GraphQL introspection). With a type name, show that type.",
		Args:        "[_type name_]",
	},
	Func: func(ce *commands.Event) {
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
		if len(ce.Args) > 0 {
			replyType(ce, client, ce.Args[0])
			return
		}

		queryRoot, mutationRoot, err := client.gql.IntrospectRoots(ce.Ctx)
		if err != nil {
			client.log.Warn().Err(err).Msg("Introspection failed")
			ce.Reply("Introspection failed (it may be disabled on the server): %v", err)
			return
		}
		var out strings.Builder
		returnTypes := map[string]bool{}
		for _, root := range []string{mutationRoot, queryRoot} {
			if root == "" {
				continue
			}
			t, err := client.gql.IntrospectType(ce.Ctx, root)
			if err != nil || t == nil {
				fmt.Fprintf(&out, "%s: could not read (%v)\n\n", root, err)
				continue
			}
			var matches []xplora.Field
			for _, f := range t.Fields {
				name := strings.ToLower(f.Name)
				for _, kw := range authKeywords {
					if strings.Contains(name, kw) {
						matches = append(matches, f)
						break
					}
				}
			}
			fmt.Fprintf(&out, "%s (%d of %d operations match):\n", root, len(matches), len(t.Fields))
			for _, f := range matches {
				fmt.Fprintf(&out, "  %s\n", f.Signature())
				if base := f.Type.BaseName(); base != "" && !isScalar(base) {
					returnTypes[base] = true
				}
			}
			out.WriteString("\n")
		}
		// Show the fields of the types these operations return (e.g. what a
		// refreshToken reply can contain).
		names := make([]string, 0, len(returnTypes))
		for n := range returnTypes {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			writeType(ce, client, &out, n)
		}
		client.log.Info().Str("schema", out.String()).Msg("Introspected Xplora auth schema")
		ce.Reply("```\n%s```", out.String())
	},
}

func isScalar(name string) bool {
	switch name {
	case "String", "Int", "Float", "Boolean", "ID":
		return true
	}
	return false
}

// writeType appends the fields (or input fields / enum values) of one type.
func writeType(ce *commands.Event, client *XploraClient, out *strings.Builder, name string) {
	t, err := client.gql.IntrospectType(ce.Ctx, name)
	if err != nil {
		fmt.Fprintf(out, "%s: could not read (%v)\n\n", name, err)
		return
	}
	if t == nil {
		fmt.Fprintf(out, "%s: no such type\n\n", name)
		return
	}
	fmt.Fprintf(out, "%s %s:\n", t.Kind, t.Name)
	for _, f := range t.Fields {
		fmt.Fprintf(out, "  %s\n", f.Signature())
	}
	for _, f := range t.InputFields {
		fmt.Fprintf(out, "  %s: %s\n", f.Name, f.Type.String())
	}
	for _, v := range t.EnumValues {
		fmt.Fprintf(out, "  %s\n", v.Name)
	}
	out.WriteString("\n")
}

func replyType(ce *commands.Event, client *XploraClient, name string) {
	var out strings.Builder
	writeType(ce, client, &out, name)
	client.log.Info().Str("type", name).Str("schema", out.String()).Msg("Introspected Xplora type")
	ce.Reply("```\n%s```", out.String())
}

// cmdRenew renews the Xplora token right away. The bridge also renews on its
// own shortly before each expiry (see refresh.go); this is for testing and
// troubleshooting.
var cmdRenew = &commands.FullHandler{
	Name:          "renew",
	RequiresLogin: true,
	Help: commands.HelpMeta{
		Section:     commands.HelpSectionAuth,
		Description: "Renew the Xplora login token now (the bridge also does this automatically).",
	},
	Func: func(ce *commands.Event) {
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
		before, _ := client.auth.ExpiresAt()
		client.log.Info().Time("expires", before).Msg("Manual token renewal requested")
		if err := client.tryRefreshToken(ce.Ctx); err != nil {
			client.log.Warn().Err(err).Msg("Manual token renewal failed")
			ce.Reply("❌ Renewal failed: %v\n\nIf the bridge reports BAD_CREDENTIALS, log in again with `relogin %s`.", err, login.ID)
			return
		}
		creds := client.auth.Creds()
		after, _ := client.auth.ExpiresAt()
		ce.Reply("✅ Renewal succeeded (method `%s`). Login valid until **%s** (was %s).",
			creds.RefreshMode, after.UTC().Format("2006-01-02 15:04 MST"), before.UTC().Format("2006-01-02 15:04 MST"))
	},
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
			// Fallback: show the last known cached location. Use the client
			// lifetime context so a stalled HTTP request doesn't outlive logout.
			loc, err := client.gql.GetWatchLastLocation(client.lifetimeCtx, wuid)
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
