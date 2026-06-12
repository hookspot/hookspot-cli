GO_IMAGE := golang:1.23
RUN := docker run --rm -v "$(CURDIR)":/src -w /src -v hookspot-cli-gomod:/go/pkg/mod -v hookspot-cli-gocache:/root/.cache/go-build $(GO_IMAGE)

.PHONY: tidy build test vet run get

tidy:
	$(RUN) go mod tidy

build:
	$(RUN) go build ./...

test:
	$(RUN) go test ./...

vet:
	$(RUN) go vet ./...

run:
	$(RUN) go run . $(ARGS)

get:
	$(RUN) go get $(PKG)
