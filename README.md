# Filehub

Lightweight multi-user file manager — Go single binary + embedded web UI + SQLite.

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
- **Shared folders** — admin-created shares under `DATA_ROOT` with per-user `read` / `write` access and an independent **Allow delete** flag (admin picks a user from a dropdown; existing grants can be changed or revoked)
- **Link shares** — secret URLs for a file or folder (`/s/<token>`); optional expiry and password (password form only when the link has one); recipients browse/download without an account and can **preview images / PDFs / play videos** inline; revoke (hard-delete) anytime from Settings → Link shares, or select several and **Delete selected**
- **File ops** — browse, upload (with per-file progress in a **bottom-right floating** Uploads panel showing `(done/total)`, hidden until uploads start), drag-and-drop into current folder or onto a folder, download, mkdir, rename, delete; **multi-select** with bulk Delete / Move / Copy; list or icons view; **sort** by name / date / type / size (asc or desc, saved in `localStorage`); fixed-column list keeps columns aligned; long names ellipsize; compact action icons
- **Preview** — image, **PDF** (blob-backed iframe; raw endpoints allow same-origin framing), and HTML5 video playback; icon view uses `/api/thumb` for image/video thumbnails; closing the dialog stops video/audio cleanly
- **Thumbnails** — optional ffmpeg thumbs (software by default; optional Intel VAAPI on hosts with `/dev/dri`, e.g. Unraid)
- **App icon** — favicon and Apple touch icon; mark on the login / auth pages and in the top bar after sign-in
- **Settings** — one top-bar entry; left vertical menu, right content panel
  - **Account** — username, home, quota used/limit
  - **Change password** — `POST /api/me/password` (enforces admin password policy)
  - **2FA** — enable/disable TOTP
  - **Link shares** — list, copy, or revoke (permanently delete) public links you created (admins see all); select multiple to delete together
  - **Users / Shares / Sessions / Registration / Security / Email / System** (admins only) — user/share tables; **Sessions** (duration + Stay signed in); **Registration**; **Security** (Force 2FA + password policy); **Email** (SMTP); **System** (DATA_ROOT notes)
  - **About** (everyone) — project description, GitHub, and Buy Me a Coffee
- **Sidebar** — expandable folder tree under My Files / shares; storage used/limit bar
- **Two-factor authentication (TOTP)** — voluntary per user; admin can require per user or force all (`FORCE_2FA_ALL` / System settings); login second step + forced enrollment; backup codes on enable
- **Password policy** — admin-configurable min length and character classes; enforced on create user, admin reset, self-service change, and forgot-password reset; policy version forces remediation after tighten; login blocks the app until password/2FA requirements are met
- **Forgot password** — admin configures SMTP; users with an email can request a reset link at `/forgot` (linked from login when ready; rate-limited; generic responses)
- **Open registration** — optional admin toggle; `/register` (linked from login) creates username/email/password accounts under the password policy (active immediately; no approval gate yet)
- **Session duration** — admin sets normal login lifetime (default **7 days**) and may allow **Stay signed in** (default **30 days**); applies to new logins only; existing sessions keep their expiry
- **Security defaults** — no remote command/shell execution; bcrypt passwords; HttpOnly session cookies; login / reset rate limits; path-traversal guards; non-root container user; SMTP password never returned by the API; `X-Frame-Options: DENY` except same-origin framing for `/api/raw`, `/api/thumb`, and public `/raw` preview URLs

Quota enforcement currently counts only the user’s **home** directory (shared folders are not billed against personal quota).

## Quick start

### First boot — admin password (read this)

On **first install**, Filehub creates the seed admin from `ADMIN_USER` / `ADMIN_PASSWORD`. There is **no emailed default password**.

**Docker (recommended):** if you leave `ADMIN_PASSWORD` unset, the container **entrypoint auto-generates** a random `ADMIN_PASSWORD` and `SESSION_SECRET` **once** and writes them to:

