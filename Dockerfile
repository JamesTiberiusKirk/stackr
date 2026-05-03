FROM golang:1.26 AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Generate templ files before building the daemon (cmd/stackrd).
RUN go install github.com/a-h/templ/cmd/templ@$(grep 'github.com/a-h/templ ' go.mod | awk '{print $2}') && \
    templ generate
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/stackrd ./cmd/stackrd && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/stackr ./cmd/stackr

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata docker-cli docker-cli-compose
WORKDIR /app
# Static assets need to be available at runtime for the daemon's web layer.
COPY --from=builder /src/static /app/static
COPY --from=builder /out/stackrd /usr/local/bin/stackrd
COPY --from=builder /out/stackr /usr/local/bin/stackr
ENTRYPOINT ["/usr/local/bin/stackrd"]
