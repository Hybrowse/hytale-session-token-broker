FROM golang:1.27-alpine AS build

WORKDIR /src

ARG TARGETOS
ARG TARGETARCH

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -buildvcs=false -trimpath -ldflags "-s -w" -o /out/hytale-session-token-broker ./cmd/hytale-session-token-broker

FROM alpine:3.23

RUN apk add --no-cache ca-certificates

RUN adduser -D -H -s /sbin/nologin app

WORKDIR /app

RUN mkdir -p /app/data && chown -R app:app /app

COPY config.yaml /app/config.yaml

COPY --from=build /out/hytale-session-token-broker /usr/local/bin/hytale-session-token-broker

RUN mkdir -p /usr/share/licenses/hybrowse-hytale-session-token-broker
COPY LICENSE NOTICE LICENSING.md COMMERCIAL_LICENSE.md TRADEMARKS.md /usr/share/licenses/hybrowse-hytale-session-token-broker/

EXPOSE 8080

USER app

ENTRYPOINT ["/usr/local/bin/hytale-session-token-broker"]
CMD ["-config", "/app/config.yaml"]
