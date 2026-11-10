# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html)
where version tags are used.

## [Unreleased]

## [0.4.2] - 2026-11-10

### Added
- **Recycle bin count badge** — when the recycle bin has items, a compact count appears to the right of the Recycle bin label in the sidebar. Counts above 99 display as `99+`. The badge is hidden when the bin is empty, uses the same per-user visibility as the recycle bin list, and refreshes after delete, restore, purge, emptying the bin, and on load or navigation.
- **Unraid Community Apps screenshots** — the Docker template now includes current login and file-browser screenshots (`templates/screenshots/01-login.png`, `templates/screenshots/02-after-login.png`).

## [0.4.1] - 2026-11-10

### Fixed
- **Recycle bin actions** — Restore and Empty recycle bin only appear inside the recycle bin (a CSS `display:flex` rule had been overriding the `hidden` attribute). Copy and Share link are hidden in the recycle bin; they remain available in normal folders.

### Changed
- **Unraid Community Apps template** — set `<Category>` in `templates/filehub.xml` to `Tools:Utilities Productivity:` (Tools › Utilities and Productivity), replacing the invalid `Cloud Tools:Utilities` value. The Community Apps scanner uses the template Category field for the app listing.

## [0.4.0] - 2026-11-10

### Added
- **linux/arm64 container images** — the Docker workflow builds `linux/amd64` and `linux/arm64` (QEMU + Buildx). Version tags still omit the `v` prefix (`0.4.0`, plus `0.4`, `latest` on the default branch, and a sha tag). The Debian `ffmpeg` package in the image is multi-arch.
- **Docker Hub publish** — the same workflow can also push `ericleung001/nas-filehub` to Docker Hub with the same tags when `DOCKERHUB_USERNAME` and `DOCKERHUB_TOKEN` are set. If those secrets are missing, Hub login and tags are skipped and GHCR still publishes.
- **Share-aware quotas** — usage is the user’s home plus files in shares attributed to that user as the acting writer (upload, copy, or replace). A share is never billed in full to every grantee. Enforcement on upload and copy into a share checks the acting user’s quota. Settings → System totals and the Users storage bar use the same definition.
- **Hardware thumbnail acceleration** — `THUMB_HWACCEL=auto|off|vaapi|nvdec` (aliases `cuda`/`cuvid`/`nvidia` map to nvdec). Legacy `THUMB_VAAPI=true` still means auto. Filehub selects Intel or AMD VAAPI on the matching render node, or NVIDIA CUDA/NVDEC when that device is detected, then falls back to software ffmpeg and finally the original image. Settings → System reports the selected backend and whether the current path can use a detected GPU. Argument selection is unit-tested; live GPU decode is not claimed without a working ffmpeg build and device.
- **PUID / PGID** — the image starts as root, chowns `/data`, optional `UMASK`, then drops privileges with `gosu`. Defaults remain `1000:1000` when unset. Documented in README, compose, and the Unraid template.
- **Registration approval** — new self-registered accounts are pending until an admin approves or rejects them (Settings → Users and Registration). The “require admin approval” toggle defaults to on. Pending users cannot sign in. Rejecting a pending account deletes it so the username can be reused. Already-active users and admin-created users are unchanged. Email notification is optional and is not required.
- **File search** — filename search across My Files and shares the user can read (`GET /api/search`), with a search box in the file browser (desktop and mobile).
- **Recycle bin** — delete (single and bulk), including in shares when Allow delete / owner / admin applies, moves items to `DATA_ROOT/.filehub-trash/<id>/` instead of a permanent delete. Users can restore or permanently purge; Empty recycle bin purges that user’s items. Trashed files still count toward the owner’s quota until purged. Link-share revoke stays a hard-delete of the link row, not a file-trash operation.
- **Folder zip download** — download a readable folder (including a share) as a streamed zip (`GET /api/zip` or the download button on a folder), with size and time safeguards. Paths stay inside the allowed tree.
- **Docker Hub docs** — public repository [`ericleung001/nas-filehub`](https://hub.docker.com/r/ericleung001/nas-filehub) (added while 0.3.22 was current; 0.4.0 continues the same name).
- **Catalog packaging snapshots** — `casaos/` and `truenas/` still document the official catalog submissions that pin **amd64** `ghcr.io/ericleung001/nas-filehub:0.3.22`. Those catalog PRs were not updated in this release and should be followed up later.

### Changed
- Quota documentation and System storage notes describe the share-writer and recycle-bin rules. Legacy share files with **no recorded writer stay unbilled** until a Filehub user writes or replaces them (they are not assigned to the share creator and are not billed to every member).


## [0.3.22] - 2026-11-10

### Changed
- **Login** — removed the Chinese tagline on the login page. The line under the Filehub title is now English only: “Multi-user file manager”.

### Fixed
- **Windows build** — Settings → System disk usage used Unix-only `syscall.Statfs`, which prevented `GOOS=windows` compiles. Windows now reports DATA_ROOT usage via `GetDiskFreeSpaceEx`; Linux and macOS behavior is unchanged.

## [0.3.21] - 2026-11-10

### Added
- **Settings → System** — auto-detects GPU / hardware-decode type from `/dev/dri` and sysfs (Intel VAAPI/QSV, AMD VAAPI, NVIDIA NVDEC, or none). Multiple GPUs are listed with vendor/type, device name when known, whether `/dev/dri` and `renderD128` exist, and whether the current `THUMB_VAAPI` thumbnail path can use it. When no device is found, a short note explains that thumbnails stay on software ffmpeg. Exposed on the existing admin-only `GET /api/admin/system/status` as `gpu`. The ffmpeg VAAPI command is unchanged.

## [0.3.20] - 2026-11-10

### Added
- **System server status** — Settings → System shows live **CPU** (usage percent, load averages, core count), **RAM** (used / total and percent), **user storage** totals (sum of home used bytes vs assigned quotas; users with quota `0` / unlimited are counted separately and are not added into the assigned total), and **DATA_ROOT disk** used / total / free. Values refresh every 5 seconds while the page is open. Data comes from `GET /api/admin/system/status` (admin only).

## [0.3.19] - 2026-11-10

### Changed
- **Storage quota settings page** — the **Apply the same quota to all users** form moved from Settings → System to its own admin Settings page, **Storage quota**, in the same nav as Sessions / Registration / Security / Email. System keeps only DATA_ROOT / homes / shares notes. In-app Users notes now point at Settings → Storage quota. The `/api/admin/settings` fields `globalQuotaEnabled` and `globalQuotaBytes` are unchanged.

## [0.3.18] - 2026-11-10

### Added
- **Global user quota** — Settings → System has **Apply the same quota to all users** and a quota field (same units as the per-user field, e.g. `5GB` / `1G` / `0` = unlimited). Off by default. Turning it on, or changing the value while it is on, updates every existing user’s stored quota in the same save (including admins). New accounts from admin Create user and open registration get that quota. Per-user quota fields are disabled, and the API rejects a per-user quota change, while the setting is on. Turning it off leaves each user’s current quota in place and allows individual edits again. Invalid values are rejected and nothing is written. Quota still counts only each user’s home.

## [0.3.17] - 2026-11-10

### Added
- **Unraid Community Apps template** — `ca_profile.xml` at the repository root and `templates/filehub.xml` (Docker app) so the GitHub repo can be scanned and submitted to Community Apps. The template uses `ghcr.io/ericleung001/nas-filehub:latest`, maps appdata to `/data`, optionally maps Unraid shares to `/data/files/shares`, and includes an optional `/dev/dri` device for Intel VAAPI thumbnails. First boot still leaves `ADMIN_PASSWORD` unset; the generated password is in `admin-credentials.txt` under appdata.
- **GHCR image workflow** — `.github/workflows/docker.yml` builds and publishes `ghcr.io/ericleung001/nas-filehub` on pushes to `main`, version tags `v*`, and manual dispatch (`latest` on the default branch, semver on tags, and a sha tag). The image is `linux/amd64`. A `.dockerignore` keeps the build context free of local data, binaries, and secrets.

## [0.3.16] - 2026-11-10

### Changed
- **Move and Copy** — destination is chosen with a folder picker instead of typing a path. After picking a writable destination (My Files or a shared folder you can write to), browse folders with a breadcrumb, **Up**, and a scrollable folder list. The current location is **This folder** (same as a blank path in earlier versions). Read-only shares are not listed as destinations. The dialog blocks moving or copying a folder into itself or a subfolder. Existing `/api/batch/move` and `/api/batch/copy` requests are unchanged. Copy can start from a read-only folder; Move still requires Allow delete (or owner/admin) on the source.

## [0.3.15] - 2026-11-10

### Added
- **Share delete permission** — grants keep Read or Write, plus a separate **Allow delete** flag. Admins set it in the Grant dialog and on each existing grant (summary e.g. `write, delete` / `read` / `read, delete`). Changing either control saves immediately. The file browser hides Delete and Move when the current share does not allow delete.

### Changed
- Existing write grants keep delete (migration sets `can_delete=1` for `perm=write`); existing read grants stay without delete. New grants default Allow delete off in the UI. The API accepts and returns `canDelete`. Write without delete can still upload, create, and rename; delete and move require the flag. Read + delete can view/download and delete, but not write. Home folders and admins keep full delete rights. `POST /api/delete`, `POST /api/batch/delete`, and moves are rejected with 403 when the flag is off.
- **About** — the Settings → About description is only: “Filehub is a lightweight, self-hosted file manager for sharing folders among users on your own server.”

## [0.3.14] - 2026-11-10

### Added
- **Link shares multi-select delete** — checkbox per row, header select-all, and **Delete selected** (enabled when at least one is checked). Confirms with “Delete N link(s)? This cannot be undone.” then hard-deletes via existing `DELETE /api/links/:id`. Partial failures are reported; the list refreshes and the selection is cleared. Single-row **Revoke** is unchanged. Admins can still delete any listed link; non-admins only their own (same API auth as today).

### Changed
- **Shared folders grants** — Grant no longer asks for a numeric user ID in a `prompt()` (typing a username became `NaN` and the request was skipped, so the other user never received a grant and could not see the share). A dialog lists users in a dropdown, then **Read** or **Write** (write includes read). Current grants show username + permission in the table and dialog, and can be changed or revoked. The API rejects unknown users and normalizes permission values.

### Fixed
- **Users storage bar** — the usage track no longer overflows from the Storage column under Status / Actions. Cause: `display: grid` on the Actions `td` took that cell out of table layout, so the quota bar was sized too wide and painted across later columns. The Actions cell stays `table-cell`; Edit/Del alignment uses an inner two-slot grid. The Storage cell clips overflow on desktop and mobile.

## [0.3.13] - 2026-11-10

### Added
- **App icon** — favicon (`/favicon.ico`, 32 and 192 PNG) and Apple touch icon; the Filehub mark appears on the login, register, forgot-password, reset, and public share pages, and in the top bar after sign-in.
- **Settings → About** — visible to every signed-in user: short project description, Apache-2.0, GitHub link, and a Buy Me a Coffee button (`https://buymeacoffee.com/filehub`).

## [0.3.12] - 2026-11-10

### Fixed
- **Share link dialog** — after creating a link, the create form is actually hidden (`[hidden]` now overrides `.admin-form { display: flex }`). Success shows **Link created**, the URL, a primary **Copy URL** button, and **Done**. Submit is disabled while the request is in flight to prevent duplicate links.

## [0.3.11] - 2026-11-10

### Fixed
- **Mobile file toolbar** — `.actions` wraps on narrow screens so **Upload** and **Refresh** stay fully visible (no longer clipped by `main` overflow). Action controls do not shrink below their content.
- **Mobile settings nav** — horizontal scrollable chip row with consistent height (no tall stretched Account box / uneven wrap gaps when many admin tabs are present).
- **Mobile Link shares table** — Actions column keeps real `table-cell` layout so **Copy** / **Revoke** stay under Actions instead of dropping under the label.

## [0.3.10] - 2026-11-10

### Changed
- **Admin settings nav** — split the long System page into separate admin-only tabs: **Sessions**, **Registration**, **Security** (Force 2FA + password policy), **Email** (SMTP), and a short **System** notes page (DATA_ROOT / homes / shares / quota).
- **Revoke link share** — DELETE `/api/links/:id` hard-deletes the row (no longer soft-revokes). The Link shares list excludes already-revoked leftovers and removes the row after Revoke.

### Fixed
- **Link shares table layout** — stopped applying `display: block` to Label/Target/URL table cells (that stacked columns). Ellipsis stays on the cells and inner `a`/`code`. Widened the Actions column for Copy + Revoke.

## [0.3.9] - 2026-11-10

### Added
- **Admin session duration** — Settings → System: choose normal session lifetime (1 hour–30 days presets; default **7 days**) and optionally allow **Stay signed in** with a longer lifetime (7–365 days presets; default **30 days**). Stored in settings (`session_ttl_seconds`, `remember_me_ttl_seconds`, `allow_remember_me`). Applies to **new logins only**.
- **Stay signed in** — login checkbox when allowed; `POST /api/login` accepts `rememberMe`; the flag is kept on the pending 2FA/enroll challenge so TOTP success uses the longer TTL. Crafted `rememberMe: true` is ignored when the admin toggle is off. `GET /api/auth/features` exposes `allowRememberMe`.

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

[Unreleased]: https://github.com/ericleung001/nas-filehub/compare/v0.3.22...HEAD
[0.3.22]: https://github.com/ericleung001/nas-filehub/compare/v0.3.21...v0.3.22
[0.3.21]: https://github.com/ericleung001/nas-filehub/compare/v0.3.20...v0.3.21
[0.3.20]: https://github.com/ericleung001/nas-filehub/compare/v0.3.19...v0.3.20
[0.3.19]: https://github.com/ericleung001/nas-filehub/compare/v0.3.18...v0.3.19
[0.3.18]: https://github.com/ericleung001/nas-filehub/compare/v0.3.17...v0.3.18
[0.3.17]: https://github.com/ericleung001/nas-filehub/compare/v0.3.16...v0.3.17
[0.3.16]: https://github.com/ericleung001/nas-filehub/compare/v0.3.15...v0.3.16
[0.3.15]: https://github.com/ericleung001/nas-filehub/compare/v0.3.14...v0.3.15
[0.3.14]: https://github.com/ericleung001/nas-filehub/compare/v0.3.13...v0.3.14
[0.3.13]: https://github.com/ericleung001/nas-filehub/compare/v0.3.12...v0.3.13
[0.3.12]: https://github.com/ericleung001/nas-filehub/compare/v0.3.11...v0.3.12
[0.3.11]: https://github.com/ericleung001/nas-filehub/compare/v0.3.10...v0.3.11
[0.3.10]: https://github.com/ericleung001/nas-filehub/compare/v0.3.9...v0.3.10
[0.3.9]: https://github.com/ericleung001/nas-filehub/compare/v0.3.8...v0.3.9
[0.3.8]: https://github.com/ericleung001/nas-filehub/compare/v0.3.7...v0.3.8
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
