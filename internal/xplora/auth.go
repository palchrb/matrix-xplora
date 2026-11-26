package xplora

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Credentials is the on-disk credential store for a single Xplora login.
// Saved to <sessionDir>/xplora_credentials.json.
type Credentials struct {
	Token        string `json:"token"`
	RefreshToken string `json:"refreshToken"`
	ExpireDate   string `json:"expireDate"` // RFC3339, Unix seconds or Unix milliseconds
	UserID       string `json:"userId"`

	// W360Token/W360Secret are returned by signIn and used by the official app
	// in the H-BackDoor-Authorization header. Persisted so they survive restarts.
	W360Token  string `json:"w360Token,omitempty"`
	W360Secret string `json:"w360Secret,omitempty"`
	// SessionID is the top-level "id" of the signIn response. It is one of the
	// uid candidates tried for the refreshToken mutation.
	SessionID string `json:"sessionId,omitempty"`
	// RefreshMode records which header/uid combination last refreshed the token
	// successfully, so it is tried first next time.
	RefreshMode string `json:"refreshMode,omitempty"`
}

// Auth manages loading, saving, and accessing Xplora credentials on disk.
type Auth struct {
	sessionDir string
	creds      *Credentials
	mu         sync.RWMutex
}

// NewAuth creates an Auth for the given session directory.
// Call Load() to read existing credentials from disk.
func NewAuth(sessionDir string) *Auth {
	return &Auth{sessionDir: sessionDir}
}

// Load reads credentials from disk.
// Returns os.ErrNotExist if not yet saved.
func (a *Auth) Load() error {
	data, err := os.ReadFile(a.credentialsPath())
	if err != nil {
		return err
	}
	var creds Credentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return fmt.Errorf("parsing Xplora credentials: %w", err)
	}
	a.mu.Lock()
	a.creds = &creds
	a.mu.Unlock()
	return nil
}

// Save writes credentials to disk atomically.
func (a *Auth) Save() error {
	a.mu.RLock()
	creds := a.creds
	a.mu.RUnlock()
	if creds == nil {
		return fmt.Errorf("no credentials to save")
	}
	return a.writeToDisk(creds)
}

// Token returns the current bearer token (or empty string if not set).
func (a *Auth) Token() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.creds == nil {
		return ""
	}
	return a.creds.Token
}

// UserID returns the parent account user ID (or empty string if not set).
func (a *Auth) UserID() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.creds == nil {
		return ""
	}
	return a.creds.UserID
}

// RefreshToken returns the stored refresh token (or empty string if not set).
func (a *Auth) RefreshToken() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.creds == nil {
		return ""
	}
	return a.creds.RefreshToken
}

// Creds returns a copy of the current credentials, or nil if none are loaded.
func (a *Auth) Creds() *Credentials {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.creds == nil {
		return nil
	}
	c := *a.creds
	return &c
}

// W360 returns the persisted w360 token and secret (empty if not set).
func (a *Auth) W360() (token, secret string) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.creds == nil {
		return "", ""
	}
	return a.creds.W360Token, a.creds.W360Secret
}

// ExpiresAt returns the token expiry time, or false if unknown or unparseable.
// expireDate may arrive as RFC3339 ("2026-03-25T10:00:00Z"), Unix-seconds
// ("1777450741"), or Unix-milliseconds ("1777450741000") — all are handled.
// Unix-seconds vs milliseconds is distinguished by magnitude: values < 1e11
// are seconds, values >= 1e11 are milliseconds.
func (a *Auth) ExpiresAt() (time.Time, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.creds == nil {
		return time.Time{}, false
	}
	return ParseExpireDate(a.creds.ExpireDate)
}

// ParseExpireDate parses an Xplora expireDate value (see ExpiresAt).
func ParseExpireDate(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, true
	}
	var n int64
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil || n <= 0 {
		return time.Time{}, false
	}
	if n < 1e11 {
		return time.Unix(n, 0), true
	}
	return time.UnixMilli(n), true
}

// NeedsRefreshWithin returns true if the token expires within d.
// Returns false if the expiry date is unknown (assume still valid).
func (a *Auth) NeedsRefreshWithin(d time.Duration) bool {
	expiry, ok := a.ExpiresAt()
	return ok && time.Until(expiry) < d
}

// SetCredentials stores new credentials in memory and saves them to disk.
func (a *Auth) SetCredentials(creds *Credentials) error {
	a.mu.Lock()
	a.creds = creds
	a.mu.Unlock()
	return a.writeToDisk(creds)
}

// credentialsPath returns the path to the credentials file.
func (a *Auth) credentialsPath() string {
	return filepath.Join(a.sessionDir, "xplora_credentials.json")
}

// writeToDisk atomically writes credentials to disk (write to tmp, then rename).
func (a *Auth) writeToDisk(creds *Credentials) error {
	if err := os.MkdirAll(a.sessionDir, 0o700); err != nil {
		return fmt.Errorf("creating session dir: %w", err)
	}
	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return fmt.Errorf("serializing Xplora credentials: %w", err)
	}
	tmpPath := a.credentialsPath() + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return fmt.Errorf("writing Xplora credentials tmp: %w", err)
	}
	if err := os.Rename(tmpPath, a.credentialsPath()); err != nil {
		return fmt.Errorf("renaming Xplora credentials: %w", err)
	}
	return nil
}
