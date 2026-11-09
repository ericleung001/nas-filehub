# Filehub

Lightweight multi-user file manager — Go single binary + embedded web UI + SQLite.

Inspired by File Browser (simple UI) and Nextcloud-style per-user homes and shared folders.

**License:** Apache-2.0

## Where it runs

Filehub is **not** Unraid-only. It runs on any host that can run **Docker** or a **Go binary**:

- Linux VPS / bare metal
- Synology / QNAP / TrueNAS / Unraid (or any NAS with Docker)
- Windows or macOS with Docker Desktop
- Local development via `go run` / a built binary (Linux, Windows, macOS)

Use **Docker Compose** (recommended) or the single binary from Quick start below. The [Unraid + Intel VAAPI](#unraid--intel-vaapi-optional) section is **optional** — it only enables hardware-accelerated ffmpeg thumbnails when `/dev/dri` is present. Software ffmpeg works everywhere by default.

## Features

- **Multi-user homes** — each user gets `DATA_ROOT/homes/<username>`; users cannot see each other’s homes
- **Shared folders** — admin-created shares under `DATA_ROOT` with per-user `read` / `write` grants
- **File ops** — browse, upload (with per-file progress), drag-and-drop into current folder or onto a folder, download, mkdir, rename, delete; list or icons view; **sort** by name / date / type / size (asc or desc, saved in `localStorage`); fixed-column list keeps Name/Size/Modified/actions aligned (clickable headers); long names ellipsize; compact action icons keep Delete visible
- **Preview** — image preview and HTML5 video playback; icon view uses `/api/thumb` for image/video thumbnails; closing the dialog stops video/audio cleanly
- **Thumbnails** — optional ffmpeg thumbs (software by default; optional Intel VAAPI on hosts with `/dev/dri`, e.g. Unraid)
- **Settings** — one top-bar entry for everyone
  - **Account** — quota used/limit, change own password (`POST /api/me/password`)
  - **Users / Shares / System** tabs (admins only) — create/edit users, quotas, password reset, disable accounts, manage shares and grants; Users and Shares shown as list tables
- **Sidebar** — expandable folder tree under My Files / shares; storage used/limit bar
- **Security defaults** — no remote command/shell execution; bcrypt passwords; HttpOnly session cookies; login rate limit; path-traversal guards; non-root container user

Quota enforcement currently counts only the user’s **home** directory (shared folders are not billed against personal quota).

## Quick start

### Docker Compose (recommended)

```bash
cp .env.example .env   # set ADMIN_PASSWORD and SESSION_SECRET
mkdir -p data/files data/db
docker compose up -d --build
```

Open <http://127.0.0.1:8088>.  
Compose binds `127.0.0.1:8088` only — put Nginx Proxy Manager (or another reverse proxy) in front.

### Local development

```bash
export DATA_ROOT=./data/files
export DB_PATH=./data/filehub.db
export THUMB_CACHE_DIR=./data/thumbs
export ADMIN_USER=admin
export ADMIN_PASSWORD='your-strong-password'
export LISTEN_ADDR=:8080
go run ./cmd/server
```

Or: `ADMIN_PASSWORD='your-password' make run` / `make smoke`.

## Environment variables

| Variable | Default | Description |
|----------|---------|-------------|
| `LISTEN_ADDR` | `:8080` | HTTP listen address |
| `DATA_ROOT` | `/data/files` | File storage root |
| `DB_PATH` | `/data/filehub.db` | SQLite database path |
| `ADMIN_USER` | `admin` | Seed admin username (first boot) |
| `ADMIN_PASSWORD` | _(required first boot)_ | Seed admin password |
| `SESSION_SECRET` | — | Recommended; session tokens are random and stored in DB |
| `SECURE_COOKIES` | `false` | Set `true` behind HTTPS / NPM |
| `THUMB_ENABLE` | `true` | Generate thumbs with ffmpeg |
| `THUMB_VAAPI` | `false` | Use Intel VAAPI for thumbs when `/dev/dri` is available |
| `THUMB_CACHE_DIR` | `/data/thumbs` | Thumbnail cache directory |
| `LOGIN_RATE_LIMIT` | `10` | Max login attempts per IP per minute |
| `MAX_UPLOAD_MB` | `2048` | Max upload size in MiB |

## Settings

Sign in → **Settings** (single top-bar button):

1. **Account** (everyone) — username, admin badge (if any), home path, storage used / quota; **Change password** (requires current password; other sessions cleared, current kept)
2. **Users** (admins) — table list of users (home, storage used/quota, status); create / edit / delete; set quota; reset passwords; disable accounts
3. **Shares** (admins) — table list of shared folders (path, grants); create shares and grant `read` / `write`
4. **System** (admins) — short notes on `DATA_ROOT` layout and quota scope

The left sidebar shows the same storage used/limit under Roots, plus an expandable folder tree for My Files and shared roots.

Disabled users cannot log in. Uploads / mkdir that would exceed home quota are rejected.

## Nginx Proxy Manager

1. Keep Filehub on loopback / LAN only (`127.0.0.1:8088`)
2. Add a Proxy Host → your domain → forward to `http://<host>:8088` (or the Docker network hostname)
3. Enable SSL (Let’s Encrypt)
4. Prefer an Access List, Authelia, or Cloudflare Access in front
5. Set `SECURE_COOKIES=true`

**Do not** port-forward the app port directly to the public internet.

## Unraid + Intel VAAPI (optional)

This section is **optional hardware acceleration** for thumbnail generation only. Filehub itself does not require Unraid or a GPU.

Browser playback uses **client** decode. Server-side VAAPI mainly speeds **ffmpeg thumbnail** generation on hosts with Intel iGPU (e.g. Unraid on a J4125) when `/dev/dri` is available. Leave `THUMB_VAAPI` unset/`false` on any other platform.

In `docker-compose.yml`, uncomment:

```yaml
devices:
  - /dev/dri:/dev/dri
environment:
  THUMB_VAAPI: "true"
```

Confirm `/dev/dri/renderD128` exists on the host. If VAAPI fails, ffmpeg falls back to software; if that fails, the original image is used.

(Full transcode streaming belongs in products like Jellyfin; Filehub stays intentionally light.)

## Security defaults

- No remote command / shell execution surface
- bcrypt password hashes
- Session cookie: `HttpOnly`, `SameSite=Lax`, optional `Secure`
- Login rate limiting
- All file ops confined under the resolved scope root (path traversal protected)
- Container runs as non-root `filehub` (uid 1000)

## API summary

Auth / session:

- `POST /api/login` · `POST /api/logout` · `GET /api/me` · `POST /api/me/password` · `GET /api/health`
- `GET /api/me` includes `usedBytes` (home usage) alongside `quotaBytes`

Files:

- `GET /api/roots` · `GET /api/list`
- `POST /api/mkdir` · `POST /api/rename` · `POST /api/delete` · `POST /api/upload`
- `GET /api/download` · `GET /api/raw` · `GET /api/thumb`

Admin (requires admin session):

- `GET|POST /api/admin/users`
- `PATCH|DELETE /api/admin/users/{id}`
- `POST /api/admin/users/{id}/password`
- `GET|POST /api/admin/shares`
- `DELETE /api/admin/shares/{id}`
- Share grants under `/api/admin/shares/{id}/grants`

## Contributing / release habit

After every intentional change:

1. Update [CHANGELOG.md](CHANGELOG.md) (Keep a Changelog)
2. Commit with a clear English message
3. Push to `origin` `main`

See [CONTRIBUTING.md](CONTRIBUTING.md). Do not commit secrets, deploy env files, binaries, or screenshots.

## License

Apache License 2.0 — see [LICENSE](LICENSE).
