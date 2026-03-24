package xplora

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
)

// SignIn authenticates with phone+password.
// Populates auth credentials on success; this is the only method that can
// be called when auth.Token() is empty.
func (c *Client) SignIn(ctx context.Context, countryCode, phone, password string) (*AuthResponse, error) {
	passwordMD5 := fmt.Sprintf("%x", md5.Sum([]byte(password)))
	vars := map[string]any{
		"countryPhoneNumber": countryCode,
		"phoneNumber":        phone,
		"password":           passwordMD5,
		"emailAddress":       nil,
		"client":             "APP",
		"userLang":           "en",
		"timeZone":           "UTC",
	}
	data, err := c.do(ctx, MutationSignIn, vars)
	if err != nil {
		return nil, err
	}

	var result struct {
		SignInWithEmailOrPhone AuthResponse `json:"signInWithEmailOrPhone"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parsing signIn response: %w", err)
	}
	return &result.SignInWithEmailOrPhone, nil
}

// SetFCMToken registers an FCM token with the Xplora backend.
// clientID is a UUID generated once per login and reused across reconnects.
func (c *Client) SetFCMToken(ctx context.Context, uid, clientID, fcmToken string) error {
	vars := map[string]any{
		"uid":         uid,
		"key":         fcmToken,
		"clientId":    clientID,
		"deviceName":  "mautrix-xplora",
		"deviceOs":    "Android",
		"deviceOsVer": "13",
		"deviceBrand": "Google",
		"deviceModel": "Pixel 7",
		"type":        1, // Android push type
	}
	_, err := c.do(ctx, MutationSetFCMToken, vars)
	return err
}

// GetMyInfo returns the logged-in parent's account info.
func (c *Client) GetMyInfo(ctx context.Context) (*UserInfo, error) {
	data, err := c.do(ctx, QueryReadMyInfo, nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		ReadMyInfo UserInfo `json:"readMyInfo"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parsing readMyInfo response: %w", err)
	}
	return &result.ReadMyInfo, nil
}

// GetWatches returns the list of children's watches linked to this account.
func (c *Client) GetWatches(ctx context.Context, uid string) ([]WatchInfo, error) {
	vars := map[string]any{"uid": uid}
	data, err := c.do(ctx, QueryWatches, vars)
	if err != nil {
		return nil, err
	}

	var result struct {
		Watches []WatchInfo `json:"watches"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parsing watches response: %w", err)
	}
	return result.Watches, nil
}

// GetChats fetches paginated chat messages for a given watch UID.
// Returns messages in the order the API returns them (typically newest first).
func (c *Client) GetChats(ctx context.Context, wuid string, offset, limit int) ([]ChatMessage, error) {
	vars := map[string]any{
		"uid":    wuid,
		"offset": offset,
		"limit":  limit,
	}
	data, err := c.do(ctx, QueryChats, vars)
	if err != nil {
		return nil, err
	}

	var result struct {
		ChatsNew ChatsResponse `json:"chatsNew"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parsing chatsNew response: %w", err)
	}
	return result.ChatsNew.List, nil
}

// SendChatText sends a text message to a watch.
func (c *Client) SendChatText(ctx context.Context, wuid, text string) error {
	vars := map[string]any{
		"uid":  wuid,
		"text": text,
	}
	_, err := c.do(ctx, MutationSendChatText, vars)
	return err
}

// SetReadChatMsg marks a specific message as read.
func (c *Client) SetReadChatMsg(ctx context.Context, wuid, msgID string) error {
	vars := map[string]any{
		"uid":   wuid,
		"msgId": msgID,
	}
	_, err := c.do(ctx, MutationSetReadChatMsg, vars)
	return err
}
