# syntax=docker/dockerfile:1

ARG RUNTIME_IMAGE=alpine:3.24.1@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b

# The published image ships the binaries GoReleaser staged for the npm package,
# so every channel installs the same build.
FROM ${RUNTIME_IMAGE}
RUN apk add --no-cache ca-certificates \
    && adduser -D -u 10001 hookspot \
    && mkdir -p /home/hookspot/.config/hookspot \
    && chown -R hookspot:hookspot /home/hookspot/.config \
    && chmod 0700 /home/hookspot/.config /home/hookspot/.config/hookspot
ARG TARGETOS
ARG TARGETARCH
COPY npm/binaries/${TARGETOS}-${TARGETARCH}/hookspot /usr/local/bin/hookspot
USER hookspot
ENTRYPOINT ["hookspot"]
