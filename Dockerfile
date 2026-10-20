# syntax=docker/dockerfile:1
# check=skip=InvalidDefaultArgInFrom

# make passes release/toolchain.env's pin.
ARG RUNTIME_IMAGE

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
