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
	W360         *W360    `json:"w360"`
}

// UserRef is a brief user reference embedded in AuthResponse and ChatMessage.
type UserRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// W360 holds an alternative token/secret pair returned by signIn.
// When present, these are used instead of the main token for H-BackDoor-Authorization.
type W360 struct {
	Token  string `json:"token"`
	Secret string `json:"secret"`
}

// RefreshTokenResponse is returned by the refreshToken mutation.
type RefreshTokenResponse struct {
	ID           string `json:"id"`
	Token        string `json:"token"`
	RefreshToken string `json:"refreshToken"`
	ExpireDate   string `json:"expireDate"`
	Valid         bool   `json:"valid"`
}

// WatchInfo represents a child's smartwatch linked to the parent account.
// ID is the watch device ID. User.ID is the child's user ID used for chatsNew.
type WatchInfo struct {
	ID   string   `json:"id"`
	Name *string  `json:"name"`
	User *UserRef `json:"user"`
}

// ChildUID returns the child's user ID for use in chatsNew queries.
// Falls back to the watch device ID if user is missing.
func (w WatchInfo) ChildUID() string {
	if w.User != nil && w.User.ID != "" {
		return w.User.ID
	}
	return w.ID
}

// ChildName returns the best available display name for the child.
func (w WatchInfo) ChildName() string {
	if w.User != nil && w.User.Name != "" {
		return w.User.Name
	}
	if w.Name != nil && *w.Name != "" {
		return *w.Name
	}
	return w.ChildUID()
}

// UserInfo is returned by the readMyInfo query.
type UserInfo struct {
	ID   string  `json:"id"`
	Name *string `json:"name"`
}

// ChatMessage is a single message from the chatsNew query.
// data is a JSON blob with type-specific content (e.g. the text for TEXT messages).
// create is the server-side Unix timestamp in milliseconds.
// sender.id identifies the sender; compare to the parent's user ID to set isFromMe.
type ChatMessage struct {
	ID     string   `json:"id"`
	MsgID  string   `json:"msgId"`
	Type   *string  `json:"type"`
	Sender *UserRef `json:"sender"`
	Data   *string  `json:"data"`
	Create *int64   `json:"create"`
}

// ChatsResponse wraps the paginated chatsNew response.
type ChatsResponse struct {
	Offset int           `json:"offset"`
	Limit  int           `json:"limit"`
	List   []ChatMessage `json:"list"`
}
