GO_VERSION := 1.26.5
GO_IMAGE := golang:$(GO_VERSION)
AIR_VERSION := v1.67.3
GORELEASER_VERSION := v2.17.1
GORELEASER_IMAGE := goreleaser/goreleaser:$(GORELEASER_VERSION)
RUN := docker run --rm -v "$(CURDIR)":/src -w /src -v hookspot-gomod:/go/pkg/mod -v hookspot-gocache:/root/.cache/go-build $(GO_IMAGE)
GORELEASER_RUN := docker run --rm -v "$(CURDIR)":/src -w /src -v hookspot-gomod:/go/pkg/mod -v hookspot-gocache:/root/.cache/go-build -e GITHUB_TOKEN -e SERVER_URL -e GORELEASER_CURRENT_TAG $(GORELEASER_IMAGE)
LDFLAGS = -X hookspot/cmd.serverURL=$(SERVER_URL)
ARGS ?=
DEV_ARGS ?= $(if $(ARGS),$(ARGS),listen)
STAGE_RELEASE_TAG = v0.0.$(shell git rev-list --count HEAD)-stage.g$(shell git rev-parse --short=7 HEAD)

.PHONY: tidy build test vet run get dev release-check stage-release

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
#   HOOKSPOT_ORGANIZATION_SLUG=acme HOOKSPOT_PROJECT_SLUG=payments HOOKSPOT_CLI_KEY=xxx make dev
# Override the command run on reload with ARGS, e.g. make dev ARGS="listen --help".
dev:
	docker compose down --remove-orphans && docker compose run --rm dev go run github.com/air-verse/air@$(AIR_VERSION) -- $(DEV_ARGS)

release-check:
	$(GORELEASER_RUN) check

# Build and publish a GitHub prerelease from the stage branch. The tag is pushed
# first because GoReleaser publishes releases for existing Git tags.
stage-release:
ifndef SERVER_URL
	$(error SERVER_URL is required, e.g. make stage-release SERVER_URL=https://api.hookspot.dev)
endif
ifndef GITHUB_TOKEN
	$(error GITHUB_TOKEN is required and must have permission to publish releases)
endif
	@test "$$(git branch --show-current)" = "stage" || (echo "stage-release must be run from the stage branch" >&2; exit 1)
	@test -z "$$(git status --porcelain)" || (echo "stage-release requires a clean working tree" >&2; exit 1)
	git fetch origin --tags
	@test "$$(git rev-parse HEAD)" = "$$(git rev-parse origin/stage)" || (echo "stage must match origin/stage before releasing" >&2; exit 1)
	$(GORELEASER_RUN) check
	@tag_commit="$$(git rev-list -n 1 "$(STAGE_RELEASE_TAG)" 2>/dev/null || true)"; \
	if [ -n "$$tag_commit" ]; then \
		test "$$tag_commit" = "$$(git rev-parse HEAD)" || (echo "$(STAGE_RELEASE_TAG) already points to another commit" >&2; exit 1); \
	else \
		git tag "$(STAGE_RELEASE_TAG)"; \
	fi
	git push origin "refs/tags/$(STAGE_RELEASE_TAG)"
	GORELEASER_CURRENT_TAG="$(STAGE_RELEASE_TAG)" $(GORELEASER_RUN) release --clean