| Path inside container | Host path (with default compose `./data:/data`) |
|----------------------|--------------------------------------------------|
| `/data/admin-credentials.txt` | **`./data/admin-credentials.txt`** ← **read this for the password** |
| `/data/.env` | `./data/.env` |

Those files are **never overwritten** on later starts. Open the credentials file, sign in as `admin`, then change the password under **Settings → Change password**.

Optional host helper (project-root `.env` + the same credentials file, also once-only):

```bash
./scripts/gen-env.sh
```

You may still set your own `ADMIN_PASSWORD` / `SESSION_SECRET` in a project `.env` or compose environment before first start; if you do, first boot will use your values (and still write the credentials file once when it is missing).

`ADMIN_PASSWORD` only **seeds** an empty database. Editing env later does **not** change an existing admin hash — use Settings (or recreate the DB during initial setup).

### Docker Compose (recommended)

```bash
mkdir -p data/files data/db
# Optional: ./scripts/gen-env.sh   # writes ./.env + ./data/admin-credentials.txt once
docker compose up -d --build
# Read the generated password (first boot only):
cat ./data/admin-credentials.txt
```

Open <http://127.0.0.1:8088> and sign in as `admin` with the password from `./data/admin-credentials.txt`.  
Compose binds `127.0.0.1:8088` only — put Nginx Proxy Manager (or another reverse proxy) in front.

### Local development

```bash
./scripts/gen-env.sh   # once — creates ./.env and ./data/admin-credentials.txt
set -a && source .env && set +a
export DATA_ROOT=./data/files DB_PATH=./data/filehub.db THUMB_CACHE_DIR=./data/thumbs LISTEN_ADDR=:8080
go run ./cmd/server
```

Or set `ADMIN_PASSWORD` yourself and run `make run` / `make smoke`.

## Environment variables

| Variable | Default | Description |
|----------|---------|-------------|
| `LISTEN_ADDR` | `:8080` | HTTP listen address |
| `DATA_ROOT` | `/data/files` | File storage root |
| `DB_PATH` | `/data/filehub.db` | SQLite database path |
| `ADMIN_USER` | `admin` | Seed admin **username** on first boot only |
| `ADMIN_PASSWORD` | _(auto on first Docker boot if unset)_ | Seed admin **password** — auto-written once to `./data/admin-credentials.txt` + `./data/.env` when unset; ignored after the admin already exists |
| `SESSION_SECRET` | _(auto on first Docker boot if unset)_ | Auto-generated with the credentials file when unset; session tokens are also random in DB |
| `CREDENTIALS_FILE` | `/data/admin-credentials.txt` | Path for the one-time admin credentials file (Docker entrypoint) |
| `DOTENV_FILE` | `/data/.env` | Path for the one-time generated env copy (Docker entrypoint) |
| `SECURE_COOKIES` | `false` | Set `true` behind HTTPS / NPM |
| `THUMB_ENABLE` | `true` | Generate thumbs with ffmpeg |
| `THUMB_VAAPI` | `false` | Use Intel VAAPI for thumbs when `/dev/dri` is available |
| `THUMB_CACHE_DIR` | `/data/thumbs` | Thumbnail cache directory |
| `LOGIN_RATE_LIMIT` | `10` | Max login attempts per IP per minute |
| `MAX_UPLOAD_MB` | `2048` | Max upload size in MiB |
| `FORCE_2FA_ALL` | `false` | If true, every user must enroll TOTP (overrides UI off) |
| `PUBLIC_BASE_URL` | — | Absolute site URL for password-reset emails (e.g. `https://filehub.example.com`) |
| `SMTP_ENABLED` | `false` | Env override: enable SMTP |
| `SMTP_HOST` / `SMTP_PORT` / `SMTP_USERNAME` / `SMTP_PASSWORD` / `SMTP_FROM` | — | Env overrides for SMTP (password never logged) |

## Settings

Sign in → **Settings** (single top-bar button):

