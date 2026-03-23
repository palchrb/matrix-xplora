package xplora

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const graphQLEndpoint = "https://api.myxplora.com/api"

// Client is a GraphQL client for the Xplora API.
// All methods (except SignIn) require a valid bearer token obtained during login.
type Client struct {
	auth       *Auth
	httpClient *http.Client
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
		httpClient: http.DefaultClient,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// graphQLRequest is the JSON body for a GraphQL request.
type graphQLRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables,omitempty"`
}

// graphQLResponse is the raw JSON response wrapper.
type graphQLResponse struct {
	Data   json.RawMessage  `json:"data"`
	Errors []graphQLError   `json:"errors,omitempty"`
}

// graphQLError is a single error in a GraphQL error response.
type graphQLError struct {
	Message string `json:"message"`
}

func (e graphQLError) Error() string { return e.Message }

// do executes a GraphQL query or mutation.
// Sets the Authorization: Bearer header if a token is available.
// Returns the "data" field of the response as json.RawMessage.
// Returns an error if the response contains top-level "errors".
func (c *Client) do(ctx context.Context, query string, variables map[string]any) (json.RawMessage, error) {
	reqBody, err := json.Marshal(graphQLRequest{Query: query, Variables: variables})
	if err != nil {
		return nil, fmt.Errorf("xplora: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, graphQLEndpoint, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("xplora: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if token := c.auth.Token(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

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
		return nil, fmt.Errorf("xplora API error: %s", gqlResp.Errors[0].Message)
	}

	return gqlResp.Data, nil
}
