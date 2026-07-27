# syntax=docker/dockerfile:1

FROM golang:1.26.5-alpine3.24 AS builder
ARG SERVER_URL
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN test -n "$SERVER_URL" || (echo "SERVER_URL build arg is required, e.g. --build-arg SERVER_URL=https://api.hookspot.dev" >&2 && exit 1)
RUN CGO_ENABLED=0 go build -ldflags "-X hookspot/cmd.serverURL=${SERVER_URL}" -o /out/hookspot .

FROM alpine:3.24.1
RUN adduser -D -u 10001 hookspot
COPY --from=builder /out/hookspot /usr/local/bin/hookspot
USER hookspot
# ENTRYPOINT ["hookspot"]
