# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html)
where version tags are used.

## [Unreleased]

## [0.4.14] - 2026-11-14

### Fixed
- **Shared folder nesting** — opening a folder that was shared with you under **Shared with me** now lists every level of subfolders and files, not just the first level. Listing used to return paths under the owner's tree (for example `temp/chi` instead of `{share id}/chi`), so opening a child folder such as `chi` failed and its contents looked missing. Paths stay inside the shared folder; a recipient still cannot walk out of that root. Admin-managed shared folders already listed nested items and keep doing so. A write grant still allows creating folders and files at any depth inside the share.

## [0.4.13] - 2026-11-14

### Changed
- **Upload size limit** — the default `MAX_UPLOAD_MB` is now 102400 (100 GiB), so files larger than the previous 2 GiB cap (including 3 GiB uploads) are accepted. Set `MAX_UPLOAD_MB=0` to disable the application cap and rely on user or storage quota. The web UI still uses chunked, resumable uploads for files of 1 MiB or more, so each request stays small. Rejection messages now include the configured maximum, for example “upload too large (max 100 GiB)”.

### Fixed
- **Share public-link overflow** — in the Share dialog, Current access no longer lets a long public URL collide with the Copy and Delete buttons on a narrow or mobile layout. The URL is shown on its own line and truncated with an ellipsis; Copy still copies the full URL. The full address remains available from the tooltip. Settings → Link shares already truncated the URL in that table.

### Documentation
- README, contributing notes, the Unraid Community Apps template overview, changelog compare links, and Settings → System quota help now match the shipped v0.4.13 behavior (including quota accounting for share writes and the Recycle bin, user-to-user share email, WebDAV, and the 100 GiB upload cap).

## [0.4.12] - 2026-11-14

### Changed
- **Share user search** — the Share dialog always shows the keyword field, including when there are five or fewer eligible users. Typing still filters the list by username or email; selected people stay visible. With more than five users, the default list is still your five most frequent recipients.

### Fixed
- **Re-sharing with an existing recipient** — opening Share on a file or folder that was already granted to a user, then selecting that same user again, no longer fails with a Bad Gateway. The existing grant is updated (or left as-is if nothing changed) and the request succeeds.

## [0.4.11] - 2026-11-14

### Changed
- **Share user picker** — choosing people in the Share dialog is a vertical list. If there are more than five eligible users, the list opens with your five most frequent recipients (how often you have granted them an item share, then the most recent grant). Remaining default slots are filled A–Z by username. Search by username or email to find anyone else; selected people stay visible so they can be unchecked. With five or fewer users, the full list is shown.

## [0.4.10] - 2026-11-14

### Added
- **Share with users** — from the file manager, Share on a file or folder can grant access to one or more Filehub users, create a public link (optional expiry and password), or both. Recipients see the items under a **Shared with me** sidebar root (also in WebDAV). Default access is read; folders can be granted write. The owner or an admin can revoke a user grant from the same Share dialog. You can only share items you can already read, and not from the Recycle bin or from Shared with me. Recipients with an email address are notified when SMTP is configured; missing SMTP or a missing address does not fail the share.
- **Per-user quota override** — Settings → Storage quota remains the default for users without a custom limit, including new accounts. Admins can still set a different quota for an individual user under Users → Edit, even while the default is on. The Users table shows whether a quota is default or custom.

### Changed
- **Recycle bin toolbar** — Folder, Upload, and Upload folder are hidden while the Recycle bin is open (restore and empty actions stay).

### Fixed
- **Active sessions and Audit log** — opening those Settings tabs did not load any rows because the refresh functions were scoped inside an unrelated search/trash initializer and were never called. Both lists now load; the current session is listed and can be distinguished from other devices; audit events already being recorded (sign-in, public links, share grants, and the new user-to-user shares) are shown. A fetch error is displayed in the table instead of leaving it blank.

## [0.4.9] - 2026-11-14

### Added
- **Folder upload** — Upload folder (directory file picker) and drag-and-drop of directories. Intermediate folders are created as needed, empty folders are created, and the relative folder structure is preserved. Existing folders are merged; name conflicts on files still use Skip / Overwrite / Rename, including Apply to all.
- **Resumable uploads** — large uploads (1 MiB and above in the web UI) use a chunked session (`/api/upload/sessions`) so a dropped connection can resume from the last complete chunk instead of starting over. Incomplete sessions expire after 24 hours and are cleaned up. Conflict policy is decided before the file is streamed.
- **WebDAV** — mount Filehub at `/dav/` with your Filehub username and password. The virtual root lists My Files and granted shared folders, with the same permission and quota rules as the web UI. WebDAV DELETE moves items to the Recycle bin. Enabled by default; set `WEBDAV_ENABLED=false` to disable. No anonymous access.
- **SMTP notifications** — when SMTP is configured, administrators who have an email address are notified if a new open-registration account is waiting for approval, and a user is notified when an admin grants them access to a shared folder (if that user has an email). Send failures are logged and do not fail the original action. If SMTP is not configured or the recipient has no email, nothing is sent.
- **Unraid User files path** — the Community Apps template now includes an optional **User files** mapping (default host `/mnt/user/nas-filehub` → container `/data/files`) so application data and user files can be split without adding a path by hand.

