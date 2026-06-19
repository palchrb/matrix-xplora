package connector

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/palchrb/matrix-xplora/internal/xplora"
	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

// XploraLogin implements a two-step login flow:
//   Step 1: enter phone number or email address
//   Step 2: enter country code (phone only) + password
//
// The identifier from step 1 is stored in the struct so step 2 knows
// which path to take and which fields to ask for.
type XploraLogin struct {
	connector  *XploraConnector
	user       *bridgev2.User
	identifier string // set after step 1; either a phone number or email address
	isEmail    bool   // true when identifier contains '@'
}

var _ bridgev2.LoginProcessUserInput = (*XploraLogin)(nil)

// Start returns step 1: a single field for phone number or email address.
func (xl *XploraLogin) Start(_ context.Context) (*bridgev2.LoginStep, error) {
	return &bridgev2.LoginStep{
		Type:   bridgev2.LoginStepTypeUserInput,
		StepID: "com.xplora.identifier",
		UserInputParams: &bridgev2.LoginUserInputParams{
			Fields: []bridgev2.LoginInputDataField{
				{
					Type: bridgev2.LoginInputFieldTypeUsername,
					ID:   "identifier",
					Name: "Xplora parent account phone number (without country code) or email address",
				},
			},
		},
	}, nil
}

// SubmitUserInput handles both steps.
// Step 1 (identifier not yet set): store the identifier, return step 2.
// Step 2 (identifier already set): complete the login.
func (xl *XploraLogin) SubmitUserInput(ctx context.Context, input map[string]string) (*bridgev2.LoginStep, error) {
	if xl.identifier == "" {
		return xl.submitStep1(ctx, input)
	}
	return xl.submitStep2(ctx, input)
}

// submitStep1 validates the identifier and returns the appropriate step 2.
func (xl *XploraLogin) submitStep1(_ context.Context, input map[string]string) (*bridgev2.LoginStep, error) {
	id := strings.TrimSpace(input["identifier"])
	if id == "" {
		return nil, fmt.Errorf("phone number or email address is required")
	}
	xl.identifier = id
	xl.isEmail = strings.ContainsRune(id, '@')

	if xl.isEmail {
		return &bridgev2.LoginStep{
			Type:   bridgev2.LoginStepTypeUserInput,
			StepID: "com.xplora.credentials_email",
			UserInputParams: &bridgev2.LoginUserInputParams{
				Fields: []bridgev2.LoginInputDataField{
					{
						Type: bridgev2.LoginInputFieldTypePassword,
						ID:   "password",
						Name: "Xplora password",
					},
				},
			},
		}, nil
	}

	return &bridgev2.LoginStep{
		Type:   bridgev2.LoginStepTypeUserInput,
		StepID: "com.xplora.credentials_phone",
		UserInputParams: &bridgev2.LoginUserInputParams{
			Fields: []bridgev2.LoginInputDataField{
				{
					Type:    bridgev2.LoginInputFieldTypeUsername,
					ID:      "country_code",
					Name:    "country code (digits only, e.g. 47 for Norway)",
					Pattern: `^\d{1,4}$`,
				},
				{
					Type: bridgev2.LoginInputFieldTypePassword,
					ID:   "password",
					Name: "Xplora password",
				},
			},
		},
	}, nil
}

// submitStep2 completes the login using the identifier stored from step 1.
func (xl *XploraLogin) submitStep2(ctx context.Context, input map[string]string) (*bridgev2.LoginStep, error) {
	password := input["password"]
	if password == "" {
		return nil, fmt.Errorf("password is required")
	}

	if xl.isEmail {
		return xl.finishLogin(ctx,
			loginIDFromEmail(xl.identifier), xl.identifier,
			&UserLoginMetadata{Email: xl.identifier},
			func(gqlClient *xplora.Client, clientID string) (*xplora.AuthResponse, error) {
				zerolog.Ctx(ctx).Info().Str("email", xl.identifier).Msg("Attempting Xplora sign-in (email)")
				return gqlClient.SignIn(ctx, "", "", xl.identifier, password, clientID)
			},
		)
	}

	countryCode := strings.TrimSpace(input["country_code"])
	if countryCode == "" {
		return nil, fmt.Errorf("country code is required")
	}
	return xl.finishLogin(ctx,
		loginIDFromPhone(countryCode, xl.identifier), "+"+countryCode+xl.identifier,
		&UserLoginMetadata{PhoneNumber: xl.identifier, CountryCode: countryCode},
		func(gqlClient *xplora.Client, clientID string) (*xplora.AuthResponse, error) {
			zerolog.Ctx(ctx).Info().Str("country_code", countryCode).Str("phone", xl.identifier).Msg("Attempting Xplora sign-in (phone)")
			return gqlClient.SignIn(ctx, countryCode, xl.identifier, "", password, clientID)
		},
	)
}

