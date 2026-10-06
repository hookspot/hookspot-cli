include release/toolchain.env

# check's prerequisites run in order, even under -j: release-snapshot needs
# release-tools' image, and release-image the binaries release-snapshot stages.
.NOTPARALLEL:

# Go and golangci-lint run on the host at mise.toml's versions; DOCKER=1 runs
# them in release/toolchain.env's images instead. run and dev always use Docker.
DOCKER ?=
# A worktree's .git points into the main checkout's, which Git, GoReleaser and
# Go's VCS stamping read inside the container.
GIT_COMMON_DIR := $(shell git rev-parse --path-format=absolute --git-common-dir 2>/dev/null)
DOCKER_RUN := docker run --rm -v "$(CURDIR)":/src -w /src $(if $(GIT_COMMON_DIR),-v "$(GIT_COMMON_DIR)":"$(GIT_COMMON_DIR)") -v hookspot-gomod:/go/pkg/mod -v hookspot-gocache:/root/.cache/go-build
GO := $(if $(filter 1,$(DOCKER)),$(DOCKER_RUN) -e GOOS -e GOARCH $(GO_IMAGE) )go
LINT := $(if $(filter 1,$(DOCKER)),$(DOCKER_RUN) -e GOOS -v hookspot-golangci-cache:/root/.cache/golangci-lint $(GOLANGCI_LINT_IMAGE) )golangci-lint
RUN_ENV := -e HOOKSPOT_CLI_KEY -e HOOKSPOT_ORGANIZATION_SLUG -e HOOKSPOT_PROJECT_SLUG -e HOOKSPOT_CONFIG_FILE
RELEASE_IMAGE := hookspot-release:local
RELEASE_RUN := $(DOCKER_RUN) $(RELEASE_IMAGE)
IMAGE := hookspot/cli
IMAGE_BUILD := docker buildx build --platform linux/amd64,linux/arm64 --target release --build-arg RUNTIME_IMAGE=$(RUNTIME_IMAGE)
DEV_CONFIG_VOLUME ?= hookspot-dev-config
COMMIT ?= $(shell git rev-parse HEAD)
SOURCE_DATE ?= $(shell git show -s --format=%cI HEAD)
# Only GoReleaser builds anything other than a dev binary.
LDFLAGS = -X hookspot/cmd.version=dev -X hookspot/cmd.serverURL=$(SERVER_URL) -X hookspot/cmd.commit=$(COMMIT) -X hookspot/cmd.sourceDate=$(SOURCE_DATE) -X hookspot/cmd.buildKind=dev
# test, golden, fmt and lint pass ARGS to go or golangci-lint unexpanded, so a
# -run pattern keeps its $; run and dev pass it to the CLI.
ARGS ?=
# The packages ARGS names, such as . or ./internal/tui; test and golden
# default to theirs without any.
ARGS_PKGS = $(filter . ./% hookspot%,$(value ARGS))
DEV_ARGS ?= $(if $(ARGS),$(ARGS),listen)
# The packages whose tests compare output with testdata/*.golden files.
GOLDEN_PKGS = $(shell $(GO) list -f '{{range .TestImports}}{{if eq . "github.com/charmbracelet/x/exp/golden"}}{{$$.ImportPath}}{{end}}{{end}}' ./...)
E2E_OUTPUT ?= tmp/e2e/hookspot

.PHONY: tidy build test golden vet fmt lint toolchain-check check e2e-build run get dev npm-test release-tools release-check release-snapshot local-build release-publish release-image release-image-publish

tidy:
	$(GO) mod tidy

