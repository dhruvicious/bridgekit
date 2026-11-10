// Package slack is the Slack connector: OAuth2 installation, Web API methods, and request signature verification.
package slack

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"bridgekit/sdk"
)

// Connector is the Slack connector.
type Connector struct {
	mockBaseURL string
	apiBaseURL  string
	oauth       sdk.OAuth2Config
	live        bool
}

// New returns a Slack connector; an empty mockBaseURL means the mock provider at
// localhost:8081.
func New(mockBaseURL string) *Connector {
	if mockBaseURL == "" {
		mockBaseURL = "localhost:8081"
	}
	return &Connector{mockBaseURL: mockBaseURL, apiBaseURL: "http://" + mockBaseURL}
}

// NewLive configures the connector for Slack’s Web API and OAuth v2.
func NewLive(apiBaseURL, clientID, clientSecret, redirectURL string) *Connector {
	return &Connector{apiBaseURL: strings.TrimRight(apiBaseURL, "/"), live: true, oauth: sdk.OAuth2Config{
		AuthURL: "https://slack.com/oauth/v2/authorize", TokenURL: "https://slack.com/api/oauth.v2.access",
		ClientID: clientID, ClientSecret: clientSecret, RedirectURL: redirectURL,
		Scopes: []string{"chat:write", "channels:read"},
	}}
}

func (c *Connector) APIBaseURL() string { return c.apiBaseURL }

func (c *Connector) methodPath(method string) string {
	if c.mockBaseURL != "" {
		return "/services/slack/" + method
	}
	return "/" + method
}

type AuthTestOutput struct {
	OK     bool   `json:"ok"`
	URL    string `json:"url"`
	Team   string `json:"team"`
	User   string `json:"user"`
	TeamID string `json:"team_id"`
	UserID string `json:"user_id"`
}

// SendMessageInput is the request body for the send_message endpoint.
type SendMessageInput struct {
	Channel string `json:"channel"`
	Text    string `json:"text"`
}

