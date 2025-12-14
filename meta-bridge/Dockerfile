FROM golang:1-alpine3.22 AS builder

RUN apk add --no-cache git ca-certificates build-base

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -o /messenger-cli ./cmd/messenger-cli
RUN go build -o /cookie-converter ./cmd/cookie-converter
RUN go build -o /import-export ./cmd/import-export

FROM alpine:3.22

RUN apk add --no-cache ca-certificates

COPY --from=builder /messenger-cli /usr/bin/messenger-cli
COPY --from=builder /cookie-converter /usr/bin/cookie-converter
COPY --from=builder /import-export /usr/bin/import-export

VOLUME /data
WORKDIR /data

ENTRYPOINT ["/usr/bin/messenger-cli"]
