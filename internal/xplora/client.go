package xplora

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

const (
	graphQLEndpoint = "https://api.myxplora.com/api"
	apiKey          = "fc45d50304511edbf67a12b93c413b6a"
	apiSecret       = "1e9b6fe0327711ed959359c157878dcb"
)

// Client is a GraphQL client for the Xplora API.
// All methods (except SignIn) require a valid bearer token obtained during login.
type Client struct {
	auth       *Auth
	httpClient *http.Client

	// w360 tokens override the main token+apiSecret for H-BackDoor-Authorization
	// when the server returns them in the sign-in response.
	w360Mu     sync.RWMutex
	w360Token  string
	w360Secret string
}

// Option configures Client.
type Option func(*Client)

// WithHTTPClient overrides the HTTP client.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) {
		c.httpClient = hc
	}
}

// NewClient creates a new Xplora GraphQL client.
func NewClient(auth *Auth, opts ...Option) *Client {
	c := &Client{
		auth:       auth,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// setW360 stores w360 tokens for use in subsequent API calls.
func (c *Client) setW360(token, secret string) {
	c.w360Mu.Lock()
	defer c.w360Mu.Unlock()
	c.w360Token = token
	c.w360Secret = secret
}

// backdoorAuth returns the H-BackDoor-Authorization header value.
func (c *Client) backdoorAuth() string {
	token := c.auth.Token()
	if token == "" {
		return "Open " + apiKey + ":" + apiSecret
	}
	c.w360Mu.RLock()
	w360Token, w360Secret := c.w360Token, c.w360Secret
	c.w360Mu.RUnlock()
	if w360Token != "" && w360Secret != "" {
		return "Bearer " + w360Token + ":" + w360Secret
	}
	return "Bearer " + token + ":" + apiSecret
}

// graphQLRequest is the JSON body for a GraphQL request.
type graphQLRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables,omitempty"`
}

// graphQLResponse is the raw JSON response wrapper.
type graphQLResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []graphQLError  `json:"errors,omitempty"`
}

// graphQLError is a single error in a GraphQL error response.
type graphQLError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e graphQLError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("%s: %s", e.Code, e.Message)
	}
	return e.Message
}

// do executes a GraphQL query or mutation.
// Returns the "data" field of the response as json.RawMessage.
// Returns an error if the HTTP status is not 200 or if the response contains errors.
func (c *Client) do(ctx context.Context, query string, variables map[string]any) (json.RawMessage, error) {
	reqBody, err := json.Marshal(graphQLRequest{Query: query, Variables: variables})
	if err != nil {
		return nil, fmt.Errorf("xplora: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, graphQLEndpoint, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("xplora: create request: %w", err)
	}

	now := time.Now().UTC()
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	req.Header.Set("User-Agent", "okhttp/5.3.2")
	req.Header.Set("H-Date", now.Format(http.TimeFormat))
	req.Header.Set("H-Tid", strconv.FormatInt(now.Unix(), 10))
	req.Header.Set("H-BackDoor-Authorization", c.backdoorAuth())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("xplora: HTTP request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("xplora: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("xplora: HTTP %d: %s", resp.StatusCode, string(body))
	}

	var gqlResp graphQLResponse
	if err := json.Unmarshal(body, &gqlResp); err != nil {
		return nil, fmt.Errorf("xplora: unmarshal response: %w", err)
	}

	if len(gqlResp.Errors) > 0 {
		return nil, fmt.Errorf("xplora: %w", gqlResp.Errors[0])
	}

	return gqlResp.Data, nil
}
