# syntax=docker/dockerfile:1
FROM golang:1.26.2-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/discoverer ./cmd/discoverer

FROM gcr.io/distroless/static-debian12
# dockerdynomesh-ggu: runs as distroless nonroot (UID 65532).
# The docker-socket-proxy sidecar (tcp://socket-proxy:2375) exposes only
# read-only EVENTS/CONTAINERS/NETWORKS/INFO endpoints, so the discoverer
# no longer needs access to /var/run/docker.sock and can drop root.
USER nonroot
COPY --from=build /out/discoverer /discoverer
ENTRYPOINT ["/discoverer"]
