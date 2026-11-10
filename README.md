# Bridgekit

A self-hosted gateway for calling third-party APIs through a consistent, locally operated interface. Bridgekit is being built around real provider APIs; it does not start a mock provider or pretend provider calls succeeded.

## Status

Early implementation. The initial version provides configuration, an HTTP health endpoint, and provider-backed connector foundations. Provider credentials and OAuth app configuration are supplied by the operator.

## Run

```sh
cp .env.example .env
# configure the values you need
go run ./cmd/bridgekit
```

The gateway listens on `:8080` by default. `GET /healthz` reports readiness.

## Security

Set a random 32-byte base64 `BRIDGEKIT_ENCRYPTION_KEY` before storing credentials. Do not expose the gateway publicly without adding an authentication layer and TLS termination.
