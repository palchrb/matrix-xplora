// Command fcm-probe is a standalone diagnostic tool for discovering the
// Xplora FCM push notification payload structure.
//
// Usage:
//
//	fcm-probe register --token <xplora-bearer-token> --uid <parent-uid> [--dir <session-dir>]
//	fcm-probe listen [--dir <session-dir>]
//
// The register subcommand:
//  1. Performs GCM checkin + registration using Xplora APK credentials.
//  2. Saves fcm_credentials.json to the session directory.
//  3. Calls setFCMToken on the Xplora API so the backend sends pushes to this device.
//
// The listen subcommand:
//  1. Loads fcm_credentials.json from the session directory.
//  2. Connects to Google's MCS (Mobile Connection Server).
//  3. Prints every raw FCM payload as JSON to stdout with a timestamp.
//
// After sending a message from a child's watch, the payload printed by listen
// reveals the exact field names needed to update internal/fcm/client.go's
// parseDataMessage function.
//
// Prerequisites:
//   - Fill in XploraSenderID, XploraAPKCertSHA1, and xploraAppPackage in
//     internal/fcm/constants.go by extracting them from the Xplora APK.
//   - Provide a valid Xplora bearer token (login via the parent app, capture
//     with a proxy, or use the bridge's login flow).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/palchrb/matrix-xplora/internal/fcm"
	"github.com/palchrb/matrix-xplora/internal/xplora"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: fcm-probe <register|listen> [flags]\n")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "register":
		cmdRegister(os.Args[2:])
	case "listen":
		cmdListen(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown subcommand: %s\n", os.Args[1])
		os.Exit(1)
	}
}

func cmdRegister(args []string) {
	fs := flag.NewFlagSet("register", flag.ExitOnError)
	dir := fs.String("dir", "fcm-probe-session", "Session directory for credentials")
	token := fs.String("token", "", "Xplora bearer token (required)")
	uid := fs.String("uid", "", "Xplora parent user ID (required)")
	clientID := fs.String("client-id", "fcm-probe-client-001", "Client ID for setFCMToken")
	_ = fs.Parse(args)

	if *token == "" {
		fmt.Fprintf(os.Stderr, "Error: --token is required\n")
		os.Exit(1)
	}
	if *uid == "" {
		fmt.Fprintf(os.Stderr, "Error: --uid is required\n")
		os.Exit(1)
	}

	if err := os.MkdirAll(*dir, 0o700); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating session dir: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	fmt.Println("Registering with GCM...")
	fcmClient := fcm.NewClient(*dir)
	fcmToken, err := fcmClient.Register(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FCM registration failed: %v\n\nNote: Ensure XploraSenderID, XploraAPKCertSHA1,\nand xploraAppPackage are filled in internal/fcm/constants.go\n", err)
		os.Exit(1)
	}
	fmt.Printf("FCM token: %s\n", fcmToken)

	fmt.Println("Registering FCM token with Xplora backend...")
	auth := xplora.NewAuth(*dir)
	_ = auth.SetCredentials(&xplora.Credentials{
		Token:  *token,
		UserID: *uid,
	})
	gqlClient := xplora.NewClient(auth)

	if err := gqlClient.SetFCMToken(ctx, *clientID, fcmToken); err != nil {
		fmt.Fprintf(os.Stderr, "setFCMToken failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("setFCMToken succeeded. Run 'fcm-probe listen' and send a message from a child watch.")
}

func cmdListen(args []string) {
	fs := flag.NewFlagSet("listen", flag.ExitOnError)
	dir := fs.String("dir", "fcm-probe-session", "Session directory for credentials")
	_ = fs.Parse(args)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	fcmClient := fcm.NewClient(*dir)
	fcmClient.OnConnected(func() {
		fmt.Println("[connected to MCS — waiting for pushes, send a message from a watch now]")
	})
	fcmClient.OnDisconnected(func() {
		fmt.Println("[MCS disconnected]")
	})
	fcmClient.OnMessage(func(msg fcm.NewMessage) {
		ts := time.Now().Format(time.RFC3339)
		formatted, _ := json.MarshalIndent(json.RawMessage(msg.Raw), "", "  ")
		fmt.Printf("[%s] FCM NewMessage payload:\n%s\n\n", ts, string(formatted))
	})

	fmt.Println("Connecting to MCS...")
	for ctx.Err() == nil {
		if err := fcmClient.Listen(ctx); err != nil && ctx.Err() == nil {
			fmt.Fprintf(os.Stderr, "MCS error: %v — retrying in 5s\n", err)
			time.Sleep(5 * time.Second)
		}
	}
}
