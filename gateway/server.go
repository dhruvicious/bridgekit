// Package gateway is Bridgekit's HTTP server: it installs connector instances, runs their
// endpoints, receives webhooks and detects schema drift.
package gateway

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"bridgekit/connectors/github"
	"bridgekit/connectors/slack"
	"bridgekit/connectors/stripe"
	"bridgekit/sdk"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Server serves the API, webhooks, docs, metrics and dashboard.
type Server struct {
	db             *DB
	mockHost       string
	apiBases       map[string]string
	apiToken       string
	webhookSecret  string
	registry       map[string]sdk.Connector
	pendingOAuth   map[string]pendingInstall // OAuth state -> install in progress
	pendingOAuthMu sync.Mutex
	driftDetector  *DriftDetector
}

// pendingInstall is an OAuth install that was started but not completed.
type pendingInstall struct {
	connectorID string
	started     time.Time
}

// oauthStateTTL is how long a user has to finish the provider's consent screen.
const oauthStateTTL = 10 * time.Minute

// maxPendingInstalls caps unfinished OAuth installs held in memory.
const maxPendingInstalls = 1000

// maxBodyBytes caps request bodies so one large upload cannot exhaust memory. It
// matches GitHub's 25 MB webhook payload limit, the largest a provider sends.
const maxBodyBytes = 25 << 20

// Options selects mock or live provider endpoints and OAuth app settings.
type Options struct {
	Mode, MockHost, WebhookSecret, PublicURL, APIToken string
	GitHubAPIURL, GitHubClientID, GitHubClientSecret   string
	SlackAPIURL, SlackClientID, SlackClientSecret      string
	StripeAPIURL                                       string
}

// New retains the local demo defaults used by the test suite and quick start.
func New(db *DB, mockHost string, webhookSecret string) *Server {
	s, _ := NewWithOptions(db, Options{Mode: "mock", MockHost: mockHost, WebhookSecret: webhookSecret})
	return s
}

// NewWithOptions builds the provider registry with either the local simulator or live APIs.
func NewWithOptions(db *DB, opts Options) (*Server, error) {
	mode := opts.Mode
	if mode == "" {
		mode = "mock"
	}
	var registry map[string]sdk.Connector
	apiBases := make(map[string]string)
	switch mode {
	case "mock":
		if opts.MockHost == "" {
			opts.MockHost = "localhost:8081"
		}
		registry = map[string]sdk.Connector{"slack": slack.New(opts.MockHost), "github": github.New(opts.MockHost), "stripe": stripe.New()}
		for id := range registry {
			apiBases[id] = "http://" + opts.MockHost
		}
	case "live":
		if len(opts.APIToken) < 32 {
			return nil, fmt.Errorf("BRIDGEKIT_API_TOKEN must contain at least 32 characters")
		}
		if opts.PublicURL == "" || opts.APIToken == "" || opts.GitHubClientID == "" || opts.GitHubClientSecret == "" || opts.SlackClientID == "" || opts.SlackClientSecret == "" {
			return nil, fmt.Errorf("live mode requires BRIDGEKIT_PUBLIC_URL, BRIDGEKIT_API_TOKEN, and GitHub and Slack OAuth client IDs and secrets")
		}
		if err := validatePublicURL(opts.PublicURL); err != nil {
			return nil, err
		}
		if opts.GitHubAPIURL == "" {
			opts.GitHubAPIURL = "https://api.github.com"
		}
		if opts.SlackAPIURL == "" {
			opts.SlackAPIURL = "https://slack.com/api"
		}
		if opts.StripeAPIURL == "" {
			opts.StripeAPIURL = "https://api.stripe.com"
		}
		for name, endpoint := range map[string]string{"GitHub API": opts.GitHubAPIURL, "Slack API": opts.SlackAPIURL, "Stripe API": opts.StripeAPIURL} {
			if err := validateProviderURL(name, endpoint); err != nil {
				return nil, err
			}
		}
		redirect := strings.TrimRight(opts.PublicURL, "/") + "/oauth/callback"
		registry = map[string]sdk.Connector{
			"slack":  slack.NewLive(opts.SlackAPIURL, opts.SlackClientID, opts.SlackClientSecret, redirect),
			"github": github.NewLive(opts.GitHubAPIURL, opts.GitHubClientID, opts.GitHubClientSecret, redirect),
			"stripe": stripe.NewLive(opts.StripeAPIURL),
		}
		apiBases["slack"], apiBases["github"], apiBases["stripe"] = opts.SlackAPIURL, opts.GitHubAPIURL, opts.StripeAPIURL
	default:
		return nil, fmt.Errorf("unsupported BRIDGEKIT_MODE %q (use mock or live)", mode)
	}
	s := &Server{db: db, mockHost: opts.MockHost, apiBases: apiBases, apiToken: opts.APIToken, webhookSecret: opts.WebhookSecret, registry: registry, pendingOAuth: make(map[string]pendingInstall)}
	s.driftDetector = NewDriftDetectorWithBases(db, registry, apiBases)
	return s, nil
}

func validatePublicURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !isSecureURL(u) {
		return fmt.Errorf("BRIDGEKIT_PUBLIC_URL must be an HTTPS URL (HTTP is allowed only for localhost)")
	}
	return nil
}

func validateProviderURL(name, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !isSecureURL(u) {
		return fmt.Errorf("%s URL must be HTTPS (HTTP is allowed only for localhost)", name)
	}
	return nil
}

func isSecureURL(u *url.URL) bool {
	if u.Scheme == "https" {
		return true
	}
	if u.Scheme != "http" {
		return false
	}
	if strings.EqualFold(u.Hostname(), "localhost") {
		return true
	}
	ip := net.ParseIP(u.Hostname())
	return ip != nil && ip.IsLoopback()
}

