GO_VERSION := 1.26.5
GO_IMAGE := golang:$(GO_VERSION)
AIR_VERSION := v1.67.3
RUN := docker run --rm -v "$(CURDIR)":/src -w /src -v hookspot-gomod:/go/pkg/mod -v hookspot-gocache:/root/.cache/go-build $(GO_IMAGE)
LDFLAGS = -X hookspot/cmd.serverURL=$(SERVER_URL)
ARGS ?=
DEV_ARGS ?= $(if $(ARGS),$(ARGS),listen)

.PHONY: tidy build test vet run get dev

tidy:
	$(RUN) go mod tidy

build:
ifndef SERVER_URL
	$(error SERVER_URL is required, e.g. make build SERVER_URL=https://api.hookspot.dev)
endif
	$(RUN) go build -ldflags "$(LDFLAGS)" ./...

test:
	$(RUN) go test ./...

vet:
	$(RUN) go vet ./...

run:
ifndef SERVER_URL
	$(error SERVER_URL is required, e.g. make run SERVER_URL=https://api.hookspot.dev ARGS="version")
endif
	$(RUN) go run -ldflags "$(LDFLAGS)" . $(ARGS)

get:
	$(RUN) go get $(PKG)

# Live-reload dev loop: rebuilds and restarts the command on every file change.
# Pass credentials inline, e.g.
#   HOOKSPOT_PROJECT=asd HOOKSPOT_CLI_KEY=xxx make dev
# Override the command run on reload with ARGS, e.g. make dev ARGS="listen --help".
dev:
	docker compose down --remove-orphans && docker compose run --rm dev go run github.com/air-verse/air@$(AIR_VERSION) -- $(DEV_ARGS)
