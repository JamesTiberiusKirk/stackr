FROM golang:1.26.2-alpine AS builder

ARG VERSION=dev

WORKDIR /app
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags "-X main.version=${VERSION}" -o /site ./cmd/stackrd

FROM alpine:3.21
RUN apk --no-cache add ca-certificates
COPY --from=builder /site /site
COPY --from=builder /app/static /static

# SQLite database file directory — mount a volume here for persistence.
RUN mkdir -p /data
VOLUME ["/data"]
ENV DATABASE_PATH=/data/daemon.db

EXPOSE 8080
ENTRYPOINT ["/site"]
