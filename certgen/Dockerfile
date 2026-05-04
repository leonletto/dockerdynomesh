# syntax=docker/dockerfile:1
FROM golang:1.26.2-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/certgen ./cmd/certgen

# mkcert is a single binary; pull it from upstream releases.
FROM alpine:3.20 AS mkcert
ARG MKCERT_VERSION=v1.4.4
RUN apk add --no-cache curl ca-certificates && \
    arch=$(uname -m | sed -e 's/x86_64/amd64/' -e 's/aarch64/arm64/') && \
    curl -fsSL "https://github.com/FiloSottile/mkcert/releases/download/${MKCERT_VERSION}/mkcert-${MKCERT_VERSION}-linux-${arch}" -o /usr/local/bin/mkcert && \
    chmod +x /usr/local/bin/mkcert

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=mkcert /usr/local/bin/mkcert /usr/local/bin/mkcert
COPY --from=build /out/certgen /certgen
ENTRYPOINT ["/certgen"]
