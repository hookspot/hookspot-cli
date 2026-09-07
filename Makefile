TOOLCHAIN_VALUE = ./scripts/release.sh _toolchain-value
unexport ENV REF TAG DIST NOTES_FILE FROM_STAGE_TAG STAGE_ACCEPTANCE
GO_VERSION = $(shell $(TOOLCHAIN_VALUE) GO_VERSION)
GO_IMAGE = $(shell $(TOOLCHAIN_VALUE) GO_IMAGE)
AIR_VERSION = $(shell $(TOOLCHAIN_VALUE) AIR_VERSION)

DOCKER_RUN := docker run --rm -v "$(CURDIR)":/src -w /src -v hookspot-gomod:/go/pkg/mod -v hookspot-gocache:/root/.cache/go-build
RUN := $(DOCKER_RUN) $(GO_IMAGE)
RUN_ENV := -e HOOKSPOT_DEV_CLI_KEY -e HOOKSPOT_DEV_ORGANIZATION_SLUG -e HOOKSPOT_DEV_PROJECT_SLUG -e HOOKSPOT_DEV_CONFIG_FILE
DEV_CONFIG_VOLUME ?= hookspot-dev-config
VERSION ?= dev
BUILD_ENVIRONMENT ?= dev
COMMIT ?= $(shell git rev-parse HEAD)
SOURCE_DATE ?= $(shell git show -s --format=%cI HEAD)
BUILD_KIND ?= dev
LDFLAGS = -X hookspot/cmd.version=$(VERSION) -X hookspot/cmd.serverURL=$(SERVER_URL) -X hookspot/cmd.buildEnvironment=$(BUILD_ENVIRONMENT) -X hookspot/cmd.commit=$(COMMIT) -X hookspot/cmd.sourceDate=$(SOURCE_DATE) -X hookspot/cmd.buildKind=$(BUILD_KIND)
ARGS ?=
DEV_ARGS ?= $(if $(ARGS),$(ARGS),listen)

.PHONY: tidy build test vet run get dev release-tools release-check release-snapshot release-build release-verify release-status release-resume stage-release prod-release

tidy:
	@./scripts/release.sh _require-clean-tree
	$(RUN) go mod tidy

build:
ifndef SERVER_URL
	$(error SERVER_URL is required, e.g. make build SERVER_URL=https://api.example.invalid)
endif
	@./scripts/release.sh _require-clean-tree
	$(RUN) go build -ldflags "$(LDFLAGS)" ./...

test:
	$(RUN) go test ./...

vet:
	$(RUN) go vet ./...

run:
ifndef SERVER_URL
	$(error SERVER_URL is required, e.g. make run SERVER_URL=https://api.example.invalid ARGS="version")
endif
	@tty_flag=; if test -t 0 && test -t 1; then tty_flag=-t; fi; \
	$(DOCKER_RUN) -i $$tty_flag -v "$(DEV_CONFIG_VOLUME)":/root/.config/hookspot $(RUN_ENV) $(GO_IMAGE) go run -ldflags "$(LDFLAGS)" . $(ARGS)

get:
	@./scripts/release.sh _require-clean-tree
	$(RUN) go get $(PKG)

# Live-reload dev loop: rebuilds and restarts the command on every file change.
# Pass credentials inline, e.g.
#   HOOKSPOT_DEV_ORGANIZATION_SLUG=acme HOOKSPOT_DEV_PROJECT_SLUG=payments HOOKSPOT_DEV_CLI_KEY=xxx make dev
# Override the command run on reload with ARGS, e.g. make dev ARGS="listen --help".
dev:
	COMMIT="$(COMMIT)" SOURCE_DATE="$(SOURCE_DATE)" docker compose --env-file release/toolchain.env run --rm dev go run github.com/air-verse/air@$(AIR_VERSION) -- $(DEV_ARGS)

release-check: export RELEASE_MAKE_ENV := $(value ENV)
release-check:
	@./scripts/release.sh _make check

release-tools:
	@./scripts/release.sh tools

release-snapshot: export RELEASE_MAKE_ENV := $(value ENV)
release-snapshot: export RELEASE_MAKE_REF := $(value REF)
release-snapshot:
	@./scripts/release.sh _make snapshot

release-build: export RELEASE_MAKE_ENV := $(value ENV)
release-build: export RELEASE_MAKE_TAG := $(value TAG)
release-build:
	@./scripts/release.sh _make build

release-verify: export RELEASE_MAKE_ENV := $(value ENV)
release-verify: export RELEASE_MAKE_DIST := $(value DIST)
release-verify:
	@./scripts/release.sh _make verify

release-status: export RELEASE_MAKE_ENV := $(value ENV)
release-status: export RELEASE_MAKE_TAG := $(value TAG)
release-status: export RELEASE_MAKE_DIST := $(value DIST)
release-status:
	@./scripts/release.sh _make status

release-resume: export RELEASE_MAKE_ENV := $(value ENV)
release-resume: export RELEASE_MAKE_TAG := $(value TAG)
release-resume: export RELEASE_MAKE_DIST := $(value DIST)
release-resume:
	@./scripts/release.sh _make resume

stage-release: export RELEASE_MAKE_ENV := $(value ENV)
stage-release: export RELEASE_MAKE_TAG := $(value TAG)
stage-release: export RELEASE_MAKE_REF := $(value REF)
stage-release: export RELEASE_MAKE_NOTES_FILE := $(value NOTES_FILE)
stage-release: export RELEASE_MAKE_DIST := $(value DIST)
stage-release:
	@./scripts/release.sh _make stage-release

prod-release: export RELEASE_MAKE_ENV := $(value ENV)
prod-release: export RELEASE_MAKE_TAG := $(value TAG)
prod-release: export RELEASE_MAKE_REF := $(value REF)
prod-release: export RELEASE_MAKE_NOTES_FILE := $(value NOTES_FILE)
prod-release: export RELEASE_MAKE_FROM_STAGE_TAG := $(value FROM_STAGE_TAG)
prod-release: export RELEASE_MAKE_STAGE_ACCEPTANCE := $(value STAGE_ACCEPTANCE)
prod-release: export RELEASE_MAKE_DIST := $(value DIST)
prod-release:
	@./scripts/release.sh _make prod-release
