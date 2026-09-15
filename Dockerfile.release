ARG GORELEASER_IMAGE=goreleaser/goreleaser:v2.17.1@sha256:1098a0be4da1780f9616a85f4c5050447b53e3e74804d8017ec1e2bbb1fb697a
ARG GO_IMAGE=golang:1.26.8@sha256:9d2f36f06329b2a141b9db99ffa32765cf695ee57b813ca29e245e8670bcbfff

FROM ${GORELEASER_IMAGE} AS goreleaser

FROM ${GO_IMAGE} AS releasebootstrap
WORKDIR /bootstrap
COPY tools/releasebootstrap/main.go ./main.go
RUN CGO_ENABLED=0 go build -trimpath -o /release-helper-check ./main.go

FROM ${GO_IMAGE}

COPY --from=goreleaser /usr/bin/goreleaser /usr/local/bin/goreleaser
COPY --from=releasebootstrap /release-helper-check /usr/local/bin/release-helper-check

ARG TARGETARCH
ARG GH_VERSION
ARG GH_SHA256_AMD64
ARG GH_SHA256_ARM64
RUN set -eu; \
    test -n "$GH_VERSION"; \
    case "$TARGETARCH" in \
      amd64) checksum="$GH_SHA256_AMD64" ;; \
      arm64) checksum="$GH_SHA256_ARM64" ;; \
      *) echo "unsupported release tool architecture: $TARGETARCH" >&2; exit 1 ;; \
    esac; \
    test -n "$checksum"; \
    archive="gh_${GH_VERSION}_linux_${TARGETARCH}.tar.gz"; \
    curl -fLsS "https://github.com/cli/cli/releases/download/v${GH_VERSION}/${archive}" -o "/tmp/${archive}"; \
    printf '%s  %s\n' "$checksum" "/tmp/${archive}" | sha256sum -c -; \
    tar -xzf "/tmp/${archive}" --strip-components=2 -C /usr/local/bin \
      "gh_${GH_VERSION}_linux_${TARGETARCH}/bin/gh"; \
    rm "/tmp/${archive}"

ENV GOTOOLCHAIN=local GOFLAGS=-mod=readonly
WORKDIR /work
ENTRYPOINT ["goreleaser"]
CMD ["--help"]
