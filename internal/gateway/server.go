package gateway

import (
	"encoding/json"
	"net/http"
)

type Server struct{ mux *http.ServeMux }

func New() *Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	return &Server{mux: mux}
}

func (s *Server) Handler() http.Handler { return s.mux }
