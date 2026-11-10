// Package stripe is the Stripe connector: API-key auth, charge listing, and webhook verification.
package stripe

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"bridgekit/sdk"
)

// Connector is the Stripe connector. API-key auth needs no provider URLs.
type Connector struct {
	apiBaseURL string
	mock       bool
	live       bool
}

// New returns a Stripe connector.
func New() *Connector {
	return &Connector{apiBaseURL: "http://localhost:8081", mock: true}
}

// NewLive configures Stripe’s production API endpoint.
func NewLive(apiBaseURL string) *Connector {
	return &Connector{apiBaseURL: strings.TrimRight(apiBaseURL, "/"), live: true}
}

func (c *Connector) APIBaseURL() string { return c.apiBaseURL }

func (c *Connector) chargesPath() string {
	if c.mock {
		return "/services/stripe/v1/charges"
	}
	return "/v1/charges"
}

func (c *Connector) Metadata() sdk.ConnectorMetadata {
	return sdk.ConnectorMetadata{
		ID:          "stripe",
		Name:        "Stripe",
		Description: "List charges and receive payment events from Stripe.",
		Category:    "Payments",
		Scopes:      []string{},
	}
}

func (c *Connector) AuthConfig() sdk.AuthConfig {
	return sdk.AuthConfig{
		Type: sdk.AuthTypeAPIKey,
		APIKey: &sdk.APIKeyConfig{
			HeaderName:  "Authorization",
			ValuePrefix: "Bearer ",
		},
	}
}

// Charge represents a Stripe charge object.
type Charge struct {
	ID       string `json:"id"`
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
	Status   string `json:"status"`
	Created  int64  `json:"created"`
}

// ChargeList is the top-level Stripe list response.
type ChargeList struct {
	Object  string   `json:"object"`
	Data    []Charge `json:"data"`
	HasMore bool     `json:"has_more"`
	URL     string   `json:"url"`
}

func (c *Connector) Endpoints() []sdk.EndpointDefinition {
	return []sdk.EndpointDefinition{
		{
			Name:         "list_charges",
			Method:       "GET",
			Path:         c.chargesPath(),
			Description:  "List Stripe charges with offset pagination (starting_after cursor).",
			OutputSchema: &ChargeList{},
			Execute:      c.executeListCharges,
			GetPaginator: c.listChargesPaginator,
		},
	}
}

func (c *Connector) executeListCharges(ctx context.Context, client *sdk.HTTPClient, req sdk.Request) (sdk.Response, error) {
	reqURL, err := url.Parse(c.chargesPath())
	if err != nil {
		return sdk.Response{}, sdk.NewPermanentError("invalid path", http.StatusInternalServerError, err)
	}

	q := reqURL.Query()
	if limit, ok := req.Params["limit"]; ok && limit != "" {
		q.Set("limit", limit)
	}
	if startingAfter, ok := req.Params["starting_after"]; ok && startingAfter != "" {
		q.Set("starting_after", startingAfter)
	}
	reqURL.RawQuery = q.Encode()

	httpReq, err := http.NewRequestWithContext(ctx, "GET", reqURL.String(), nil)
	if err != nil {
		return sdk.Response{}, sdk.NewPermanentError("failed to create HTTP request", http.StatusInternalServerError, err)
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		return sdk.Response{}, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return sdk.Response{}, sdk.NewTransientError("failed to read response body", resp.StatusCode, err)
	}

	return sdk.Response{Body: body, StatusCode: resp.StatusCode}, nil
}

// listChargesPaginator pages with starting_after, the ID of the previous page's last charge.
func (c *Connector) listChargesPaginator(path string, params map[string]string, limit int) sdk.Paginator {
	return sdk.NewCursorPaginator(
		c.chargesPath(),
		"starting_after",
		"limit",
		limit,
		extractStripeNextCursor,
	)
}

// extractStripeNextCursor uses the last charge's ID as the next starting_after cursor.
func extractStripeNextCursor(body []byte, _ http.Header) (string, bool, error) {
	var list struct {
		HasMore bool     `json:"has_more"`
		Data    []Charge `json:"data"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return "", false, fmt.Errorf("failed to parse Stripe list response: %w", err)
	}
	if !list.HasMore || len(list.Data) == 0 {
		return "", false, nil
	}
	return list.Data[len(list.Data)-1].ID, true, nil
}

// HandleWebhook checks the v1 HMAC-SHA256 of the body in the Stripe-Signature header under
// event.Secret. Real Stripe signs "{t}.{body}".
func (c *Connector) HandleWebhook(ctx context.Context, event sdk.WebhookEvent) error {
	sigHeader := event.Headers["Stripe-Signature"]
	if sigHeader == "" {
		return sdk.NewPermanentError("missing Stripe-Signature header", http.StatusBadRequest, nil)
	}

	sig := ""
	for _, part := range strings.Split(sigHeader, ",") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "v1=") {
			sig = part
			break
		}
	}
	if sig == "" {
		return sdk.NewPermanentError("no v1 signature found in Stripe-Signature header", http.StatusBadRequest, nil)
	}

	if !c.live {
		if !sdk.VerifyHMACSHA256HexSignature(event.Payload, sig, "v1=", event.Secret) {
			return sdk.NewPermanentError("invalid Stripe webhook signature", http.StatusUnauthorized, nil)
		}
		return nil
	}
	var timestamp string
	var signatures []string
	for _, part := range strings.Split(sigHeader, ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		if key == "t" {
			timestamp = value
		}
		if key == "v1" {
			signatures = append(signatures, value)
		}
	}
	unix, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || timestamp == "" || time.Since(time.Unix(unix, 0)) > 5*time.Minute || time.Until(time.Unix(unix, 0)) > 5*time.Minute {
		return sdk.NewPermanentError("stale or invalid Stripe webhook timestamp", http.StatusUnauthorized, nil)
	}
	if len(event.Secret) == 0 {
		return sdk.NewPermanentError("missing Stripe webhook signing secret", http.StatusUnauthorized, nil)
	}
	mac := hmac.New(sha256.New, event.Secret)
	mac.Write([]byte(timestamp + "."))
	mac.Write(event.Payload)
	expected := mac.Sum(nil)
	for _, candidate := range signatures {
		got, err := hex.DecodeString(candidate)
		if err == nil && hmac.Equal(got, expected) {
			return nil
		}
	}
	return sdk.NewPermanentError("invalid Stripe webhook signature", http.StatusUnauthorized, nil)
}