### Changed
- **Overwrite to Recycle bin** — uploading with Overwrite no longer replaces the existing file in place. The current file is moved to the Recycle bin with the same rules as a normal delete, then the new file is written. If the Recycle bin move fails, the original file is left in place. Overwriting a folder is still rejected. Overwrite also requires delete permission (a write-only share grant cannot overwrite). Trashed bytes still count toward the owner’s quota until purged.
- **TrueNAS catalog docs** — README and `truenas/` no longer describe the live Market listing as 0.4.4 or a pending [truenas/apps#6466](https://github.com/truenas/apps/pull/6466) update. Filehub is listed on the [TrueNAS Apps Market](https://apps.truenas.com/catalog/filehub_community/); already-published versions follow new releases automatically, so version-bump pull requests are not needed.
- **Session duration** — the admin settings page that controls login lifetime and Stay signed in is named **Session duration**, so it is not confused with **Active sessions** (the per-user device list).

### Fixed
- **WebDAV listing in Finder** — authenticated WebDAV traffic no longer shares the login rate limit (10 requests per IP per minute). macOS Finder sends many PROPFIND/GET requests when opening a folder; those used to return 429 and the folder appeared empty. Failed passwords are still rate-limited.
- **Bulk selection bar** — the multi-select action bar is hidden unless at least one item is selected, so it no longer appears as “0 selected”.

## [0.4.8] - 2026-11-12

### Added
- **Upload name conflicts** — when a file with the same name and extension already exists at the destination, the uploader can **Skip**, **Overwrite**, or **Rename**. Rename appends `_1` before the extension (`photo.jpg` → `photo_1.jpg`); if that name is also taken the number increases (`photo_2.jpg`, …). The incoming filename is the base, so uploading `photo_1.jpg` when it exists becomes `photo_1_1.jpg`. A batch of conflicts can be resolved per file or with **Apply to all**. The same choices apply to the Upload button, drag-and-drop, and multi-file uploads, including writable shared folders. The API enforces the policy (`conflict=skip|overwrite|rename` on `POST /api/upload`); a collision without a policy returns 409. Skip does not change the existing file. Overwrite replaces it in place (same as the previous silent replace; the existing file is not moved to the recycle bin).

## [0.4.7] - 2026-11-11

### Fixed
- **Unique registration email** — open registration (with or without admin approval) rejects an email that is already used by an active, pending, or disabled account. The same check applies when an administrator creates or edits a user. After a pending account is rejected and deleted, that email can be used again. Blank emails on admin-created users remain allowed on more than one account.
- **Pending sign-up message** — when registration requires admin approval, the sign-up page now shows “Your account is waiting for admin approval” after a successful request. The confirmation previously sat inside the form and disappeared when the form was hidden.


## [0.4.6] - 2026-11-11

### Fixed
- **Users table Actions** — the Actions column is wide enough to keep Edit, Approve, Reject, and Del on one row, instead of wrapping when a pending user has extra buttons.


## [0.4.5] - 2026-11-11

### Fixed
- **Public share password field** — opening a public share link that has no password no longer shows the password unlock form. A CSS rule on `.panel form` was overriding the HTML `hidden` attribute.

### Added
- **README screenshots** — the GitHub README now includes current login and file-browser screenshots.
- **TrueNAS Apps listing in the README** — the GitHub README now documents the official [TrueNAS Apps Market](https://apps.truenas.com/catalog/filehub_community/) Community listing and how to install Filehub from the TrueNAS Apps UI.

### Changed
- **TrueNAS Community App packaging** — revised `truenas/` to match catalog conventions after review: required initial admin password, configurable run-as user and group (default 568), boolean environment values, user-provided WebUI listen port, and VAAPI thumbnails when an Intel or AMD GPU is selected. The official catalog image pin remains `0.4.4`. The default WebUI port is `30568` so it does not collide with other community apps.
- **Published image tag examples** — README pull and compose examples now use `0.4.5`.

## [0.4.4] - 2026-11-10

### Fixed
- **Mobile Settings spacing** — on narrow screens the Settings section tabs no longer stretch to fill the viewport, which had left large empty gaps above and below the tab row.

## [0.4.3] - 2026-11-10

### Added
- **Trusted proxy client IP** — login, password-reset, and share-link unlock rate limits use the real client address when the immediate peer is loopback or a host listed in `TRUSTED_PROXIES` (comma-separated IPs or CIDRs). `X-Forwarded-For` / `X-Real-IP` from an untrusted peer are ignored.
- **Share-link unlock rate limit** — password-protected public link unlocks use the same per-IP limiter as login (`LOGIN_RATE_LIMIT`, default 10/minute) and return 429 when exceeded.
- **Active sessions** — Settings → Active sessions lists the signed-in user’s sessions (device, IP, signed-in and last-seen times) and can revoke one session or all other sessions. Changing a password still signs out other sessions. Session tokens are never returned to the client.
- **Audit log** — Settings → Audit log (admins) records sign-in success and failure (no passwords), public link create/revoke, shared-folder grant changes, and password-reset requests (no tokens). The log is paginated.
- **Per-account lockout** — after `LOGIN_LOCKOUT_LIMIT` failed logins for a username (default 10), that username is locked for `LOGIN_LOCKOUT_MINUTES` (default 15). Further attempts during the lock do not extend the window. Lock state is stored in the database so it survives a process restart. The per-IP limiter still applies.

### Changed
- **Dangerous file previews** — `.html`, `.htm`, `.svg`, `.xml`, `.js`, and similar scriptable types are served as attachments with `Content-Type: application/octet-stream` (plus `X-Content-Type-Options: nosniff`). Images, video, and PDF can still be previewed inline. `Content-Disposition` filenames use RFC 5987 encoding.
- **Password-reset URLs** — when `PUBLIC_BASE_URL` is unset, reset links use `Host` and never `X-Forwarded-Host`. `X-Forwarded-Proto` is used only when the immediate peer is a trusted proxy. A warning is logged if a reset email is sent without `PUBLIC_BASE_URL`. Public HTTPS deployments must set `PUBLIC_BASE_URL` and `SECURE_COOKIES=true`.
- **TOTP secrets at rest** — `users.totp_secret` is stored as AES-GCM ciphertext (`v1:`) using a key derived from `SESSION_SECRET` (or a persistent key file next to the database if the env is empty). Existing plaintext secrets are migrated on startup or on the next successful write/enable path. Backup codes remain bcrypt-hashed.
- **Security headers** — HTML responses add a document Content-Security-Policy (`default-src` / `script-src` / `style-src 'self'`, `object-src 'none'`, `base-uri 'self'`, `form-action 'self'`, plus `img`/`media`/`frame-src` for previews), `Permissions-Policy` (camera, microphone, and geolocation disabled), and `Cross-Origin-Opener-Policy: same-origin`. HSTS is not set by the app; set it on the reverse proxy for public HTTPS. `/api/raw`, `/api/thumb`, and public raw responses still allow same-origin framing for previews.

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

[Unreleased]: https://github.com/ericleung001/nas-filehub/compare/v0.4.14...HEAD
[0.4.14]: https://github.com/ericleung001/nas-filehub/compare/v0.4.13...v0.4.14
[0.4.13]: https://github.com/ericleung001/nas-filehub/compare/v0.4.12...v0.4.13
[0.4.12]: https://github.com/ericleung001/nas-filehub/compare/v0.4.11...v0.4.12
[0.4.11]: https://github.com/ericleung001/nas-filehub/compare/v0.4.10...v0.4.11
[0.4.10]: https://github.com/ericleung001/nas-filehub/compare/v0.4.9...v0.4.10
[0.4.9]: https://github.com/ericleung001/nas-filehub/compare/v0.4.8...v0.4.9
[0.4.8]: https://github.com/ericleung001/nas-filehub/compare/v0.4.7...v0.4.8
[0.4.7]: https://github.com/ericleung001/nas-filehub/compare/v0.4.6...v0.4.7
[0.4.6]: https://github.com/ericleung001/nas-filehub/compare/v0.4.5...v0.4.6
[0.4.5]: https://github.com/ericleung001/nas-filehub/compare/v0.4.4...v0.4.5
[0.4.4]: https://github.com/ericleung001/nas-filehub/compare/v0.4.3...v0.4.4
[0.4.3]: https://github.com/ericleung001/nas-filehub/compare/v0.4.2...v0.4.3
[0.4.2]: https://github.com/ericleung001/nas-filehub/compare/v0.4.1...v0.4.2
[0.4.1]: https://github.com/ericleung001/nas-filehub/compare/v0.4.0...v0.4.1
[0.4.0]: https://github.com/ericleung001/nas-filehub/compare/v0.3.22...v0.4.0
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
