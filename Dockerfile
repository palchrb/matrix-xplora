FROM golang:1-alpine3.23 AS builder

RUN apk add --no-cache git ca-certificates build-base su-exec

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN sed -i 's/\r$//' ./build.sh \
 && chmod +x ./build.sh \
 && ./build.sh

# Static ffmpeg binary with AMR-NB (libopencore_amrnb) + OGG Opus support.
# Alpine's stock ffmpeg is NOT compiled with libopencore-amrnb, so we pull
# a pre-compiled static binary from mwader/static-ffmpeg instead.
FROM mwader/static-ffmpeg:7.1 AS ffmpeg-static

FROM alpine:3.23

ENV UID=1337 \
    GID=1337

RUN apk add --no-cache su-exec ca-certificates bash yq-go

COPY --from=ffmpeg-static /ffmpeg /usr/local/bin/ffmpeg
COPY --from=builder /build/mautrix-xplora /usr/bin/mautrix-xplora
COPY --from=builder /build/fcm-probe /usr/bin/fcm-probe
COPY --from=builder /build/docker-run.sh /docker-run.sh

RUN sed -i 's/\r$//' /docker-run.sh \
 && chmod +x /docker-run.sh

VOLUME /data

CMD ["/docker-run.sh"]
