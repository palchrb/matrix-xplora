// Package fcm provides Android-native FCM (Firebase Cloud Messaging) push
// notification support for the Xplora bridge.
//
// It implements GCM device registration, FCM token acquisition, and an MCS
// (Mobile Connection Server) client for receiving real-time push notifications
// from Xplora's backend via Google's FCM infrastructure.
//
// Usage:
//
//	client := fcm.NewClient(sessionDir)
//	client.OnRawMessage(func(msg fcm.RawFCMMessage) { ... })
//	token, err := client.Register(ctx)
//	err = client.Listen(ctx)
package fcm
