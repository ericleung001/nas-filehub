# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html)
where version tags are used.

## [Unreleased]

## [0.3.8] - 2026-11-09

### Added
- **First-boot auto credentials** — Docker entrypoint (`scripts/docker-entrypoint.sh`) generates a random `ADMIN_PASSWORD` and `SESSION_SECRET` when both `/data/admin-credentials.txt` and `/data/.env` are missing and env values are unset/placeholders. Writes them **once** to those paths (mode `0600`); **never overwrites** existing files. Compose mounts `./data:/data`, so read **`./data/admin-credentials.txt`** on the host after first `docker compose up`.
- **`scripts/gen-env.sh`** — host helper that creates project-root `.env` (from `.env.example` when present) and `./data/admin-credentials.txt` once; safe to re-run (no overwrite).

### Changed
- README first-boot section documents the credentials file paths prominently; compose no longer injects weak default passwords; `.env.example` leaves `ADMIN_PASSWORD` / `SESSION_SECRET` empty for auto-generation.

## [0.3.7] - 2026-11-09

### Changed
- **Docs:** README and `.env.example` spell out first-boot admin setup — set `ADMIN_PASSWORD` in `.env` before `docker compose` / first run; default `ADMIN_USER=admin`; no password is emailed or printed; change later under Settings → Change password (env only seeds an empty DB).

## [0.3.6] - 2026-11-09

### Changed
- **Sign up** and **Forgot password** are separate pages (`/register`, `/forgot`) instead of long forms on the login screen. Login keeps username/password (+ 2FA steps) with links when those features are enabled. Password reset remains at `/reset?token=…`.

## [0.3.5] - 2026-11-09

### Added
- **Open registration** — Settings → System toggle **Allow open registration**. When on, the login page shows **Sign up**; `POST /api/register` creates a non-admin user with username + email + password (enforces username rules and the password policy). Accounts are active immediately (admin approval not required yet). `GET /api/auth/features` exposes `openRegistration` and the password policy hint.

### Fixed
- **PDF preview** no longer shows “refused to connect” in the modal or on public share pages. Cause: global `X-Frame-Options: DENY` blocked same-origin iframes of `/api/raw` and `/api/public/links/.../raw`. Fix: allow `SAMEORIGIN` framing (plus `frame-ancestors 'self'`) for those raw/thumb routes, set `Content-Type: application/pdf` with `Content-Disposition: inline` for PDFs, and load previews via a same-origin **blob object URL** so framing headers cannot break the viewer.
- **Settings → Users** actions column: the current user’s lone **Edit** button no longer sits flush-right while other rows show Edit+Del. Actions use a two-slot grid plus a spacer when Del is omitted so Edit stays aligned.

## [0.3.4] - 2026-11-09

### Added
- **PDF inline preview** in the authenticated browser and on public share pages (iframe against `/api/raw` or `/api/public/links/{token}/raw`). Single-file PDF shares show the document on open; folder shares get a Preview action. Files with `.pdf` are classified as kind `pdf`.

### Fixed
- **Uploads** floating panel stays **hidden** until at least one upload is queued; **Clear** (or an empty list) hides it again. CSS `display: flex` no longer overrides the `hidden` attribute.

## [0.3.3] - 2026-11-09

### Fixed
- Public share links **without a password** no longer show the password form. Unlock UI only appears when `hasPassword` is true and unlock is still required (`needPassword`).

### Added
- Public share page: **inline image preview and HTML5 video playback** for single-file shares; in folder shares, Preview / Play opens the same modal; Download remains available. Uses `/api/public/links/{token}/raw`.

## [0.3.2] - 2026-11-09

### Added
- **Email / SMTP** — Settings → System → Email (SMTP): enable, host, port, username, password, from. Password stored in SQLite settings and never returned to the UI (`passwordSet` flag only). Optional env overrides: `SMTP_ENABLED`, `SMTP_HOST`, `SMTP_PORT`, `SMTP_USERNAME`, `SMTP_PASSWORD`, `SMTP_FROM`. Optional `PUBLIC_BASE_URL` for reset links behind a reverse proxy.
- **User email** — admin set on create/edit; shown on Account.
- **Forgot password** — login link when SMTP is ready; `POST /api/forgot-password` sends a 1-hour reset link; `/reset?token=` page sets a new password under the current password policy. Rate-limited (5/min/IP). Responses avoid account enumeration.
- Admin **Send test email** (`POST /api/admin/smtp/test`).

### Changed
- Uploads panel is a **bottom-right floating** card with header progress `(done/total)`, scrollable list, and Clear.

## [0.3.1] - 2026-11-09

