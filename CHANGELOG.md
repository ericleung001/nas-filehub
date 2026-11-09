# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html)
where version tags are used.

## [Unreleased]

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

[Unreleased]: https://github.com/ericleung001/nas-filehub/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/ericleung001/nas-filehub/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/ericleung001/nas-filehub/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/ericleung001/nas-filehub/releases/tag/v0.1.0
