.PHONY: test build images up down clean

GO ?= go
DOCKER ?= docker

test:
	$(GO) test ./...

build:
	$(GO) build -o bin/discoverer ./cmd/discoverer
	$(GO) build -o bin/certgen ./cmd/certgen
	$(GO) build -o bin/welcome ./cmd/welcome

images:
	$(DOCKER) compose build

up:
	./bootstrap.sh

down:
	$(DOCKER) compose down

clean:
	rm -rf bin/
	$(GO) clean -testcache
