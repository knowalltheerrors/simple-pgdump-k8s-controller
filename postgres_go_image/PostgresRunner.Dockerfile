FROM golang:1.25.3 AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/backup-runner ./cmd/backup-runner
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/backup-runner ./cmd/backup-runner


FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates curl gnupg \
    && install -d /usr/share/postgresql-common/pgdg \
    && curl -fsSL https://www.postgresql.org/media/keys/ACCC4CF8.asc \
         -o /usr/share/postgresql-common/pgdg/apt.postgresql.org.asc \
    && echo "deb [signed-by=/usr/share/postgresql-common/pgdg/apt.postgresql.org.asc] \
         https://apt.postgresql.org/pub/repos/apt bookworm-pgdg main" \
         > /etc/apt/sources.list.d/pgdg.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends postgresql-client-18 \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /out/backup-runner /usr/local/bin/backup-runner

USER 65532:65532
ENTRYPOINT ["/usr/local/bin/backup-runner"]