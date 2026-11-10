package gateway

import (
	"encoding/json"
	"net/http"
	"strconv"

	"bridgekit/connectors/github"
	"bridgekit/connectors/slack"
	"bridgekit/connectors/stripe"
	"bridgekit/internal/config"
)

type Server struct {
	mux *http.ServeMux
	cfg config.Config
}

func New(cfg config.Config) *Server {
	s := &Server{mux: http.NewServeMux(), cfg: cfg}
	s.mux.HandleFunc("GET /healthz", s.health)
	s.mux.HandleFunc("GET /v1/github/repos", s.listRepositories)
	s.mux.HandleFunc("GET /v1/stripe/charges", s.listCharges)
	s.mux.HandleFunc("POST /v1/slack/messages", s.postMessage)
	return s
}
func (s *Server) Handler() http.Handler { return s.mux }
func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
func (s *Server) listRepositories(w http.ResponseWriter, r *http.Request) {
	if s.cfg.GitHubToken == "" {
		writeError(w, http.StatusServiceUnavailable, "GITHUB_TOKEN is not configured")
		return
	}
	repos, err := github.New(s.cfg.GitHubAPIURL, s.cfg.GitHubToken).ListRepositories(r.Context(), r.URL.Query().Get("owner"))
	if err != nil {
		providerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, repos)
}
func (s *Server) listCharges(w http.ResponseWriter, r *http.Request) {
	if s.cfg.StripeSecretKey == "" {
		writeError(w, http.StatusServiceUnavailable, "STRIPE_SECRET_KEY is not configured")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	charges, err := stripe.New(s.cfg.StripeAPIURL, s.cfg.StripeSecretKey).ListCharges(r.Context(), limit)
	if err != nil {
		providerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, charges)
}
func (s *Server) postMessage(w http.ResponseWriter, r *http.Request) {
	if s.cfg.SlackBotToken == "" {
		writeError(w, http.StatusServiceUnavailable, "SLACK_BOT_TOKEN is not configured")
		return
	}
	var input struct {
		Channel string `json:"channel"`
		Text    string `json:"text"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "request body must be valid JSON")
		return
	}
	if input.Channel == "" || input.Text == "" {
		writeError(w, http.StatusBadRequest, "channel and text are required")
		return
	}
	result, err := slack.New(s.cfg.SlackAPIURL, s.cfg.SlackBotToken).PostMessage(r.Context(), input.Channel, input.Text)
	if err != nil {
		providerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
func providerError(w http.ResponseWriter, err error) {
	writeError(w, http.StatusBadGateway, err.Error())
}
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
