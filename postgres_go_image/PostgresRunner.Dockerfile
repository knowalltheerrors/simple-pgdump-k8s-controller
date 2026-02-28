FROM golang:1.25.3 AS builder

# Build Go binary
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/backup-runner ./cmd/backup-runner
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/backup-runner ./cmd/backup-runner

# Runtime image with pg_dump
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates postgresql-client \
    && rm -rf /var/lib/apt/lists/*
COPY --from=builder /out/backup-runner /usr/local/bin/backup-runner
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/backup-runner"]
