# matrix-xplora

A Matrix bridge for the [Xplora](https://www.xplora.eu/) children's smartwatch platform, built with [mautrix-go](https://github.com/maunium/mautrix-go).

Bridges the Xplora parent app chat to Matrix, so you can message your child's watch from any Matrix client. One Matrix room per watch.

## Status

**Alpha / work in progress.** Text messaging and read receipts work. FCM push notifications require APK constant extraction (see below) — until then the bridge falls back to 30-second polling.

## Features

- Send and receive text messages to/from Xplora watches
- Read receipt sync
- Auto-creates one Matrix room per linked child watch
- FCM push notifications (requires APK setup — see below)
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

This produces `./mautrix-xplora` (the bridge) and `./fcm-probe` (FCM diagnostic tool).

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

## FCM push notifications (optional but recommended)

Without FCM, the bridge polls every 30 seconds. With FCM, messages arrive within seconds.

FCM requires three constants extracted from the Xplora Android APK. See [Extracting FCM constants](#extracting-fcm-constants) below.

### Using fcm-probe to discover the payload format

After filling in the constants, run `fcm-probe` to register a virtual device and observe raw FCM payloads:

```sh
# 1. Register a virtual Android device with Xplora's FCM project
./fcm-probe register \
  --token  <your-xplora-bearer-token> \
  --uid    <your-xplora-user-id> \
  --dir    ./fcm-probe-session

# 2. Connect and wait for a push
./fcm-probe listen --dir ./fcm-probe-session
# Then send a message from a child's watch — the payload prints to stdout.
```

The printed JSON reveals the field names needed to update `parseDataMessage` in `internal/fcm/client.go`.

## Extracting FCM constants

You need three values from the Xplora Android APK. Download the APK from your device or a trusted APK mirror.

### Install tools

```sh
# apktool — disassembles the APK
brew install apktool        # macOS
apt install apktool         # Debian/Ubuntu

# apksigner — reads the signing certificate
# Ships with Android SDK build-tools, or:
apt install apksigner
```

### Extract the values

```sh
apktool d xplora.apk -o xplora-decompiled

# 1. Sender ID (project_number)
grep -r "project_number\|mobilesdk_app_id\|gcm_defaultSenderId" xplora-decompiled/

# 2. App package name
grep "package=" xplora-decompiled/AndroidManifest.xml | head -1

# 3. APK certificate SHA-1
apksigner verify --print-certs xplora.apk | grep "SHA-1"
```

### Fill in constants.go

Open `internal/fcm/constants.go`. The values are XOR-encoded with key `0x5A` to avoid plain-text storage. Encode your values:

```go
// Quick encoder — run once, paste the result into constants.go
package main

import "fmt"

func main() {
    key := byte(0x5A)
    for _, s := range []string{"YOUR_SENDER_ID", "YOUR_CERT_SHA1", "com.your.package"} {
        b := make([]byte, len(s))
        for i, c := range s { b[i] = byte(c) ^ key }
        fmt.Printf("%#v\n", b)
    }
}
```

Then in `constants.go`, replace the empty `init()` assignments with `decode([]byte{...}, xk)` calls using your encoded byte slices.

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