// finishLogin is the shared tail: session dir, sign in, save credentials,
// fetch device list, persist login, start connection.
func (xl *XploraLogin) finishLogin(
	ctx context.Context,
	loginID networkid.UserLoginID,
	remoteName string,
	meta *UserLoginMetadata,
	doSignIn func(*xplora.Client, string) (*xplora.AuthResponse, error),
) (*bridgev2.LoginStep, error) {
	log := zerolog.Ctx(ctx)

	sessDir := xl.connector.sessionDir(loginID)
	if err := os.MkdirAll(sessDir, 0o700); err != nil {
		return nil, fmt.Errorf("creating session directory: %w", err)
	}

	clientID := loadOrCreateClientID(sessDir)
	auth := xplora.NewAuth(sessDir)
	gqlClient := xplora.NewClient(auth)

	authResp, err := doSignIn(gqlClient, clientID)
	if err != nil {
		log.Error().Err(err).Msg("Xplora sign-in failed")
		return nil, fmt.Errorf("Xplora login failed: %w", err)
	}
	if authResp.Token == "" {
		return nil, fmt.Errorf("Xplora login returned empty token")
	}
	if authResp.User == nil || authResp.User.ID == "" {
		return nil, fmt.Errorf("Xplora login returned no user ID")
	}
	log.Info().Str("user_id", authResp.User.ID).Msg("Xplora sign-in succeeded")

	if err := auth.SetCredentials(&xplora.Credentials{
		Token:        authResp.Token,
		RefreshToken: authResp.RefreshToken,
		ExpireDate:   string(authResp.ExpireDate),
		UserID:       authResp.User.ID,
	}); err != nil {
		return nil, fmt.Errorf("saving Xplora credentials: %w", err)
	}

	devices, err := gqlClient.GetDeviceList(ctx)
	if err != nil {
		log.Warn().Err(err).Msg("deviceList failed after sign-in; watch list will be empty")
	}
	var children []xplora.WatchInfo
	for _, d := range devices {
		if d.User == nil || d.User.ID == "" {
			continue
		}
		name := d.User.Name
		w := xplora.WatchInfo{
			ID:   d.ID,
			Name: &name,
			User: &xplora.UserRef{ID: d.User.ID, Name: d.User.Name},
		}
		if d.User.File != nil && d.User.File.Orig != nil && d.User.File.Orig.URLPathS3 != "" {
			w.AvatarURL = d.User.File.Orig.URLPathS3
		}
		children = append(children, w)
	}

	meta.UserID = authResp.User.ID
	meta.ClientID = clientID
	meta.Children = children

	ul, err := xl.user.NewLogin(ctx, &database.UserLogin{
		ID:         loginID,
		RemoteName: remoteName,
		Metadata:   meta,
	}, &bridgev2.NewLoginParams{
		LoadUserLogin: func(_ context.Context, login *bridgev2.UserLogin) error {
			login.Client = newXploraClient(xl.connector, login, auth, gqlClient, meta)
			return nil
		},
	})
	if err != nil {
		return nil, fmt.Errorf("saving login: %w", err)
	}

	ul.Client.Connect(ul.Log.WithContext(context.Background()))

	return &bridgev2.LoginStep{
		Type:         bridgev2.LoginStepTypeComplete,
		StepID:       "com.xplora.complete",
		Instructions: fmt.Sprintf("Successfully logged in as %s", remoteName),
		CompleteParams: &bridgev2.LoginCompleteParams{
			UserLoginID: ul.ID,
			UserLogin:   ul,
		},
	}, nil
}

// Cancel is called if the user aborts. No open connections to tear down.
func (xl *XploraLogin) Cancel() {}

// loadOrCreateClientID reads the persisted FCM client UUID from dir/client_id.txt,
// or generates and saves a new one if the file is absent or empty.
// The file is NOT removed by LogoutRemote, so the UUID survives logout+re-login.
func loadOrCreateClientID(dir string) string {
	path := filepath.Join(dir, "client_id.txt")
	if data, err := os.ReadFile(path); err == nil {
		if id := strings.TrimSpace(string(data)); id != "" {
			return id
		}
	}
	id := uuid.New().String()
	_ = os.WriteFile(path, []byte(id), 0o600)
	return id
}
