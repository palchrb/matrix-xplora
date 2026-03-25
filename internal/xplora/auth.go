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
	ExpireDate   string `json:"expireDate"` // ISO8601, e.g. "2026-03-25T10:00:00Z"
	UserID       string `json:"userId"`
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

// NeedsRefresh returns true if the token is expired or will expire within 1 hour.
// Returns false if the expiry date cannot be parsed (assume still valid).
// expireDate may arrive as RFC3339 ("2026-03-25T10:00:00Z"), Unix-seconds
// ("1777450741"), or Unix-milliseconds ("1777450741000") — all are handled.
// Unix-seconds vs milliseconds is distinguished by magnitude: values < 1e11
// are seconds, values >= 1e11 are milliseconds.
func (a *Auth) NeedsRefresh() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.creds == nil || a.creds.ExpireDate == "" {
		return false
	}
	expiry, err := time.Parse(time.RFC3339, a.creds.ExpireDate)
	if err != nil {
		var n int64
		if _, scanErr := fmt.Sscanf(a.creds.ExpireDate, "%d", &n); scanErr == nil && n > 0 {
			if n < 1e11 {
				expiry = time.Unix(n, 0) // seconds
			} else {
				expiry = time.UnixMilli(n) // milliseconds
			}
		} else {
			return false
		}
	}
	return time.Until(expiry) < time.Hour
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
