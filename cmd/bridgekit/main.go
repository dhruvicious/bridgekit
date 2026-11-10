package main

import (
	"log/slog"
	"net/http"
	"os"
	"time"

	"bridgekit/internal/config"
	"bridgekit/internal/gateway"
)

func main() {
	cfg := config.Load()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)
	httpServer := &http.Server{Addr: cfg.Addr, Handler: gateway.New(cfg).Handler(), ReadHeaderTimeout: 10 * time.Second}
	logger.Info("Bridgekit starting", "addr", cfg.Addr)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
