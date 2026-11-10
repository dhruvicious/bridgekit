// Command gateway runs the Bridgekit gateway in mock or live-provider mode.
package main

import (
	"errors"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"bridgekit/gateway"
	"bridgekit/mock"
)

func main() {
	addr := flag.String("addr", ":8080", "Gateway listen address")
	dbPath := flag.String("db", "./bridgekit.db", "SQLite database path")
	mode := flag.String("mode", envOr("BRIDGEKIT_MODE", "mock"), "Provider mode: mock or live")
	mockAddr := flag.String("mock-addr", ":8081", "Mock provider listen address, in :port form")
	publicURL := flag.String("public-url", os.Getenv("BRIDGEKIT_PUBLIC_URL"), "Public gateway base URL for OAuth callbacks")
	webhookSecret := flag.String("webhook-secret", envOr("BRIDGEKIT_WEBHOOK_SECRET", devWebhookSecret), "Shared HMAC webhook secret")
	flag.Parse()

	production := os.Getenv("BRIDGEKIT_ENV") == "production"
	apiToken := os.Getenv("BRIDGEKIT_API_TOKEN")
	if (*mode == "live" || production) && len(apiToken) < 32 {
		fatal("BRIDGEKIT_API_TOKEN must contain at least 32 characters in live or production mode")
	}
	if *mode == "live" && !production {
		fatal("live provider mode requires BRIDGEKIT_ENV=production so real credentials are encrypted at rest")
	}
	if err := checkWebhookSecret(*webhookSecret, production); err != nil {
		fatal("invalid webhook secret", "err", err)
	}

	if *mode == "mock" {
		slog.Info("starting mock provider", "component", "main", "addr", *mockAddr)
		if err := mock.NewServer(*mockAddr, *webhookSecret).Start(); err != nil {
			fatal("start mock provider failed", "addr", *mockAddr, "err", err)
		}
	}

	db, err := gateway.NewDB(*dbPath)
	if err != nil {
		fatal("open database failed", "path", *dbPath, "err", err)
	}
	defer db.Close()

	gw, err := gateway.NewWithOptions(db, gateway.Options{
		Mode: *mode, MockHost: "localhost" + *mockAddr, WebhookSecret: *webhookSecret, APIToken: apiToken,
		PublicURL:      *publicURL,
		GitHubAPIURL:   envOr("GITHUB_API_URL", "https://api.github.com"),
		GitHubClientID: os.Getenv("GITHUB_CLIENT_ID"), GitHubClientSecret: os.Getenv("GITHUB_CLIENT_SECRET"),
		SlackAPIURL:   envOr("SLACK_API_URL", "https://slack.com/api"),
		SlackClientID: os.Getenv("SLACK_CLIENT_ID"), SlackClientSecret: os.Getenv("SLACK_CLIENT_SECRET"),
		StripeAPIURL: envOr("STRIPE_API_URL", "https://api.stripe.com"),
	})
	if err != nil {
		fatal("configure providers failed", "err", err)
	}
	if err := gw.Start(*addr); err != nil {
		fatal("start gateway failed", "addr", *addr, "err", err)
	}

	slog.Info("bridgekit gateway running", "component", "main", "addr", *addr, "mode", *mode)
	slog.Info("press Ctrl+C to stop", "component", "main")
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	slog.Info("shutting down", "component", "main")
}

const devWebhookSecret = "mock_webhook_secret_value"

func checkWebhookSecret(secret string, production bool) error {
	if secret == "" {
		return errors.New("webhook secret is empty; set BRIDGEKIT_WEBHOOK_SECRET")
	}
	if production && secret == devWebhookSecret {
		return errors.New("BRIDGEKIT_WEBHOOK_SECRET must be set in production; the default is public")
	}
	return nil
}

func fatal(msg string, args ...any) {
	slog.Error(msg, append([]any{"component", "main"}, args...)...)
	os.Exit(1)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
