package xplora

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
)

// SignIn authenticates with phone+password and stores the resulting token.
// This is the only method callable when auth.Token() is empty.
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

	resp := &result.SignInWithEmailOrPhone

	// If the server returned w360 tokens, update the client to use them
	// for all subsequent H-BackDoor-Authorization headers.
	if resp.W360 != nil && resp.W360.Token != "" && resp.W360.Secret != "" {
		c.setW360(resp.W360.Token, resp.W360.Secret)
	}

	return resp, nil
}

// RefreshToken exchanges a refresh token for a new access token.
func (c *Client) RefreshToken(ctx context.Context, uid, refreshToken string) (*RefreshTokenResponse, error) {
	vars := map[string]any{
		"uid":          uid,
		"refreshToken": refreshToken,
	}
	data, err := c.do(ctx, MutationRefreshToken, vars)
	if err != nil {
		return nil, err
	}

	var result struct {
		RefreshToken RefreshTokenResponse `json:"refreshToken"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parsing refreshToken response: %w", err)
	}
	return &result.RefreshToken, nil
}

// SetFCMToken registers an FCM token with the Xplora backend.
// clientID is a UUID generated once per login and reused across reconnects.
func (c *Client) SetFCMToken(ctx context.Context, clientID, fcmToken string) error {
	vars := map[string]any{
		"clientId":     clientID,
		"fcmToken":     fcmToken,
		"manufacturer": "Google",
		"brand":        "Google",
		"model":        "Pixel 7",
		"osVer":        "13",
		"userLang":     "en",
		"timeZone":     "UTC",
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


// GetChats fetches paginated chat messages for a given watch's child user ID.
// msgID optionally filters to messages after a given message ID.
func (c *Client) GetChats(ctx context.Context, wuid string, offset, limit int, msgID string) ([]ChatMessage, error) {
	vars := map[string]any{
		"uid":    wuid,
		"offset": offset,
		"limit":  limit,
	}
	if msgID != "" {
		vars["msgId"] = msgID
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

// FetchChatImage returns the download URL for an image message.
func (c *Client) FetchChatImage(ctx context.Context, wuid, msgID string) (string, error) {
	vars := map[string]any{"uid": wuid, "msgId": msgID}
	data, err := c.do(ctx, QueryFetchChatImage, vars)
	if err != nil {
		return "", err
	}
	var result struct {
		FetchChatImage string `json:"fetchChatImage"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("parsing fetchChatImage response: %w", err)
	}
	return result.FetchChatImage, nil
}

// FetchChatVoice returns the download URL for a voice message.
func (c *Client) FetchChatVoice(ctx context.Context, wuid, msgID string) (string, error) {
	vars := map[string]any{"uid": wuid, "msgId": msgID}
	data, err := c.do(ctx, QueryFetchChatVoice, vars)
	if err != nil {
		return "", err
	}
	var result struct {
		FetchChatVoice string `json:"fetchChatVoice"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("parsing fetchChatVoice response: %w", err)
	}
	return result.FetchChatVoice, nil
}

// AskWatchLocate requests the watch to push a fresh GPS fix to the server.
// Returns an error if the API call fails, but the watch's response is asynchronous.
func (c *Client) AskWatchLocate(ctx context.Context, wuid string) error {
	vars := map[string]any{"uid": wuid}
	_, err := c.do(ctx, QueryAskWatchLocate, vars)
	return err
}

// GetWatchLastLocation returns the most recent known location of a watch.
func (c *Client) GetWatchLastLocation(ctx context.Context, wuid string) (*LocationInfo, error) {
	vars := map[string]any{"uid": wuid}
	data, err := c.do(ctx, QueryWatchLastLocate, vars)
	if err != nil {
		return nil, err
	}
	var result struct {
		WatchLastLocate LocationInfo `json:"watchLastLocate"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parsing watchLastLocate response: %w", err)
	}
	return &result.WatchLastLocate, nil
}