// Handler returns the gateway's routes; tests mount it on an httptest.Server.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/v1/connectors", s.handleListConnectors)
	mux.HandleFunc("/v1/connectors/", s.handleInstallConnector) // POST /v1/connectors/{id}/install
	mux.HandleFunc("/oauth/callback", s.handleOAuthCallback)

	mux.HandleFunc("/v1/instances", s.handleListInstances)
	// GET and DELETE /v1/instances/{id}, POST /v1/instances/{id}/endpoints/{endpoint}
	mux.HandleFunc("/v1/instances/", s.handleInstanceRoute)

	mux.HandleFunc("/webhooks/", s.handleWebhookPayload)

	mux.HandleFunc("/openapi.json", s.handleOpenAPISpec)
	mux.HandleFunc("/docs", s.handleDocs)
	mux.HandleFunc("/docs/", s.handleDocs)
	mux.Handle("/metrics", promhttp.Handler())

	mux.Handle("/ui/", http.StripPrefix("/ui/", http.FileServer(http.Dir("./ui"))))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/ui/", http.StatusFound)
			return
		}
		http.NotFound(w, r)
	})

	return logRequests(s.apiAuth(mux))
}

func (s *Server) apiAuth(next http.Handler) http.Handler {
	if s.apiToken == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/") {
			authorization := r.Header.Get("Authorization")
			provided := strings.TrimPrefix(authorization, "Bearer ")
			if !strings.HasPrefix(authorization, "Bearer ") || subtle.ConstantTimeCompare([]byte(provided), []byte(s.apiToken)) != 1 {
				w.Header().Set("WWW-Authenticate", "Bearer")
				writeJSONError(w, http.StatusUnauthorized, "A valid Bridgekit API token is required")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// Start binds addr before returning, so the port is open for callers, then serves in the
// background and starts the drift detector.
func (s *Server) Start(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}

	s.driftDetector.Start(30 * time.Second)

	slog.Info("gateway starting", "component", "gateway", "addr", addr)
	server := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		if err := server.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
			slog.Error("gateway stopped serving", "component", "gateway", "err", err)
		}
	}()
	return nil
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		slog.Info("request", "component", "gateway", "method", r.Method, "path", r.URL.Path, "duration", time.Since(start))
	})
}

// tokenRefreshHook returns an instance's refresh hook; it persists the new token so later
// requests and drift scans use it.
func (s *Server) tokenRefreshHook(instanceID string, conn sdk.Connector) func(ctx context.Context, token *sdk.Token) (*sdk.Token, error) {
	connectorID := conn.Metadata().ID
	return func(ctx context.Context, oldToken *sdk.Token) (*sdk.Token, error) {
		slog.Info("refreshing oauth token", "component", "gateway", "instance_id", instanceID, "connector", connectorID)

		authCfg := conn.AuthConfig()
		if authCfg.Type != sdk.AuthTypeOAuth2 || authCfg.OAuth2 == nil {
			return nil, fmt.Errorf("connector does not support OAuth2")
		}

		form := url.Values{
			"grant_type": {"refresh_token"}, "refresh_token": {oldToken.RefreshToken},
			"client_id": {authCfg.OAuth2.ClientID}, "client_secret": {authCfg.OAuth2.ClientSecret},
		}
		client := &http.Client{Timeout: 10 * time.Second}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, authCfg.OAuth2.TokenURL, strings.NewReader(form.Encode()))
		if err != nil {
			recordTokenRefresh(connectorID, "failure")
			return nil, fmt.Errorf("failed to create refresh request: %w", err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			recordTokenRefresh(connectorID, "failure")
			return nil, fmt.Errorf("failed to call refresh URL: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			recordTokenRefresh(connectorID, "failure")
			return nil, fmt.Errorf("refresh server returned code %d", resp.StatusCode)
		}

		var tokenResp struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			ExpiresIn    int    `json:"expires_in"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
			recordTokenRefresh(connectorID, "failure")
			return nil, fmt.Errorf("failed to parse refresh response: %w", err)
		}

		if tokenResp.AccessToken == "" {
			recordTokenRefresh(connectorID, "failure")
			return nil, fmt.Errorf("refresh response has no access token")
		}
		var expiry time.Time
		if tokenResp.ExpiresIn > 0 {
			expiry = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
		}
		refreshToken := tokenResp.RefreshToken
		if refreshToken == "" {
			refreshToken = oldToken.RefreshToken
		}
		newToken := &sdk.Token{AccessToken: tokenResp.AccessToken, RefreshToken: refreshToken, Expiry: expiry}

		creds := &sdk.Credentials{Token: newToken}
		if err := s.db.UpdateCredentials(instanceID, sdk.AuthTypeOAuth2, creds); err != nil {
			recordTokenRefresh(connectorID, "failure")
			return nil, fmt.Errorf("failed to save refreshed credentials: %w", err)
		}

		recordTokenRefresh(connectorID, "success")
		slog.Info("oauth token refreshed", "component", "gateway", "instance_id", instanceID, "connector", connectorID)
		return newToken, nil
	}
}

// readBody reads the request body up to maxBodyBytes. On failure it also returns the
// status to answer with: 413 for an oversized body, 400 otherwise.
func readBody(w http.ResponseWriter, r *http.Request) ([]byte, int, error) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return nil, http.StatusRequestEntityTooLarge, err
		}
		return nil, http.StatusBadRequest, err
	}
	return body, http.StatusOK, nil
}

func writeJSONError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
