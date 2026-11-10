# Live Mode Setup

This guide runs Bridgekit against the real GitHub, Slack, and Stripe APIs. Live mode requires Go 1.26 or newer, registered OAuth apps for GitHub and Slack, and a publicly reachable HTTPS URL for OAuth callbacks.

## 1. Configure the provider apps

Create a GitHub OAuth App and a Slack app, then copy each app's client ID and client secret into the environment. Register this exact callback URL with both apps:

```text
https://YOUR_PUBLIC_HOST/oauth/callback
```

Set `BRIDGEKIT_PUBLIC_URL` to the base URL, without `/oauth/callback`; Bridgekit adds that path itself. Slack requires an HTTPS redirect URL. See the [GitHub OAuth web flow](https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/authorizing-oauth-apps) and [Slack OAuth v2 installation flow](https://api.slack.com/authentication/oauth-v2).

## 2. Configure Bridgekit

Copy the example environment file and edit it:

```sh
cp .env.example .env
```

Set at least these values in `.env`:

```dotenv
BRIDGEKIT_MODE=live
BRIDGEKIT_ENV=production
BRIDGEKIT_PUBLIC_URL=https://YOUR_PUBLIC_HOST
BRIDGEKIT_API_TOKEN=REPLACE_WITH_A_RANDOM_VALUE_AT_LEAST_32_CHARACTERS
BRIDGEKIT_ENCRYPTION_KEY=REPLACE_WITH_BASE64_OF_32_RANDOM_BYTES
BRIDGEKIT_WEBHOOK_SECRET=REPLACE_WITH_A_RANDOM_SECRET
GITHUB_CLIENT_ID=YOUR_GITHUB_OAUTH_CLIENT_ID
GITHUB_CLIENT_SECRET=YOUR_GITHUB_OAUTH_CLIENT_SECRET
SLACK_CLIENT_ID=YOUR_SLACK_CLIENT_ID
SLACK_CLIENT_SECRET=YOUR_SLACK_CLIENT_SECRET
```

Generate random values on macOS or Linux:

```sh
openssl rand -hex 32
openssl rand -base64 32
```

Use the base64 output from `openssl rand -base64 32` for `BRIDGEKIT_ENCRYPTION_KEY`. Keep the API token, encryption key, OAuth client secrets, and webhook secret in a secrets manager for deployment. Do not commit `.env`.

Provider API URLs default to the official endpoints. Override them only when needed:

```dotenv
GITHUB_API_URL=https://api.github.com
SLACK_API_URL=https://slack.com/api
STRIPE_API_URL=https://api.stripe.com
```

Live mode refuses to start unless `BRIDGEKIT_ENV=production`, the API token is at least 32 characters, and the production encryption and webhook settings pass startup validation. The mock provider is not started in live mode.

## 3. Build and run from source

From the repository root, load the environment, build the gateway, and start it:

```sh
set -a
source .env
set +a

go build -o ./bridgekit ./cmd/gateway
./bridgekit
```

The gateway listens on `:8080` by default. When deployed behind a reverse proxy, expose it at `BRIDGEKIT_PUBLIC_URL` over HTTPS and forward traffic to the gateway. The OAuth callback must be reachable by GitHub and Slack.

Open the dashboard at `http://localhost:8080/ui/` for a local browser session, or use your public HTTPS host after deployment. The dashboard prompts for `BRIDGEKIT_API_TOKEN` and keeps it in the current tab's session storage.

## 4. Run with Docker

Build and run the included image with the same `.env` file:

```sh
docker build -t bridgekit .
docker run --env-file .env \
  -p 8080:8080 \
  -v bridgekit-data:/data \
  bridgekit
```

The container stores its SQLite database at `/data/bridgekit.db`. Put TLS termination and public HTTPS routing in front of the container, and set `BRIDGEKIT_PUBLIC_URL` to that public host.

## 5. Use the API

Management routes under `/v1/` require a bearer token in live mode:

```sh
curl -H "Authorization: Bearer $BRIDGEKIT_API_TOKEN" \
  http://localhost:8080/v1/connectors
```

Install GitHub or Slack from the dashboard to start OAuth. For Stripe, install an instance with a Stripe API key. The current connector operations include creating/listing GitHub issues, sending Slack messages, and listing Stripe charges.

## Current live-mode limitation

Live outbound API calls and GitHub/Slack OAuth installs are supported. Incoming GitHub webhooks can use the configured shared secret. **Live Slack and Stripe webhooks are not ready for production delivery:** Bridgekit currently has one shared webhook secret, while Slack and Stripe use provider-specific signing secrets. Do not rely on the live Slack or Stripe webhook routes until provider-specific webhook secret configuration is implemented.
