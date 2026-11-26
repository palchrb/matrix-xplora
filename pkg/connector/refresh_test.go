package connector

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/palchrb/matrix-xplora/internal/xplora"
	"github.com/rs/zerolog"
)

// fakeXplora answers refreshToken and readMyInfo. accept decides whether a
// refresh with the given header and uid succeeds.
type fakeXplora struct {
	mu     sync.Mutex
	calls  []string // "op header uid"
	accept func(header, uid string) bool
	// notYet, if set, makes matching refreshes answer valid=false with the
	// current token, as the server did for Open + user id right after login.
	notYet func(header, uid string) bool
	// rotate, if set, makes matching refreshes answer valid=false with a NEW
	// token, i.e. a rotation the server does not flag as valid.
	rotate   func(header, uid string) bool
	newTok   string
	myInfoOK func(header string) bool
}

func (f *fakeXplora) RoundTrip(req *http.Request) (*http.Response, error) {
	var body struct {
		OperationName string         `json:"operationName"`
		Variables     map[string]any `json:"variables"`
	}
	raw, _ := io.ReadAll(req.Body)
	_ = json.Unmarshal(raw, &body)
	header := req.Header.Get("H-BackDoor-Authorization")
	uid, _ := body.Variables["uid"].(string)
	f.mu.Lock()
	f.calls = append(f.calls, body.OperationName+" "+header+" "+uid)
	f.mu.Unlock()

	var out string
	switch body.OperationName {
	case "RefreshToken":
		if f.rotate != nil && f.rotate(header, uid) {
			out = `{"data":{"refreshToken":{"id":"parent","token":"rotated","refreshToken":"rt2","expireDate":"1900000000","valid":false}}}`
		} else if f.notYet != nil && f.notYet(header, uid) {
			out = `{"data":{"refreshToken":{"id":"parent","token":"oldtok","refreshToken":"rt","expireDate":"1800000000","valid":false}}}`
		} else if f.accept(header, uid) {
			// refreshToken deliberately empty: the old one must be kept.
			out = `{"data":{"refreshToken":{"id":"x","token":"` + f.newTok + `","refreshToken":"","expireDate":"1900000000","valid":true}}}`
		} else {
			out = `{"errors":[{"code":"E000004","message":"Authentication failed."}],"data":{"refreshToken":null}}`
		}
	case "ReadMyInfo":
		if f.myInfoOK == nil || f.myInfoOK(header) {
			out = `{"data":{"readMyInfo":{"id":"parent","children":[]}}}`
		} else {
			out = `{"errors":[{"code":"E000004","message":"Authentication failed."}],"data":{"readMyInfo":null}}`
		}
	default:
		out = `{"data":{}}`
	}
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewBufferString(out))}, nil
}

func newTestClient(t *testing.T, f *fakeXplora, creds *xplora.Credentials) *XploraClient {
	t.Helper()
	auth := xplora.NewAuth(t.TempDir())
	if err := auth.SetCredentials(creds); err != nil {
		t.Fatal(err)
	}
	gql := xplora.NewClient(auth, xplora.WithHTTPClient(&http.Client{Transport: f}))
	return &XploraClient{
		auth: auth,
		gql:  gql,
		log:  zerolog.Nop(),
		meta: &UserLoginMetadata{Children: []xplora.WatchInfo{{ID: "watch1", User: &xplora.UserRef{ID: "child1"}}}},
	}
}

func baseCreds() *xplora.Credentials {
	return &xplora.Credentials{
		Token: "oldtok", RefreshToken: "rt", ExpireDate: "1800000000",
		UserID: "parent", SessionID: "sess", W360Token: "w3t", W360Secret: "w3s",
	}
}