### Fixed
- Public share URLs (`/s/{token}`) no longer hit `ERR_TOO_MANY_REDIRECTS`. SPA fallback served `/index.html` through `http.FileServer`, which 301-redirects to `./`; under `/s/...` that resolves to `/s/` and loops. Fallback now serves `/` so index.html is returned without a redirect.
- Settings → Link shares table: separate Label / Target / URL / Status columns with ellipsis so long URLs no longer overlap.

### Changed
- Deleting a file or folder (single or bulk) also removes matching `link_shares` rows (exact path and links nested under a deleted folder) so the Link shares page does not keep dead links.

## [0.3.0] - 2026-11-09

### Added
- **Multi-select** — checkboxes in List and Icons views; header select-all; selection clears on folder change.
- **Bulk actions** — Delete / Move / Copy selected items (`POST /api/batch/delete|move|copy`); Move/Copy pick destination root + folder path.
- **Link shares** — share a file or whole folder with a secret URL (`/s/<token>`). Optional expiry and password; recipients get read-only browse/download without an account. Create via 🔗 on a row or bulk Share (one item). Settings → **Link shares** to copy or revoke (owner or admin).
- Public endpoints under `/api/public/links/{token}/` (`meta`, `unlock`, `list`, `download`, `raw`); authenticated `GET/POST /api/links`, `DELETE /api/links/{id}`.
- **TOTP 2FA** — voluntary enable/disable; admin per-user require or force-all; login second step + enrollment gate; backup codes.
- **Password policy** — admin min length and require letters/digits/symbols; policy version forces password change on next login.
- **Login remediation** — must change password and/or enroll 2FA before using the file browser.
- File **sort** by name / date / type / size (asc/desc); **List / Icons** views; drag-and-drop upload; sidebar folder tree + storage bar; unified Settings left nav.

### Fixed
- Uploads panel capped height so large queues do not push the layout.
- Sort controls stay on one toolbar row; list columns stay aligned; Delete stays visible; closing video preview stops audio.

### Changed
- Display name **Filehub**; README clarifies any Docker/Linux host (Unraid VAAPI optional).
- Settings Users/Shares as tables; single Settings entry (no separate Admin button).

## [0.2.0] - 2026-11-06

### Added

- Admin user management UI and API
  - Edit username, admin flag, home path
  - Per-user storage quota (`quotaBytes`; `0` = unlimited; UI accepts `5GB` / `500MB`)
  - Home usage shown next to quota in the admin user list
  - Admin password reset (`POST /api/admin/users/{id}/password`); clears that user’s sessions
  - Disable / enable accounts (blocked at login; sessions cleared when disabled)
- Quota enforcement on upload and mkdir for the user’s home root
- DB migration helpers for new user columns (`quota_bytes`, `disabled`)

### Changed

- Admin user create accepts optional `quotaBytes`
- `/api/me` and admin user payloads include `quotaBytes` and `disabled`

## [0.1.0] - 2026-11-06

### Added

- Initial MVP: multi-user file manager (Go + embedded web UI + SQLite)
- Per-user homes under `DATA_ROOT/homes/<user>`
- Shared folders with `read` / `write` grants
- Browse, upload, download, mkdir, rename, delete
- Image preview and HTML5 video playback
- Optional ffmpeg thumbnails; Unraid Intel VAAPI hooks documented
- Docker Compose bind to `127.0.0.1:8088`; NPM / reverse-proxy guidance
- Security defaults: bcrypt, HttpOnly sessions, login rate limit, path traversal guards, non-root container
- Env-based config (`LISTEN_ADDR`, `DATA_ROOT`, `DB_PATH`, admin seed, thumbs, upload limits)

[Unreleased]: https://github.com/ericleung001/nas-filehub/compare/v0.3.7...HEAD
[0.3.7]: https://github.com/ericleung001/nas-filehub/compare/v0.3.6...v0.3.7
[0.3.6]: https://github.com/ericleung001/nas-filehub/compare/v0.3.5...v0.3.6
[0.3.5]: https://github.com/ericleung001/nas-filehub/compare/v0.3.4...v0.3.5
[0.3.4]: https://github.com/ericleung001/nas-filehub/compare/v0.3.3...v0.3.4
[0.3.3]: https://github.com/ericleung001/nas-filehub/compare/v0.3.2...v0.3.3
[0.3.2]: https://github.com/ericleung001/nas-filehub/compare/v0.3.1...v0.3.2
[0.3.1]: https://github.com/ericleung001/nas-filehub/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/ericleung001/nas-filehub/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/ericleung001/nas-filehub/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/ericleung001/nas-filehub/releases/tag/v0.1.0