1. **Account** (everyone) — username, email, admin badge (if any), home path, storage used / quota
2. **Change password** — requires current password; must meet the admin password policy; other sessions cleared, current kept
3. **2FA** — enable/disable TOTP (Authenticator app + backup codes)
4. **Link shares** — copy or revoke (permanently delete) public file/folder links, or select several and **Delete selected**; deleted rows leave the list
5. **Users** (admins) — table list of users (home, storage used/quota, status); create / edit / delete; set email / quota; reset passwords; disable accounts; per-user require 2FA
6. **Shares** (admins) — table list of shared folders (path, grants); create shares; grant access with a user dropdown, Read or Write (write includes read), and **Allow delete**; change or revoke existing grants
7. **Sessions** (admins) — normal session duration and optional Stay signed in (applies to new logins only)
8. **Registration** (admins) — allow open registration
9. **Security** (admins) — Force 2FA for all users; password policy (tightening bumps a version so existing users must change password on next login)
10. **Email** (admins) — SMTP for forgot-password
11. **System** (admins) — `DATA_ROOT` / homes / shares / quota notes
12. **About** (everyone) — what Filehub is, Apache-2.0, [GitHub](https://github.com/ericleung001/nas-filehub), and [Buy Me a Coffee](https://buymeacoffee.com/filehub)

The left sidebar shows the same storage used/limit under Roots, plus an expandable folder tree for My Files and shared roots.

On phones, the file toolbar wraps so Upload/Refresh stay visible, and Settings uses a horizontal scrollable chip nav.

Disabled users cannot log in. Uploads / mkdir that would exceed home quota are rejected.

## Nginx Proxy Manager

1. Keep Filehub on loopback / LAN only (`127.0.0.1:8088`)
2. Add a Proxy Host → your domain → forward to `http://<host>:8088` (or the Docker network hostname)
3. Enable SSL (Let’s Encrypt)
4. Prefer an Access List, Authelia, or Cloudflare Access in front
5. Set `SECURE_COOKIES=true`

**Do not** port-forward the app port directly to the public internet.


## Releases

Tagged releases are published on GitHub: [ericleung001/nas-filehub/releases](https://github.com/ericleung001/nas-filehub/releases).

Each release includes:

- **Changelog** notes for that version
- **Downloadable binaries** for Linux (amd64), and often Windows/macOS when built for that tag
- **Docker** install notes (see Quick start — `docker compose up -d --build` from the tagged source)

### Install from a release binary

```bash
# example: Linux amd64 from the latest release assets
chmod +x nas-filehub-linux-amd64
# Once: write ./.env + ./data/admin-credentials.txt (or export ADMIN_PASSWORD yourself)
./scripts/gen-env.sh   # from a full clone; or: export ADMIN_USER=admin ADMIN_PASSWORD='your-strong-password'
set -a && source .env && set +a
export DATA_ROOT=./data/files DB_PATH=./data/filehub.db THUMB_CACHE_DIR=./data/thumbs LISTEN_ADDR=:8080
./nas-filehub-linux-amd64
```

Sign in as `admin` with the password from `./data/admin-credentials.txt` (or the one you exported), then change it under **Settings → Change password**. Prefer Docker Compose for production (bind to loopback and put a reverse proxy in front) — first `docker compose up` auto-writes that credentials file when `ADMIN_PASSWORD` is unset.

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
- `GET /api/me` includes `usedBytes` (home usage) alongside `quotaBytes` and `email`
- `GET /api/auth/features` (`openRegistration`, `allowRememberMe`, password policy, forgotPassword) · `POST /api/forgot-password` · `GET /api/reset-password/meta` · `POST /api/reset-password`
- `POST /api/login` accepts optional `rememberMe` (honored only when admin allows Stay signed in)

Files:

- `GET /api/roots` · `GET /api/list`
- `POST /api/mkdir` · `POST /api/rename` · `POST /api/delete` · `POST /api/upload`
- `GET /api/download` · `GET /api/raw` · `GET /api/thumb`

Admin (requires admin session):

- `GET|POST /api/admin/users` (email on create)
- `PATCH|DELETE /api/admin/users/{id}` (email on patch)
- `POST /api/admin/users/{id}/password`
- `GET|PUT /api/admin/settings` (`force2faAll`, `openRegistration`, `sessionTtlSeconds`, `rememberMeTtlSeconds`, `allowRememberMe`, password policy, SMTP — password write-only)
- `POST /api/admin/smtp/test`
- `GET|POST /api/admin/shares`
- `DELETE /api/admin/shares/{id}`
- Share grants under `/api/admin/shares/{id}/grants`

## Contributing / release habit

After every intentional change:

1. Update [CHANGELOG.md](CHANGELOG.md) (Keep a Changelog)
2. Commit with a clear English message
3. Push to `origin` `main`

See [CONTRIBUTING.md](CONTRIBUTING.md). Do not commit secrets, deploy env files, binaries, or screenshots.


## Email and forgot password

1. Admin → Settings → **Email**: enable, host, port, username, password, from address. Save. Optionally **Send test email**.
2. Set each user’s **email** under Users → Create / Edit (required for that user to receive reset mail).
3. Prefer setting `PUBLIC_BASE_URL` to your public HTTPS origin so reset links are correct behind Nginx Proxy Manager.
4. The login page links to **Forgot password?** (`/forgot`) when SMTP is ready. The user enters username or email; if a matching account has an email, a one-hour link is sent. The `/reset?token=…` page enforces the current password policy.

SMTP credentials can also be supplied via env (`SMTP_*`); env values override the DB for the running process. The API never returns the SMTP password — only `passwordSet: true|false`.

## Support

Filehub is open source: [ericleung001/nas-filehub](https://github.com/ericleung001/nas-filehub).

If Filehub is useful, you can [buy me a coffee](https://buymeacoffee.com/filehub).

## License

Apache License 2.0 — see [LICENSE](LICENSE).

## Two-factor authentication (2FA)

Uses standard TOTP (RFC 6238, 30s / SHA1 / 6 digits), compatible with Google Authenticator, 1Password, Authy, etc.

1. **User (voluntary)** — Settings → Account → Enable 2FA → scan QR → confirm code. Backup codes are shown once. Disable with password or a current code (blocked if 2FA is required).
2. **Admin per user** — Settings → Users → Edit → **require 2FA**.
3. **Admin all users** — Settings → Security → **Force 2FA for all users**, or set env `FORCE_2FA_ALL=true`.
4. **Login** — after a correct password, if 2FA is enabled the UI asks for a code (or backup code). If 2FA is required but not enrolled, enrollment (QR) is required before a session is issued.

### API — 2FA

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| POST | `/api/login` | — | `{username,password,rememberMe?}`; may return `{needTotp,pendingToken}` or `{needTotpSetup,pendingToken}` (rememberMe survives 2FA) |
| POST | `/api/login/totp` | — | `{pendingToken,code}` → session |
| POST | `/api/login/totp/setup` | — | `{pendingToken}` → `{secret,otpauthUrl,qrDataUrl}` (enroll challenge) |
| POST | `/api/login/totp/enable` | — | `{pendingToken,code}` → session + `{backupCodes}` |
| POST | `/api/me/totp/setup` | session | Start enrollment → QR/secret |
| POST | `/api/me/totp/confirm` | session | `{code}` enable + `{backupCodes}` |
| POST | `/api/me/totp/disable` | session | `{password}` or `{code}` |
| GET/PUT | `/api/admin/settings` | admin | `{force2faAll,openRegistration,sessionTtlSeconds,rememberMeTtlSeconds,allowRememberMe,…}` |
| PATCH | `/api/admin/users/{id}` | admin | accepts `totpRequired` |

SQLite: `users.totp_secret`, `users.totp_enabled`, `users.totp_required`; `settings.force_2fa_all`, `session_ttl_seconds`, `remember_me_ttl_seconds`, `allow_remember_me`; `pending_logins` (includes `remember_me`); `totp_backup_codes`.
