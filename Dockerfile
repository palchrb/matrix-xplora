FROM golang:1-alpine3.23 AS builder

RUN apk add --no-cache git ca-certificates build-base su-exec

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN sed -i 's/\r$//' ./build.sh \
 && chmod +x ./build.sh \
 && ./build.sh

FROM alpine:3.23

ENV UID=1337 \
    GID=1337

RUN apk add --no-cache su-exec ca-certificates bash yq-go

COPY --from=builder /build/mautrix-xplora /usr/bin/mautrix-xplora
COPY --from=builder /build/docker-run.sh /docker-run.sh

RUN sed -i 's/\r$//' /docker-run.sh \
 && chmod +x /docker-run.sh

VOLUME /data

CMD ["/docker-run.sh"]
