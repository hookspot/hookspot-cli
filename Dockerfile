# syntax=docker/dockerfile:1

FROM golang:1.23-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/hookspot-cli .

FROM alpine:3.20
RUN adduser -D -u 10001 hookspot
COPY --from=builder /out/hookspot-cli /usr/local/bin/hookspot-cli
USER hookspot
ENTRYPOINT ["hookspot-cli"]