func TestRefreshTriesVariantsAndKeepsRefreshToken(t *testing.T) {
	f := &fakeXplora{newTok: "newtok", accept: func(h, uid string) bool {
		return strings.HasPrefix(h, "Bearer oldtok:") && uid == "child1"
	}}
	c := newTestClient(t, f, baseCreds())

	if err := c.tryRefreshToken(t.Context()); err != nil {
		t.Fatalf("refresh failed: %v", err)
	}
	got := c.auth.Creds()
	if got.Token != "newtok" || got.RefreshToken != "rt" || got.ExpireDate != "1900000000" {
		t.Fatalf("unexpected creds after refresh: %+v", got)
	}
	if got.RefreshMode != "token/child" {
		t.Fatalf("RefreshMode = %q, want token/child", got.RefreshMode)
	}
	// Open (3 uids) and w360 (3 uids) are tried before token/userId, token/sessionId, token/child.
	refreshCalls := 0
	for _, call := range f.calls {
		if strings.HasPrefix(call, "RefreshToken ") {
			refreshCalls++
		}
	}
	if refreshCalls != 9 {
		t.Fatalf("refresh calls = %d, want 9: %v", refreshCalls, f.calls)
	}
	if !strings.HasPrefix(f.calls[0], "RefreshToken Open ") || !strings.HasSuffix(f.calls[0], " parent") {
		t.Fatalf("first attempt = %q, want open with parent uid", f.calls[0])
	}
}

func TestRefreshTriesRememberedModeFirst(t *testing.T) {
	f := &fakeXplora{newTok: "newtok", accept: func(h, uid string) bool {
		return strings.HasPrefix(h, "Open ") && uid == "sess"
	}}
	creds := baseCreds()
	creds.RefreshMode = "open/sessionId"
	c := newTestClient(t, f, creds)

	if err := c.tryRefreshToken(t.Context()); err != nil {
		t.Fatalf("refresh failed: %v", err)
	}
	if !strings.HasPrefix(f.calls[0], "RefreshToken Open ") || !strings.HasSuffix(f.calls[0], " sess") {
		t.Fatalf("first attempt = %q, want remembered open/sessionId", f.calls[0])
	}
}

func TestRefreshAllFailCooldown(t *testing.T) {
	f := &fakeXplora{accept: func(string, string) bool { return false }}
	c := newTestClient(t, f, baseCreds())

	if err := c.tryRefreshToken(t.Context()); err == nil {
		t.Fatal("expected failure")
	}
	n := len(f.calls)
	if n != 9 { // 3 headers x 3 uids
		t.Fatalf("calls = %d, want 9", n)
	}
	if got := c.auth.Creds(); got.Token != "oldtok" {
		t.Fatalf("token changed after failed refresh: %q", got.Token)
	}
	// A second attempt right away is suppressed by the cooldown.
	if err := c.tryRefreshToken(t.Context()); err == nil || len(f.calls) != n {
		t.Fatalf("expected cooldown to suppress retry; err=%v calls=%d", err, len(f.calls))
	}
}

func TestRefreshDropsW360WhenRejectedAfterRefresh(t *testing.T) {
	f := &fakeXplora{
		newTok: "newtok",
		accept: func(h, uid string) bool { return strings.HasPrefix(h, "Bearer w3t:") && uid == "parent" },
		// After refresh, the old w360 pair no longer works; the new token does.
		myInfoOK: func(h string) bool { return strings.HasPrefix(h, "Bearer newtok:") },
	}
	c := newTestClient(t, f, baseCreds())

	if err := c.tryRefreshToken(t.Context()); err != nil {
		t.Fatalf("refresh failed: %v", err)
	}
	got := c.auth.Creds()
	if got.Token != "newtok" || got.W360Token != "" {
		t.Fatalf("expected new token and dropped w360, got %+v", got)
	}
}