// SendMessageOutput is Slack's chat.postMessage response.
type SendMessageOutput struct {
	OK      bool   `json:"ok"`
	Channel string `json:"channel"`
	TS      string `json:"ts,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (c *Connector) Metadata() sdk.ConnectorMetadata {
	return sdk.ConnectorMetadata{
		ID:          "slack",
		Name:        "Slack",
		Description: "Send messages and receive events from Slack workspaces.",
		Category:    "Communication",
		Scopes:      []string{"chat:write", "channels:read", "incoming-webhook"},
	}
}

func (c *Connector) AuthConfig() sdk.AuthConfig {
	if c.mockBaseURL != "" {
		return sdk.AuthConfig{Type: sdk.AuthTypeOAuth2, OAuth2: &sdk.OAuth2Config{AuthURL: fmt.Sprintf("http://%s/mock/oauth/authorize", c.mockBaseURL), TokenURL: fmt.Sprintf("http://%s/services/slack/oauth/token", c.mockBaseURL), Scopes: []string{"chat:write", "channels:read"}}}
	}
	return sdk.AuthConfig{Type: sdk.AuthTypeOAuth2, OAuth2: &c.oauth}
}

func (c *Connector) Endpoints() []sdk.EndpointDefinition {
	return []sdk.EndpointDefinition{
		{
			Name: "auth_test", Method: "POST", Path: c.methodPath("auth.test"),
			Description:  "Check the authenticated Slack workspace without changing data.",
			OutputSchema: &AuthTestOutput{}, Execute: c.executeAuthTest,
		},
		{
			Name:         "send_message",
			Method:       "POST",
			Path:         c.methodPath("chat.postMessage"),
			Description:  "Send a message to a channel.",
			InputSchema:  &SendMessageInput{},
			OutputSchema: &SendMessageOutput{},
			Execute:      c.executeSendMessage,
		},
	}
}

func (c *Connector) executeAuthTest(ctx context.Context, client *sdk.HTTPClient, _ sdk.Request) (sdk.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.methodPath("auth.test"), nil)
	if err != nil {
		return sdk.Response{}, sdk.NewPermanentError("failed to create HTTP request", http.StatusInternalServerError, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return sdk.Response{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return sdk.Response{}, sdk.NewTransientError("failed to read response body", resp.StatusCode, err)
	}
	var result AuthTestOutput
	if err := json.Unmarshal(body, &result); err != nil {
		return sdk.Response{}, sdk.NewPermanentError("invalid Slack auth.test response", resp.StatusCode, err)
	}
	if !result.OK {
		return sdk.Response{}, sdk.NewPermanentError("Slack auth.test failed", resp.StatusCode, nil)
	}
	return sdk.Response{Body: body, StatusCode: resp.StatusCode}, nil
}

func (c *Connector) executeSendMessage(ctx context.Context, client *sdk.HTTPClient, req sdk.Request) (sdk.Response, error) {
	var input SendMessageInput
	if err := json.Unmarshal(req.Body, &input); err != nil {
		return sdk.Response{}, sdk.NewPermanentError("invalid JSON body", http.StatusBadRequest, err)
	}

	if input.Channel == "" || input.Text == "" {
		return sdk.Response{}, sdk.NewPermanentError("channel and text are required fields", http.StatusBadRequest, nil)
	}

	payloadBytes, err := json.Marshal(input)
	if err != nil {
		return sdk.Response{}, sdk.NewPermanentError("failed to marshal request", http.StatusInternalServerError, err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.methodPath("chat.postMessage"), strings.NewReader(string(payloadBytes)))
	if err != nil {
		return sdk.Response{}, sdk.NewPermanentError("failed to create HTTP request", http.StatusInternalServerError, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(httpReq)
	if err != nil {
		return sdk.Response{}, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return sdk.Response{}, sdk.NewTransientError("failed to read response body", resp.StatusCode, err)
	}

	var result SendMessageOutput
	if err := json.Unmarshal(body, &result); err != nil {
		return sdk.Response{}, sdk.NewPermanentError("invalid Slack response", resp.StatusCode, err)
	}
	if !result.OK {
		return sdk.Response{}, sdk.NewPermanentError("Slack API error: "+result.Error, resp.StatusCode, nil)
	}
	return sdk.Response{Body: body, StatusCode: resp.StatusCode}, nil
}

// HandleWebhook checks "v0=" plus the hex HMAC-SHA256 of the body under event.Secret.
// Real Slack signs "v0:{timestamp}:{body}".
func (c *Connector) HandleWebhook(ctx context.Context, event sdk.WebhookEvent) error {
	sig := event.Headers["X-Slack-Signature"]
	if sig == "" {
		return sdk.NewPermanentError("missing Slack signature header", http.StatusBadRequest, nil)
	}

	if !c.live {
		if !sdk.VerifyHMACSHA256HexSignature(event.Payload, sig, "v0=", event.Secret) {
			return sdk.NewPermanentError("invalid webhook signature", http.StatusUnauthorized, nil)
		}
		return nil
	}
	timestamp := event.Headers["X-Slack-Request-Timestamp"]
	unix, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || timestamp == "" || time.Since(time.Unix(unix, 0)) > 5*time.Minute || time.Until(time.Unix(unix, 0)) > 5*time.Minute {
		return sdk.NewPermanentError("stale or invalid Slack request timestamp", http.StatusUnauthorized, nil)
	}
	if len(event.Secret) == 0 {
		return sdk.NewPermanentError("missing Slack signing secret", http.StatusUnauthorized, nil)
	}
	mac := hmac.New(sha256.New, event.Secret)
	mac.Write([]byte("v0:" + timestamp + ":"))
	mac.Write(event.Payload)
	got, err := hex.DecodeString(strings.TrimPrefix(sig, "v0="))
	if err != nil || !hmac.Equal(got, mac.Sum(nil)) {
		return sdk.NewPermanentError("invalid webhook signature", http.StatusUnauthorized, nil)
	}
	return nil
}
