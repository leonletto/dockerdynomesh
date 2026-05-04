# syntax=docker/dockerfile:1
FROM golang:1.26.2-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/welcome ./cmd/welcome

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/welcome /welcome
USER nonroot:nonroot
ENTRYPOINT ["/welcome"]
