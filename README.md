# Bridgekit

A self-hosted Go gateway that exposes a small, consistent HTTP interface over real GitHub, Stripe, and Slack APIs. Provider API URLs default to their official services; no mock server is included.

## Run

Set the provider credentials you want to use, then start the gateway:

```sh
cp .env.example .env
export GITHUB_TOKEN=...
export STRIPE_SECRET_KEY=...
export SLACK_BOT_TOKEN=...
go run ./cmd/bridgekit
```

The server listens on `:8080` by default. `GET /healthz` reports process health. Credentials are read from the process environment; use your deployment platform's secret manager, and do not expose this unauthenticated gateway publicly.

## HTTP API

- `GET /v1/github/repos?owner=OWNER` — list authenticated user's repositories or a public owner's repositories. GitHub token required.
- `GET /v1/stripe/charges?limit=25` — list charges (limit from 1 to 100). Stripe secret key required.
- `POST /v1/slack/messages` — post a message. JSON body: `{"channel":"C123","text":"Hello"}`. Slack bot token required.

Provider failures are returned as `502`; missing credentials are returned as `503`. The gateway does not store credentials, implement OAuth installation, or verify inbound webhooks yet. Those require provider-specific registration and security work before production use.
