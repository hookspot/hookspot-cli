# syntax=docker/dockerfile:1

ARG GO_IMAGE=golang:1.26.9-alpine3.24@sha256:3082400e369fa24d5fc60bca20edab3f6d604e0c5a690ec66b295eff4dd87ade
ARG RUNTIME_IMAGE=alpine:3.24.1@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b

FROM ${GO_IMAGE} AS builder
ARG SERVER_URL
ARG VERSION=dev
ARG COMMIT=unknown
ARG SOURCE_DATE=unknown
ARG BUILD_KIND=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN test -n "$SERVER_URL" || (echo "SERVER_URL build arg is required, e.g. --build-arg SERVER_URL=https://api.example.invalid" >&2 && exit 1)
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X hookspot/cmd.version=${VERSION} -X hookspot/cmd.serverURL=${SERVER_URL} -X hookspot/cmd.commit=${COMMIT} -X hookspot/cmd.sourceDate=${SOURCE_DATE} -X hookspot/cmd.buildKind=${BUILD_KIND}" -o /out/hookspot .

FROM ${RUNTIME_IMAGE} AS runtime
RUN apk add --no-cache ca-certificates \
    && adduser -D -u 10001 hookspot \
    && mkdir -p /home/hookspot/.config/hookspot \
    && chown -R hookspot:hookspot /home/hookspot/.config \
    && chmod 0700 /home/hookspot/.config /home/hookspot/.config/hookspot
USER hookspot
ENTRYPOINT ["hookspot"]

# The published image ships the binaries GoReleaser staged for the npm package,
# so every channel installs the same build.
FROM runtime AS release
ARG TARGETOS
ARG TARGETARCH
COPY npm/binaries/${TARGETOS}-${TARGETARCH}/hookspot /usr/local/bin/hookspot

# Default target, used by docker-compose.yml: built from source for any SERVER_URL.
FROM runtime
COPY --from=builder /out/hookspot /usr/local/bin/hookspot
