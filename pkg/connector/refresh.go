package connector

import (
	"context"
	"fmt"
	"time"

	"github.com/palchrb/matrix-xplora/internal/xplora"
)

// Token renewal.
//
// The refreshToken mutation has never been captured from the official app
// (pyxplora_api exposes it but never calls it). What is known (2026-11-26):
//
//   - signIn issues an access token valid for 35 days.
//   - refreshToken works with the Open app key and the parent user id. The
//     w360 and token headers are rejected (E000004); other uids are not
//     found (E000021).
//   - A refresh answers valid=false but carries a NEW access token and a NEW
//     refresh token. The new access token is valid for only 15 minutes. The
//     old access token and refresh token stop working.
//   - Refresh still works after the access token has expired (tested 4.5
//     minutes after). After 3 hours a refresh token was gone; the limit in
//     between is unknown.
//
// So the bridge renews shortly before each expiry (renewBefore), on startup
// if the token is expired or nearly so, and whenever a call is rejected with
// E000004. This repeats every ~10 minutes once the first 35-day token has
// been renewed.
const (
	// renewBefore is how long before expiry the bridge renews the token.
	renewBefore = 5 * time.Minute
	// renewCheckInterval is how often the expiry is checked (locally).
	renewCheckInterval = time.Minute
	// refreshFailCooldown suppresses repeated attempts right after a failure,
	// so a burst of E000004 errors does not trigger a burst of refreshes.
	refreshFailCooldown = time.Minute
)

// refreshAttempt is one header/uid combination for the refreshToken mutation.
type refreshAttempt struct {
	variant xplora.AuthVariant
	uidKind string
	uid     string
}

func (a refreshAttempt) mode() string { return string(a.variant) + "/" + a.uidKind }

// refreshAttempts lists the combinations to try, the last successful one first.
func (c *XploraClient) refreshAttempts(creds *xplora.Credentials) []refreshAttempt {
	type uidCandidate struct{ kind, uid string }
	var uids []uidCandidate
	seen := map[string]bool{}
	add := func(kind, uid string) {
		if uid != "" && !seen[uid] {
			seen[uid] = true
			uids = append(uids, uidCandidate{kind, uid})
		}
	}
	add("userId", creds.UserID)
	add("sessionId", creds.SessionID)
	c.childrenMu.RLock()
	for _, w := range c.meta.Children {
		add("child", w.ChildUID()) // what pyxplora_api passes
	}
	c.childrenMu.RUnlock()

	var out []refreshAttempt
	// Open first: with the app key the server accepts the call and finds the
	// refresh token (it answered valid=false a minute after login), while the
	// w360 and token headers are rejected with E000004.
	for _, v := range []xplora.AuthVariant{xplora.AuthOpen, xplora.AuthW360, xplora.AuthToken} {
		if _, ok := c.gql.AuthHeader(v); !ok {
			continue
		}
		for _, u := range uids {
			out = append(out, refreshAttempt{variant: v, uidKind: u.kind, uid: u.uid})
		}
	}
	if creds.RefreshMode != "" {
		for i, a := range out {
			if a.mode() == creds.RefreshMode {
				rest := append(out[:i:i], out[i+1:]...)
				out = append([]refreshAttempt{a}, rest...)
				break
			}
		}
	}
	return out
}

// tryRefreshToken renews the access token, trying each header/uid combination
// until one works, and saves the new credentials.
func (c *XploraClient) tryRefreshToken(ctx context.Context) error {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()
	if !c.lastRefreshFail.IsZero() && time.Since(c.lastRefreshFail) < refreshFailCooldown {
		return fmt.Errorf("token refresh failed %s ago; not retrying yet", time.Since(c.lastRefreshFail).Round(time.Second))
	}
	err := c.refreshLocked(ctx)
	if err != nil {
		c.lastRefreshFail = time.Now()
	} else {
		c.lastRefreshFail = time.Time{}
	}
	return err
}

