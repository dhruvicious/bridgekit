package config

import "os"

type Config struct {
	Addr, DBPath, EncryptionKey             string
	GitHubAPIURL, StripeAPIURL, SlackAPIURL string
}

func Load() Config {
	return Config{
		Addr:          env("BRIDGEKIT_ADDR", ":8080"),
		DBPath:        env("BRIDGEKIT_DB", "./bridgekit.db"),
		EncryptionKey: os.Getenv("BRIDGEKIT_ENCRYPTION_KEY"),
		GitHubAPIURL:  env("GITHUB_API_URL", "https://api.github.com"),
		StripeAPIURL:  env("STRIPE_API_URL", "https://api.stripe.com"),
		SlackAPIURL:   env("SLACK_API_URL", "https://slack.com/api"),
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
