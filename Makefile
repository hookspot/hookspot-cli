include release/toolchain.env

DOCKER_RUN := docker run --rm -v "$(CURDIR)":/src -w /src -v hookspot-gomod:/go/pkg/mod -v hookspot-gocache:/root/.cache/go-build
RUN := $(DOCKER_RUN) $(GO_IMAGE)
RUN_ENV := -e HOOKSPOT_CLI_KEY -e HOOKSPOT_ORGANIZATION_SLUG -e HOOKSPOT_PROJECT_SLUG -e HOOKSPOT_CONFIG_FILE
RELEASE_IMAGE := hookspot-release:local
RELEASE_RUN := $(DOCKER_RUN) $(RELEASE_IMAGE)
DEV_CONFIG_VOLUME ?= hookspot-dev-config
COMMIT ?= $(shell git rev-parse HEAD)
SOURCE_DATE ?= $(shell git show -s --format=%cI HEAD)
# Only GoReleaser builds anything other than a dev binary.
LDFLAGS = -X hookspot/cmd.version=dev -X hookspot/cmd.serverURL=$(SERVER_URL) -X hookspot/cmd.buildEnvironment=dev -X hookspot/cmd.commit=$(COMMIT) -X hookspot/cmd.sourceDate=$(SOURCE_DATE) -X hookspot/cmd.buildKind=dev
ARGS ?=
DEV_ARGS ?= $(if $(ARGS),$(ARGS),listen)

.PHONY: tidy build test vet run get dev npm-test release-tools release-check release-snapshot release-publish

tidy:
	$(RUN) go mod tidy

build:
ifndef SERVER_URL
	$(error SERVER_URL is required, e.g. make build SERVER_URL=https://api.example.invalid)
endif
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
	$(RUN) go get $(PKG)

# Live-reload dev loop: rebuilds and restarts the command on every file change.
# Pass credentials inline, e.g.
#   HOOKSPOT_ORGANIZATION_SLUG=acme HOOKSPOT_PROJECT_SLUG=payments HOOKSPOT_CLI_KEY=xxx make dev
# Override the command run on reload with ARGS, e.g. make dev ARGS="listen --help".
dev:
	COMMIT="$(COMMIT)" SOURCE_DATE="$(SOURCE_DATE)" docker compose --env-file release/toolchain.env run --rm dev go run github.com/air-verse/air@$(AIR_VERSION) -- $(DEV_ARGS)

# Host Node, not the Docker toolchain: the launcher has no dependencies.
npm-test:
	node --test 'npm/test/*.test.js'

# Locked GoReleaser-on-pinned-Go image shared by local snapshots and CI.
release-tools:
	docker build -f Dockerfile.release -t $(RELEASE_IMAGE) --build-arg GORELEASER_IMAGE=$(GORELEASER_IMAGE) --build-arg GO_IMAGE=$(GO_IMAGE) .

# Exit 2 is GoReleaser's deprecation notice for the `brews` section, which is
# used deliberately (formula, not cask) and still supported by the pinned version.
release-check:
	@$(RELEASE_RUN) check; status=$$?; test $$status -eq 0 || test $$status -eq 2 || exit $$status

# The build embeds VCS metadata (-buildvcs=true) from the mounted checkout, so a
# dirty tree would change the artifacts. npm/binaries/ (filled by post-hooks,
# root-owned on Linux hosts) is cleared by a before hook inside the container.
define require-clean-tree
	@test -z "$$(git status --porcelain)" || { echo "$(1) requires a clean tree; commit or stash your changes first" >&2; exit 1; }
endef

release-snapshot:
	$(call require-clean-tree,release-snapshot)
	$(RELEASE_RUN) release --snapshot --clean --skip=publish

# CI only: publishes the GitHub Release and the Homebrew formula.
release-publish:
ifndef GITHUB_TOKEN
	$(error GITHUB_TOKEN is required to publish the GitHub Release)
endif
ifndef HOMEBREW_TAP_TOKEN
	$(error HOMEBREW_TAP_TOKEN is required to push the Homebrew formula)
endif
	$(call require-clean-tree,release-publish)
	$(DOCKER_RUN) -e GITHUB_TOKEN -e HOMEBREW_TAP_TOKEN $(RELEASE_IMAGE) release --clean
