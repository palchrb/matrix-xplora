package connector

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/palchrb/matrix-xplora/internal/xplora"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
)

// XploraLogin implements the single-step password login flow.
//
// Step 1: Collect countryCode, phone, password → call SignIn → done.
type XploraLogin struct {
	connector *XploraConnector
	user      *bridgev2.User
}

var _ bridgev2.LoginProcessUserInput = (*XploraLogin)(nil)

// Start returns the single input step with credential fields.
func (xl *XploraLogin) Start(_ context.Context) (*bridgev2.LoginStep, error) {
	return &bridgev2.LoginStep{
		Type:         bridgev2.LoginStepTypeUserInput,
		StepID:       "com.xplora.enter_credentials",
		Instructions: "Enter your Xplora parent app credentials.",
		UserInputParams: &bridgev2.LoginUserInputParams{
			Fields: []bridgev2.LoginInputDataField{
				{
					Type:    bridgev2.LoginInputFieldTypeUsername,
					ID:      "country_code",
					Name:    "Country code (digits only, e.g. 47 for Norway)",
					Pattern: `^\d{1,4}$`,
				},
				{
					Type:    bridgev2.LoginInputFieldTypeUsername,
					ID:      "phone",
					Name:    "Phone number (without country code)",
					Pattern: `^\d{5,15}$`,
				},
				{
					Type: bridgev2.LoginInputFieldTypePassword,
					ID:   "password",
					Name: "Password",
				},
			},
		},
	}, nil
}

// SubmitUserInput handles the credential submission and completes the login.
func (xl *XploraLogin) SubmitUserInput(ctx context.Context, input map[string]string) (*bridgev2.LoginStep, error) {
	countryCode := input["country_code"]
	phone := input["phone"]
	password := input["password"]

	if countryCode == "" {
		return nil, fmt.Errorf("country code is required")
	}
	if phone == "" {
		return nil, fmt.Errorf("phone number is required")
	}
	if password == "" {
		return nil, fmt.Errorf("password is required")
	}

	loginID := loginIDFromPhone(countryCode, phone)
	sessDir := xl.connector.sessionDir(loginID)

	if err := os.MkdirAll(sessDir, 0o700); err != nil {
		return nil, fmt.Errorf("creating session directory: %w", err)
	}

	auth := xplora.NewAuth(sessDir)
	gqlClient := xplora.NewClient(auth)

	authResp, err := gqlClient.SignIn(ctx, countryCode, phone, password)
	if err != nil {
		return nil, fmt.Errorf("Xplora login failed: %w", err)
	}
	if authResp.Token == "" {
		return nil, fmt.Errorf("Xplora login returned empty token")
	}

	if authResp.User == nil || authResp.User.ID == "" {
		return nil, fmt.Errorf("Xplora login returned no user ID")
	}
	userID := authResp.User.ID

	creds := &xplora.Credentials{
		Token:        authResp.Token,
		RefreshToken: authResp.RefreshToken,
		ExpireDate:   string(authResp.ExpireDate),
		UserID:       userID,
	}
	if err := auth.SetCredentials(creds); err != nil {
		return nil, fmt.Errorf("saving Xplora credentials: %w", err)
	}

	// Build WatchInfo list from sign-in response children.
	var children []xplora.WatchInfo
	for _, c := range authResp.User.Children {
		if c.Ward != nil && c.Ward.ID != "" {
			name := c.Ward.Name
			w := xplora.WatchInfo{
				ID:   c.Ward.ID,
				Name: &name,
				User: c.Ward,
			}
			if c.Ward.File != nil && c.Ward.File.ID != "" {
				w.AvatarURL = "https://api.myxplora.com/file?id=" + c.Ward.File.ID
			}
			children = append(children, w)
		}
	}

	// Load or generate a stable ClientID for FCM registration.
	// The ClientID identifies the virtual Android device registered with Google.
	// It must survive logout+login cycles: re-registering a new device on every
	// login causes Google rate-limiting and makes Xplora route pushes to the
	// old device until the association is refreshed.
	clientID := loadOrCreateClientID(sessDir)

	meta := &UserLoginMetadata{
		PhoneNumber: phone,
		CountryCode: countryCode,
		UserID:      userID,
		ClientID:    clientID,
		Children:    children,
	}

	ul, err := xl.user.NewLogin(ctx, &database.UserLogin{
		ID:         loginID,
		RemoteName: "+" + countryCode + phone,
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

	// Start the connection immediately without requiring a restart.
	ul.Client.Connect(ul.Log.WithContext(context.Background()))

	return &bridgev2.LoginStep{
		Type:         bridgev2.LoginStepTypeComplete,
		StepID:       "com.xplora.complete",
		Instructions: fmt.Sprintf("Successfully logged in as +%s%s", countryCode, phone),
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
