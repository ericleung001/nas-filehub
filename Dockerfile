# Build stage — cross-compile from the build platform (amd64/arm64).
FROM --platform=$BUILDPLATFORM golang:bookworm AS build
WORKDIR /src
ARG TARGETOS=linux
ARG TARGETARCH=amd64
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o /out/nas-filehub ./cmd/server

# Runtime — debian ffmpeg is multi-arch (software decode). Hardware thumbnail
# flags (VAAPI / NVDEC) are selected at runtime when a device is present;
# stock Debian ffmpeg usually has VAAPI but not NVIDIA CUDA, and falls back
# to software when a backend is unavailable.
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates ffmpeg gosu \
    && rm -rf /var/lib/apt/lists/* \
    && useradd -r -u 1000 -d /data filehub
WORKDIR /app
COPY --from=build /out/nas-filehub /app/nas-filehub
COPY scripts/docker-entrypoint.sh /app/docker-entrypoint.sh
RUN mkdir -p /data/files /data/thumbs \
 && chmod 755 /app/docker-entrypoint.sh /app/nas-filehub
# Start as root so the entrypoint can apply PUID/PGID, then drop privileges.
ENV LISTEN_ADDR=:8080 \
    DATA_ROOT=/data/files \
    DB_PATH=/data/filehub.db \
    THUMB_CACHE_DIR=/data/thumbs \
    THUMB_ENABLE=true \
    THUMB_VAAPI=false \
    THUMB_HWACCEL= \
    PUID=1000 \
    PGID=1000 \
    SECURE_COOKIES=false \
    LOGIN_RATE_LIMIT=10 \
    CREDENTIALS_FILE=/data/admin-credentials.txt \
    DOTENV_FILE=/data/.env
EXPOSE 8080
VOLUME ["/data"]
ENTRYPOINT ["/app/docker-entrypoint.sh"]
