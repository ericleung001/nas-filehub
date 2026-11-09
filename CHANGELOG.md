# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html)
where version tags are used.

## [Unreleased]

### Added
- **Admin password policy** — Settings → System: minimum length (4–128) and require letters / digits / symbols. Stored in `settings`; changing rules bumps `password_policy_version` so users with an older stamp must change password before using the app.
- **Login remediation gate** — after password (and TOTP if enabled), users who fail the current policy or are behind the policy version must change password; users required to use 2FA must enroll. Password gate runs before 2FA enroll. Server `requireAuth` allows only `/api/me`, password/TOTP remediation routes, and logout while gated.
- **Settings left nav** — vertical menu (Account, Change password, 2FA; admins also Users, Shares, System) with a single right-hand panel for the selected section (no stacked account cards).

- **TOTP two-factor authentication (RFC 6238)** — users can enable/disable 2FA in Settings → Account (QR + secret, confirm with code; optional backup codes shown once). Login asks for a TOTP/backup code when enabled. Admins can require 2FA per user (Users → Edit → require 2FA) or force all users (Settings → System, or env `FORCE_2FA_ALL`). Forced users who have not enrolled must set up 2FA before accessing files.
- **File list sorting** — sort by Name, Date (modified), Type (extension), or Size; Asc/Desc toggle; preference saved in `localStorage` (`filehub-sort-by`, `filehub-sort-dir`); works in List and Icons views; list column headers are clickable.
- **Drag-and-drop upload** from macOS/Windows: drop on the empty list/grid area to upload into the current folder; drop onto a folder row/icon to upload into that folder (visual drop-target highlight).
- **List / Icons view toggle** in the toolbar (preference saved in `localStorage`).
- **Icon view** shows image/video thumbnails via `GET /api/thumb` (falls back to file-type icons); clicking an image or video opens the existing preview modal (close still stops audio).
- Unified **Settings** with tabs: **Account** for every user (quota + change password); admins also get **Users**, **Shares**, and **System** tabs (no separate top-bar Admin button).
- Per-user account panel: home quota used/limit, change own password via `POST /api/me/password` (`currentPassword`, `newPassword`; verifies current; keeps current session, clears others).
- Sidebar **folder tree** under Roots: expand/collapse directories for My Files and shares; clicking a node opens that path in the main list.
- Sidebar **Storage** used/limit bar (from `/api/me`).
- Upload progress UI: per-file status with filename, transferred bytes / percent, and success or error after each finish (XHR `upload.onprogress`; no API change).
- `GET /api/me` (and login) now include `usedBytes` for home usage.

### Fixed
- Upload status panel no longer pushes the layout when many files are queued: `#upload-list` is capped (`max-height: min(40vh, 280px)`) with `overflow-y: auto`; **Clear** stays in the non-scrolling header.
- Toolbar **Sort** label + dropdown + Asc/Desc stay on one horizontal row (global `label { flex-direction: column }` no longer stacks them).
- File list columns stay aligned under Name / Size / Modified / actions (`table-layout: fixed` + `colgroup`); long names ellipsize; actions (including Delete) stay fully visible via compact icon buttons.
- Modified hides under 960px; Size hides under 800px on narrow viewports.
- Closing the media preview (X, Escape, or backdrop) now pauses video, clears `src`, calls `load()`, and removes the element so audio cannot keep playing.

### Changed
- Settings UI uses a left vertical section menu instead of top tabs / stacked Account cards.
- README clarifies Filehub runs on any Docker / Linux host (VPS, Synology, Windows/macOS with Docker, bare Go binary); Unraid + VAAPI docs marked as optional hardware accel only.
- Product display name is **Filehub** (UI title/header/login; repository path remains `nas-filehub`).
- Settings **Users** and **Shares** admin tabs render as dark-theme tables (columns + row actions) instead of stacked cards.
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
