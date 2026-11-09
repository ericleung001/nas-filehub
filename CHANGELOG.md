# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html)
where version tags are used.

## [Unreleased]

### Added
- Unified **Settings** with tabs: **Account** for every user (quota + change password); admins also get **Users**, **Shares**, and **System** tabs (no separate top-bar Admin button).
- Per-user account panel: home quota used/limit, change own password via `POST /api/me/password` (`currentPassword`, `newPassword`; verifies current; keeps current session, clears others).
- Sidebar **folder tree** under Roots: expand/collapse directories for My Files and shares; clicking a node opens that path in the main list.
- Sidebar **Storage** used/limit bar (from `/api/me`).
- Upload progress UI: per-file status with filename, transferred bytes / percent, and success or error after each finish (XHR `upload.onprogress`; no API change).
- `GET /api/me` (and login) now include `usedBytes` for home usage.

### Fixed
- File list columns stay aligned under Name / Size / Modified / actions (`table-layout: fixed` + `colgroup`); long names ellipsize; actions (including Delete) stay fully visible via compact icon buttons.
- Modified hides under 960px; Size hides under 800px on narrow viewports.
- Closing the media preview (X, Escape, or backdrop) now pauses video, clears `src`, calls `load()`, and removes the element so audio cannot keep playing.

### Changed
- Top bar exposes a single **Settings** entry; admin user/share management moved into Settings tabs.
- Remove meta language note from README.

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

[Unreleased]: https://github.com/ericleung001/nas-filehub/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/ericleung001/nas-filehub/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/ericleung001/nas-filehub/releases/tag/v0.1.0
