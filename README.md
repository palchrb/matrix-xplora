# matrix-xplora  

A Matrix bridge for the [Xplora](https://www.xplora.eu/) children's smartwatch platform, built with [mautrix-go](https://github.com/maunium/mautrix-go).

Bridges the Xplora parent app chat to Matrix, so you can message your child's watch from any Matrix client. One Matrix room per watch.

Would like to highlight that I have picked pieces from https://github.com/Ludy87/pyxplora_api, and added a bit more here and there. It receives FCM push messages from xplora by registering as an android client.

## Status

**Alpha / work in progress.**

## Features

**Messaging**
- Send and receive text messages
- Send and receive images
- Send and receive voice messages
- Send and receive emoticons/stickers
- Single emoji in Matrix maps to the matching Xplora sticker when available
- Read receipt sync (Matrix → watch)

**Infrastructure**
- FCM push notifications (messages arrive within seconds)
- Polling fallback when FCM is unavailable
- Automatic token refresh with proactive expiry detection
- Session health check every 10 minutes — notifies in Matrix if session is invalidated (e.g. another device logged in)
- Auto-creates one Matrix room per linked child watch
- Child watch avatars synced to Matrix

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
