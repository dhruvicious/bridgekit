package main

import (
	"log/slog"
	"net/http"
	"os"

	"bridgekit/internal/config"
	"bridgekit/internal/gateway"
)

func main() {
	cfg := config.Load()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)
	server := gateway.New()
	httpServer := &http.Server{Addr: cfg.Addr, Handler: server.Handler(), ReadHeaderTimeout: 10_000_000_000}
	logger.Info("Bridgekit starting", "addr", cfg.Addr)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
