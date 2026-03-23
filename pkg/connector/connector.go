// Package connector implements the mautrix bridgev2 network connector for
// Xplora children's smartwatches, using the Xplora GraphQL API and
// Android-native FCM push notifications.
package connector

import (
	"context"
	_ "embed"
	"fmt"
	"path/filepath"

	"github.com/palchrb/matrix-xplora/internal/xplora"
	"go.mau.fi/util/configupgrade"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

// XploraConnector is the top-level NetworkConnector for the bridge.
// One instance is shared across the whole bridge process.
var _ bridgev2.NetworkConnector = (*XploraConnector)(nil)

// XploraConnector implements bridgev2.NetworkConnector.
type XploraConnector struct {
	br     *bridgev2.Bridge
	Config Config
}

// Config holds network-specific config fields (in the network: section).
type Config struct {
	// SessionsDir is the base directory where per-user Xplora sessions are
	// stored. Each login gets a subdirectory named after its login ID.
	// Defaults to "sessions".
	SessionsDir string `yaml:"sessions_dir"`
}

//go:embed example-config.yaml
var ExampleConfig string

func upgradeConfig(helper configupgrade.Helper) {
	helper.Copy(configupgrade.Str, "sessions_dir")
}

// sessionDir returns the directory for a specific login's credentials.
func (xc *XploraConnector) sessionDir(loginID networkid.UserLoginID) string {
	base := xc.Config.SessionsDir
	if base == "" {
		base = "sessions"
	}
	return filepath.Join(base, string(loginID))
}

func (xc *XploraConnector) Init(br *bridgev2.Bridge) {
	xc.br = br
}

func (xc *XploraConnector) Start(_ context.Context) error {
	return nil
}

func (xc *XploraConnector) GetCapabilities() *bridgev2.NetworkGeneralCapabilities {
	return &bridgev2.NetworkGeneralCapabilities{}
}

func (xc *XploraConnector) GetBridgeInfoVersion() (info, capabilities int) {
	return 1, 1
}

func (xc *XploraConnector) GetName() bridgev2.BridgeName {
	return bridgev2.BridgeName{
		DisplayName:      "Xplora",
		NetworkURL:       "https://www.xplora.com/",
		NetworkID:        "xplora",
		BeeperBridgeType: "github.com/palchrb/matrix-xplora",
		DefaultPort:      29341,
	}
}

func (xc *XploraConnector) GetConfig() (example string, data any, upgrader configupgrade.Upgrader) {
	return ExampleConfig, &xc.Config, configupgrade.SimpleUpgrader(upgradeConfig)
}

func (xc *XploraConnector) GetDBMetaTypes() database.MetaTypes {
	return database.MetaTypes{
		UserLogin: func() any { return &UserLoginMetadata{} },
		Portal:    func() any { return &PortalMetadata{} },
		Ghost:     nil,
		Message:   nil,
		Reaction:  nil,
	}
}

// LoadUserLogin prepares an existing login for connection.
func (xc *XploraConnector) LoadUserLogin(ctx context.Context, login *bridgev2.UserLogin) error {
	meta := login.Metadata.(*UserLoginMetadata)
	sessDir := xc.sessionDir(login.ID)

	auth := xplora.NewAuth(sessDir)
	if err := auth.Load(); err != nil {
		login.Log.Warn().Err(err).Msg("Could not load Xplora credentials, will reconnect on next Connect()")
	}

	gqlClient := xplora.NewClient(auth)
	login.Client = newXploraClient(xc, login, auth, gqlClient, meta)
	return nil
}

func (xc *XploraConnector) GetLoginFlows() []bridgev2.LoginFlow {
	return []bridgev2.LoginFlow{{
		Name:        "Password",
		Description: "Log in with your Xplora parent account phone number and password",
		ID:          "password",
	}}
}

func (xc *XploraConnector) CreateLogin(_ context.Context, user *bridgev2.User, flowID string) (bridgev2.LoginProcess, error) {
	if flowID != "password" {
		return nil, fmt.Errorf("unknown login flow ID: %q", flowID)
	}
	return &XploraLogin{connector: xc, user: user}, nil
}

// --- DB metadata types ---

// UserLoginMetadata stores per-login data for the bridge database.
type UserLoginMetadata struct {
	PhoneNumber string `json:"phoneNumber"`
	CountryCode string `json:"countryCode"`
	UserID      string `json:"userId"`
	// ClientID is a UUID generated once at first login and reused for FCM registration.
	ClientID string `json:"clientId"`
	// FCMToken is the last registered FCM push token.
	FCMToken string `json:"fcmToken"`
}

// PortalMetadata stores Xplora-specific data for a bridged Matrix room.
// One portal per child watch.
type PortalMetadata struct {
	WUID      string `json:"wuid"`       // watch UID (child's Xplora user ID)
	ChildName string `json:"childName"`  // display name from watches query
	LastMsgID string `json:"lastMsgId"`  // last seen msgId for polling deduplication
}

// --- Identifier helpers ---

// portalIDFromWUID creates a stable PortalID from a watch UID.
func portalIDFromWUID(wuid string) networkid.PortalID {
	return networkid.PortalID(wuid)
}

// ghostIDFromWUID uses the watch UID directly as the ghost user ID.
func ghostIDFromWUID(wuid string) networkid.UserID {
	return networkid.UserID(wuid)
}

// loginIDFromPhone uses E.164 format as the login ID.
func loginIDFromPhone(countryCode, phone string) networkid.UserLoginID {
	return networkid.UserLoginID("+" + countryCode + phone)
}
