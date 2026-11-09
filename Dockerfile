# Build stage
FROM golang:bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/nas-filehub ./cmd/server

# Runtime — ffmpeg for thumbnails (software by default; VAAPI when /dev/dri + THUMB_VAAPI=true)
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates ffmpeg \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY --from=build /out/nas-filehub /app/nas-filehub
COPY scripts/docker-entrypoint.sh /app/docker-entrypoint.sh
RUN mkdir -p /data/files /data/thumbs \
 && useradd -r -u 1000 -d /data filehub \
 && chown -R filehub:filehub /data /app \
 && chmod 755 /app/docker-entrypoint.sh
USER filehub
ENV LISTEN_ADDR=:8080 \
    DATA_ROOT=/data/files \
    DB_PATH=/data/filehub.db \
    THUMB_CACHE_DIR=/data/thumbs \
    THUMB_ENABLE=true \
    THUMB_VAAPI=false \
    SECURE_COOKIES=false \
    LOGIN_RATE_LIMIT=10 \
    CREDENTIALS_FILE=/data/admin-credentials.txt \
    DOTENV_FILE=/data/.env
EXPOSE 8080
VOLUME ["/data"]
ENTRYPOINT ["/app/docker-entrypoint.sh"]
