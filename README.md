# NAS FileHub

Lightweight multi-user file manager — Go single binary + embedded web UI + SQLite.

Inspired by File Browser (simple UI) and Nextcloud-style per-user homes and shared folders.

**License:** Apache-2.0

> 短注：本 README 以英文為主。簡體／粵語說明可另外補；日常開發請跟 [CONTRIBUTING.md](CONTRIBUTING.md)。

## Features

- **Multi-user homes** — each user gets `DATA_ROOT/homes/<username>`; users cannot see each other’s homes
- **Shared folders** — admin-created shares under `DATA_ROOT` with per-user `read` / `write` grants
- **File ops** — browse, upload, download, mkdir, rename, delete
- **Preview** — image preview and HTML5 video playback (client-side decode)
- **Thumbnails** — optional ffmpeg thumbs (software by default; Unraid Intel VAAPI supported)
- **Admin console**
  - Create / edit / delete users
  - Set storage **quota** per user (e.g. `5GB`; `0` = unlimited)
  - See **home usage** vs quota in the user list
  - **Reset passwords** (invalidates that user’s sessions)
  - Disable accounts (blocked at login)
  - Manage shares and grants
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

## Admin usage

Sign in as an admin → **Admin**:

1. **Create user** — home is created automatically; optional quota (e.g. `5GB`, `500MB`, or `0` for unlimited)
2. **Edit user** — username, admin flag, home path, quota, disabled
3. **Set password** — resets password and clears that user’s sessions
4. **Create share** — path relative to `DATA_ROOT` (e.g. `shared/family`)
5. **Grant** — assign `read` or `write` to a user id

Disabled users cannot log in. Uploads / mkdir that would exceed home quota are rejected.

## Nginx Proxy Manager

1. Keep FileHub on loopback / LAN only (`127.0.0.1:8088`)
2. Add a Proxy Host → your domain → forward to `http://<host>:8088` (or the Docker network hostname)
3. Enable SSL (Let’s Encrypt)
4. Prefer an Access List, Authelia, or Cloudflare Access in front
5. Set `SECURE_COOKIES=true`

**Do not** port-forward the app port directly to the public internet.

## Unraid + Intel VAAPI (e.g. J4125)

Browser playback uses **client** decode. Server-side VAAPI mainly speeds **ffmpeg thumbnail** generation.

In `docker-compose.yml`, uncomment:

```yaml
devices:
  - /dev/dri:/dev/dri
environment:
  THUMB_VAAPI: "true"
```

Confirm `/dev/dri/renderD128` exists on the host. If VAAPI fails, ffmpeg falls back to software; if that fails, the original image is used.

(Full transcode streaming belongs in products like Jellyfin; FileHub stays intentionally light.)

## Security defaults

- No remote command / shell execution surface
- bcrypt password hashes
- Session cookie: `HttpOnly`, `SameSite=Lax`, optional `Secure`
- Login rate limiting
- All file ops confined under the resolved scope root (path traversal protected)
- Container runs as non-root `filehub` (uid 1000)

## API summary

Auth / session:

- `POST /api/login` · `POST /api/logout` · `GET /api/me` · `GET /api/health`

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
