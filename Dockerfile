FROM golang:1.25 AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/stackrd ./cmd/stackrd && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/stackr ./cmd/stackr

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata docker-cli docker-cli-compose
WORKDIR /app
COPY --from=builder /out/stackrd /usr/local/bin/stackrd
COPY --from=builder /out/stackr /usr/local/bin/stackr
ENTRYPOINT ["/usr/local/bin/stackrd"]
