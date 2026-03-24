# matrix-xplora

A Matrix bridge for the [Xplora](https://www.xplora.eu/) children's smartwatch platform, built with [mautrix-go](https://github.com/maunium/mautrix-go).

Bridges the Xplora parent app chat to Matrix, so you can message your child's watch from any Matrix client. One Matrix room per watch.

## Status

**Alpha / work in progress.** Text messaging, read receipts, and FCM push notifications work.

## Features

- Send and receive text messages to/from Xplora watches
- Read receipt sync
- Auto-creates one Matrix room per linked child watch
- FCM push notifications (messages arrive within seconds)
- Polling fallback when FCM is unavailable

## Requirements

- Go 1.24+
- A Matrix homeserver (Synapse, Conduit, etc.)
- An Xplora parent account with at least one linked watch

## Setup

### 1. Build

```sh
./build.sh
```  

### 2. Generate config

```sh
./mautrix-xplora -g -c config.yaml
```

Edit `config.yaml` — at minimum set:
- `homeserver.address` and `homeserver.domain`
- `appservice.address` (where Synapse can reach this process)
- `bridge.permissions` (map your Matrix user to `admin`)

### 3. Register the appservice

```sh
./mautrix-xplora -g -r registration.yaml -c config.yaml
```

Add `registration.yaml` to your Synapse `app_service_config_files` and restart Synapse.

### 4. Run

```sh
./mautrix-xplora -c config.yaml
```

### 5. Log in

In any Matrix client, open a DM with the bridge bot (configured in `config.yaml` as `bridge.bot`), then send:

```
login
```

Follow the prompts to enter your country code, phone number, and Xplora password.

## Docker

```sh
docker compose up -d
```

See `docker-compose.yml`. Mount a `./data` volume for persistent credentials.

## Project structure

```
cmd/
  mautrix-xplora/   Bridge entry point
  fcm-probe/        Standalone FCM diagnostic tool
internal/
  fcm/              FCM/GCM registration and MCS listener
  xplora/           Xplora GraphQL API client
pkg/
  connector/        mautrix-go bridge connector (login, portal, message handling)
```

## License

AGPLv3 — see [LICENSE](LICENSE).