func TestHumanDuration(t *testing.T) {
	cases := map[time.Duration]string{
		20 * time.Minute:              "less than an hour",
		time.Hour:                     "1 hour",
		7 * time.Hour:                 "7 hours",
		48 * time.Hour:                "2 days",
		53 * time.Hour:                "2 days 5 hours",
		25*time.Hour + 10*time.Minute: "1 day 1 hour",
	}
	for d, want := range cases {
		if got := humanDuration(d); got != want {
			t.Errorf("humanDuration(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestParseExpireDate(t *testing.T) {
	want := time.Unix(1795677868, 0)
	for _, s := range []string{"1795677868", "1795677868000", want.UTC().Format(time.RFC3339)} {
		got, ok := xplora.ParseExpireDate(s)
		if !ok || !got.Equal(want) {
			t.Errorf("ParseExpireDate(%q) = %v, %v", s, got, ok)
		}
	}
	if _, ok := xplora.ParseExpireDate(""); ok {
		t.Error("empty expireDate should not parse")
	}
}

func TestRefreshNotYetIsLoggedWithoutSecrets(t *testing.T) {
	f := &fakeXplora{
		accept: func(string, string) bool { return false },
		notYet: func(h, uid string) bool { return strings.HasPrefix(h, "Open ") && uid == "parent" },
	}
	c := newTestClient(t, f, baseCreds())
	var logBuf bytes.Buffer
	c.log = zerolog.New(&logBuf)

	if err := c.tryRefreshToken(t.Context()); err == nil {
		t.Fatal("valid=false must not count as a successful refresh")
	}
	if got := c.auth.Creds(); got.Token != "oldtok" || got.RefreshMode != "" {
		t.Fatalf("credentials changed after valid=false: %+v", got)
	}
	out := logBuf.String()
	for _, want := range []string{
		`"message":"Token refresh returned no usable token"`,
		`"mode":"open/userId"`, `"valid":false`, `"token":"same"`,
		`"refresh_token":"same"`, `"id":"userId"`, `"expire_date":"1800000000"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("log missing %s\nlog: %s", want, out)
		}
	}
	for _, secret := range []string{"oldtok", `"rt"`, "w3t", "w3s"} {
		if strings.Contains(out, secret) {
			t.Errorf("log leaks secret %q: %s", secret, out)
		}
	}
}

func TestRefreshValidFalseWithNewTokenIsTriedAndKept(t *testing.T) {
	f := &fakeXplora{
		accept:   func(string, string) bool { return false },
		rotate:   func(h, uid string) bool { return strings.HasPrefix(h, "Open ") && uid == "parent" },
		myInfoOK: func(h string) bool { return strings.HasPrefix(h, "Bearer rotated:") },
	}
	creds := baseCreds()
	creds.W360Token, creds.W360Secret = "", ""
	c := newTestClient(t, f, creds)

	if err := c.tryRefreshToken(t.Context()); err != nil {
		t.Fatalf("rotated token that works should be kept: %v", err)
	}
	got := c.auth.Creds()
	if got.Token != "rotated" || got.RefreshToken != "rt2" || got.RefreshMode != "open/userId" {
		t.Fatalf("unexpected creds: %+v", got)
	}
}

func TestRefreshValidFalseWithRejectedTokenRestoresOld(t *testing.T) {
	f := &fakeXplora{
		accept:   func(string, string) bool { return false },
		rotate:   func(h, uid string) bool { return strings.HasPrefix(h, "Open ") && uid == "parent" },
		myInfoOK: func(string) bool { return false },
	}
	creds := baseCreds()
	creds.W360Token, creds.W360Secret = "", ""
	c := newTestClient(t, f, creds)

	if err := c.tryRefreshToken(t.Context()); err == nil {
		t.Fatal("expected an error when the rotated token is rejected")
	}
	if got := c.auth.Creds(); got.Token != "oldtok" || got.RefreshToken != "rt" {
		t.Fatalf("old credentials not restored: %+v", got)
	}
}

func TestRefreshStopsAfterServerAnswersWithoutToken(t *testing.T) {
	f := &fakeXplora{
		accept: func(string, string) bool { return true }, // later variants would "work"
		notYet: func(h, uid string) bool { return strings.HasPrefix(h, "Open ") && uid == "parent" },
	}
	c := newTestClient(t, f, baseCreds())

	if err := c.tryRefreshToken(t.Context()); err == nil {
		t.Fatal("expected failure when the server answers without a usable token")
	}
	refreshCalls := 0
	for _, call := range f.calls {
		if strings.HasPrefix(call, "RefreshToken ") {
			refreshCalls++
		}
	}
	// open/userId is first and consumes the refresh token: nothing else is tried.
	if refreshCalls != 1 {
		t.Fatalf("refresh calls = %d, want 1: %v", refreshCalls, f.calls)
	}
}
