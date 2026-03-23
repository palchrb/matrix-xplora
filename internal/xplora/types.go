// Package xplora provides a GraphQL client for the Xplora children's
// smartwatch API (api.myxplora.com). It handles authentication, chat
// messaging, and device/watch management.
package xplora

// AuthResponse is returned by the signInWithEmailOrPhone mutation.
type AuthResponse struct {
	ID           string   `json:"id"`
	Token        string   `json:"token"`
	RefreshToken string   `json:"refreshToken"`
	ExpireDate   string   `json:"expireDate"` // ISO8601 string
	User         *UserRef `json:"user"`
}

// UserRef is a brief user reference embedded in AuthResponse.
type UserRef struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

// WatchInfo represents a child's smartwatch linked to the parent account.
type WatchInfo struct {
	UID  string  `json:"uid"`
	Name *string `json:"name"`
}

// UserInfo is returned by the readMyInfo query.
type UserInfo struct {
	ID   string  `json:"id"`
	Name *string `json:"name"`
}

// ChatMessage is a single message from the chatsNew query.
type ChatMessage struct {
	MsgID  string  `json:"msgId"`
	Type   *string `json:"type"`
	Text   *string `json:"text"`
	// Tm is the message timestamp. Assumed to be Unix milliseconds
	// (common in JS-originated APIs); verify with fcm-probe if needed.
	Tm     *int64  `json:"tm"`
	Status *string `json:"status"`
}

// ChatsResponse wraps the paginated chatsNew response.
type ChatsResponse struct {
	Offset int           `json:"offset"`
	Limit  int           `json:"limit"`
	List   []ChatMessage `json:"list"`
}