func (c *XploraClient) refreshLocked(ctx context.Context) error {
	old := c.auth.Creds()
	if old == nil || old.RefreshToken == "" {
		return fmt.Errorf("no refresh token available")
	}
	attempts := c.refreshAttempts(old)
	if len(attempts) == 0 {
		return fmt.Errorf("no refresh variants available")
	}
	var lastErr error
	tried := make([]string, 0, len(attempts))
	for _, a := range attempts {
		tried = append(tried, a.mode())
		resp, err := c.gql.RefreshToken(ctx, a.uid, old.RefreshToken, a.variant)
		if err == nil && (!resp.Valid || resp.Token == "") {
			c.logUnusableRefresh(a, old, resp)
			// valid=false with a new token may still be a rotation: the old
			// token is then dead anyway, so try the new one (verified below,
			// with the old credentials restored if it is rejected).
			if resp.Token == "" || resp.Token == old.Token {
				// The server processed the call, which consumes the refresh
				// token, so no other variant can succeed now.
				c.log.Warn().Str("mode", a.mode()).Strs("tried", tried).
					Msg("Token refresh answered without a usable token; refresh token likely consumed")
				return fmt.Errorf("server answered %s without a usable token (valid=%t); the refresh token is likely used up",
					a.mode(), resp.Valid)
			}
		}
		if err != nil {
			c.log.Debug().Err(err).Str("mode", a.mode()).Msg("Token refresh variant failed")
			lastErr = err
			continue
		}

		next := *old
		next.Token = resp.Token
		if resp.RefreshToken != "" { // keep the old one if the server omits it
			next.RefreshToken = resp.RefreshToken
		}
		if resp.ExpireDate != "" {
			next.ExpireDate = string(resp.ExpireDate)
		}
		next.RefreshMode = a.mode()
		if err := c.auth.SetCredentials(&next); err != nil {
			return fmt.Errorf("saving refreshed credentials: %w", err)
		}
		if err := c.verifyAfterRefresh(ctx, &next); err != nil {
			c.log.Error().Err(err).Str("mode", a.mode()).Msg("Refreshed token was rejected; restoring previous credentials")
			_ = c.auth.SetCredentials(old)
			return fmt.Errorf("refreshed token rejected: %w", err)
		}
		exp, _ := xplora.ParseExpireDate(next.ExpireDate)
		c.log.Info().Str("mode", a.mode()).Time("expires", exp).Msg("Access token refreshed successfully")
		return nil
	}
	c.log.Warn().Strs("tried", tried).Err(lastErr).Msg("All token refresh variants failed")
	return fmt.Errorf("all %d refresh variants failed, last error: %w", len(attempts), lastErr)
}

// logUnusableRefresh logs what a refresh response without a usable token
// contained, without logging any secret: tokens are only compared to the
// current ones.
func (c *XploraClient) logUnusableRefresh(a refreshAttempt, old *xplora.Credentials, resp *xplora.RefreshTokenResponse) {
	evt := c.log.Info()
	if resp.Token != "" && resp.Token != old.Token {
		// The normal renewal reply: valid=false but with new tokens.
		evt = c.log.Debug()
	}
	evt = evt.
		Str("mode", a.mode()).
		Bool("valid", resp.Valid).
		Str("token", compareSecret(resp.Token, old.Token)).
		Str("refresh_token", compareSecret(resp.RefreshToken, old.RefreshToken)).
		Str("id", idKind(resp.ID, old)).
		Str("expire_date", string(resp.ExpireDate))
	if exp, ok := xplora.ParseExpireDate(string(resp.ExpireDate)); ok {
		evt = evt.Time("expires", exp)
	}
	if cur, ok := xplora.ParseExpireDate(old.ExpireDate); ok {
		evt = evt.Time("current_expires", cur)
	}
	evt.Msg("Token refresh returned no usable token")
}

// compareSecret describes got relative to current without revealing either.
func compareSecret(got, current string) string {
	switch {
	case got == "":
		return "empty"
	case got == current:
		return "same"
	default:
		return "new"
	}
}

// idKind says which known ID a response id matches.
func idKind(id string, creds *xplora.Credentials) string {
	switch {
	case id == "":
		return "empty"
	case id == creds.UserID:
		return "userId"
	case id == creds.SessionID:
		return "sessionId"
	default:
		return "other"
	}
}

// verifyAfterRefresh checks that normal API calls work with the new token. If
// they fail while w360 tokens are in use, the w360 pair may belong to the old
// token, so it is dropped and the plain token is tried.
func (c *XploraClient) verifyAfterRefresh(ctx context.Context, creds *xplora.Credentials) error {
	_, err := c.gql.GetMyInfo(ctx)
	if err == nil {
		return nil
	}
	if _, ok := c.gql.AuthHeader(xplora.AuthW360); !ok {
		return err
	}
	c.gql.ClearW360()
	cleared := *creds
	cleared.W360Token, cleared.W360Secret = "", ""
	if serr := c.auth.SetCredentials(&cleared); serr != nil {
		return serr
	}
	if _, err2 := c.gql.GetMyInfo(ctx); err2 != nil {
		return err2
	}
	c.log.Info().Msg("Dropped w360 tokens after refresh; the plain token works")
	return nil
}

// maybeRenew renews the token when it expires within renewBefore (or has
// already expired). The expiry check is local; the API is only called when a
// renewal is due. Failures are retried on the next check after the cooldown.
func (c *XploraClient) maybeRenew(ctx context.Context) {
	exp, ok := c.auth.ExpiresAt()
	if !ok || time.Until(exp) > renewBefore {
		return
	}
	c.log.Debug().Time("expires", exp).Msg("Token expires soon, renewing")
	if err := c.tryRefreshToken(ctx); err != nil {
		c.log.Warn().Err(err).Time("expires", exp).Msg("Scheduled token renewal failed")
	}
}