build:
ifndef SERVER_URL
	$(error SERVER_URL is required, e.g. make build SERVER_URL=https://api.example.invalid)
endif
	$(GO) build -ldflags "$(LDFLAGS)" ./...

# e.g. make test ARGS='-run TestStatus ./internal/cards'
test:
	$(GO) test $(value ARGS) $(if $(ARGS_PKGS),,./...)

# Rewrites the golden files of the tests ARGS names (default: all of them);
# review the diff. e.g. make golden ARGS='-run TestStatus ./internal/cards'
golden:
	$(GO) test $(value ARGS) $(if $(ARGS_PKGS),,$(GOLDEN_PKGS)) -update

vet:
	$(GO) vet ./...

fmt:
	$(LINT) fmt $(value ARGS)

# Includes the gofmt check. Each GOOS has its own config and terminal code.
lint:
	for goos in darwin linux windows; do GOOS=$$goos $(LINT) run $(value ARGS) || exit 1; done

# go.mod, mise.toml and release/toolchain.env's images pin the same Go, and
# mise.toml and the image the same golangci-lint.
toolchain-check:
	@go=$$(sed -n 's/^go = "\(.*\)"$$/\1/p' mise.toml); lint=$$(sed -n 's/^golangci-lint = "\(.*\)"$$/\1/p' mise.toml); \
	grep -qx "go $$go" go.mod \
		&& grep -q "^GO_IMAGE=golang:$$go@" release/toolchain.env \
		&& grep -q "^GO_ALPINE_IMAGE=golang:$$go-" release/toolchain.env \
		&& grep -q "^GOLANGCI_LINT_IMAGE=golangci/golangci-lint:v$$lint@" release/toolchain.env \
		|| { echo "go.mod, mise.toml and release/toolchain.env pin different Go or golangci-lint versions" >&2; exit 1; }

# Everything CI's ci job runs. release-snapshot needs a clean tree.
check: toolchain-check lint vet test release-tools release-check release-snapshot release-image npm-test
	scripts/smoke_test.sh
	cd npm && npm pack --dry-run

# A host binary for end-to-end checks against a local Hookspot, e.g.
#   make e2e-build VERSION=1.2.0 SERVER_URL=https://hookspot.localhost:4443 UPDATE_API_URL=http://127.0.0.1:8080
# VERSION is what it reports and compares with the latest release the GitHub
# API at UPDATE_API_URL (default GitHub's) serves; the later -X wins. With
# DOCKER=1 it builds for LOCAL_GOOS/LOCAL_GOARCH.
e2e-build: LDFLAGS += -X hookspot/cmd.version=$(VERSION) $(if $(UPDATE_API_URL),-X hookspot/cmd.githubAPIBaseURL=$(UPDATE_API_URL))
e2e-build:
ifndef VERSION
	$(error VERSION is required, e.g. make e2e-build VERSION=1.2.0 SERVER_URL=https://hookspot.localhost:4443)
endif
ifndef SERVER_URL
	$(error SERVER_URL is required, e.g. make e2e-build VERSION=1.2.0 SERVER_URL=https://hookspot.localhost:4443)
endif
	$(if $(filter 1,$(DOCKER)),GOOS=$(LOCAL_GOOS) GOARCH=$(LOCAL_GOARCH) )$(GO) build -o $(E2E_OUTPUT) -ldflags "$(LDFLAGS)" .

run:
ifndef SERVER_URL
	$(error SERVER_URL is required, e.g. make run SERVER_URL=https://api.example.invalid ARGS="version")
endif
	@tty_flag=; if test -t 0 && test -t 1; then tty_flag=-t; fi; \
	$(DOCKER_RUN) -i $$tty_flag -v "$(DEV_CONFIG_VOLUME)":/root/.config/hookspot $(RUN_ENV) $(GO_IMAGE) go run -ldflags "$(LDFLAGS)" . $(ARGS)

get:
	$(GO) get $(PKG)

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
	$(RELEASE_RUN) check; status=$$?; test $$status -eq 0 || test $$status -eq 2 || exit $$status

# The build embeds VCS metadata (-buildvcs=true) from the mounted checkout, so a
# dirty tree would change the artifacts.
define require-clean-tree
	@test -z "$$(git status --porcelain)" || { echo "$(1) requires a clean tree; commit or stash your changes first" >&2; exit 1; }
endef

release-snapshot:
	$(call require-clean-tree,release-snapshot)
	$(RELEASE_RUN) release --snapshot --clean --skip=publish

# Unpublished binary for a non-prod server (host macOS by default); see
# scripts/build-local.fish. The tmpfs keeps release-snapshot's dist/, and the
# skipped hooks only stage the prod npm package and build-info.json.
LOCAL_GOOS ?= darwin
LOCAL_GOARCH ?= $(shell uname -m | sed s/x86_64/amd64/)
LOCAL_OUTPUT ?= tmp/dist/hookspot_$(CONFIG_PREFIX)

local-build: release-tools
	mkdir -p $(dir $(LOCAL_OUTPUT))
	$(DOCKER_RUN) --tmpfs /src/dist -e SERVER_URL="$(SERVER_URL)" -e CONFIG_PREFIX=$(CONFIG_PREFIX) -e GOOS=$(LOCAL_GOOS) -e GOARCH=$(LOCAL_GOARCH) $(RELEASE_IMAGE) build --snapshot --single-target --skip=before,post-hooks --output $(LOCAL_OUTPUT)

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

# Multi-arch image of the Linux binaries that release-snapshot or
# release-publish staged in npm/binaries/. Builds without pushing.
release-image:
	$(IMAGE_BUILD) .

# CI only: pushes the image. Pre-release versions (1.2.3-rc.1) never move `latest`.
release-image-publish:
ifndef VERSION
	$(error VERSION is required, e.g. make release-image-publish VERSION=1.2.3)
endif
	$(IMAGE_BUILD) -t $(IMAGE):$(VERSION) $(if $(findstring -,$(VERSION)),,-t $(IMAGE):latest) --push .
