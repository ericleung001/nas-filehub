# NAS FileHub

輕量多用戶檔案瀏覽器（似 File Browser，多用戶似 Nextcloud）。  
Lightweight multi-user file manager — Go single binary + embedded web UI + SQLite.

**Apache-2.0**

## 功能 Features

- 多用戶，每人獨立 home（`DATA_ROOT/homes/<user>`）
- Shared folders：`read` / `write` 權限
- 瀏覽、上傳、下載、新建資料夾、重新命名、刪除
- 圖片預覽、HTML5 影片播放（瀏覽器端解碼）
- 可選 ffmpeg 縮圖（軟件；Unraid J4125 可開 VAAPI）
- **無** command / shell 執行入口
- bcrypt 密碼、HttpOnly session cookie、登入 rate limit

## 快速開始 Quick start

### Docker Compose（建議）

```bash
cp .env.example .env   # 改 ADMIN_PASSWORD / SESSION_SECRET
mkdir -p data/files data/db
docker compose up -d --build
```

瀏覽器開：<http://127.0.0.1:8088>  
預設只綁 `127.0.0.1`（畀 Nginx Proxy Manager 反代）。

### 本機跑（開發）

```bash
export DATA_ROOT=./data/files
export DB_PATH=./data/filehub.db
export THUMB_CACHE_DIR=./data/thumbs
export ADMIN_USER=admin
export ADMIN_PASSWORD='your-strong-password'
export LISTEN_ADDR=:8080
go run ./cmd/server
```

## 環境變數 Env

| Var | Default | 說明 |
|-----|---------|------|
| `LISTEN_ADDR` | `:8080` | HTTP listen |
| `DATA_ROOT` | `/data/files` | 檔案根目錄 |
| `DB_PATH` | `/data/filehub.db` | SQLite |
| `ADMIN_USER` / `ADMIN_PASSWORD` | admin / (必填首次) | 首次開機 seed admin |
| `SESSION_SECRET` | — | 建議設定（token 本身隨機存 DB） |
| `SECURE_COOKIES` | `false` | 經 HTTPS／NPM 時設 `true` |
| `THUMB_ENABLE` | `true` | ffmpeg 縮圖 |
| `THUMB_VAAPI` | `false` | Intel VAAPI 硬解縮圖 |
| `THUMB_CACHE_DIR` | `/data/thumbs` | 縮圖快取 |
| `LOGIN_RATE_LIMIT` | `10` | 每 IP 每分鐘登入次數 |
| `MAX_UPLOAD_MB` | `2048` | 上傳上限 |

## Nginx Proxy Manager

1. FileHub 只聽內網／`127.0.0.1:8088`
2. NPM 新增 Proxy Host → 你的 domain → forward `http://<unraid-ip>:8088`（或 Docker network 內 hostname）
3. 開 SSL（Let's Encrypt）
4. 建議加 Access List 或 Authelia／Cloudflare Access
5. 設 `SECURE_COOKIES=true`

**唔好** 將 `:8080` 直接 port-forward 出公網。

## Unraid + J4125 硬解縮圖

瀏覽器播片／睇圖多數用**客戶端**硬解；NAS 端硬解主要幫 **ffmpeg 產生縮圖／預覽**，減 CPU。

`docker-compose.yml` 取消註解：

```yaml
devices:
  - /dev/dri:/dev/dri
environment:
  THUMB_VAAPI: "true"
```

Unraid 確認 `/dev/dri/renderD128` 存在。VAAPI 失敗會自動 fallback 軟件 ffmpeg；再失敗則直接用原圖。

（真正「轉碼串流」屬 Jellyfin 類產品；本專案刻意保持輕量。）

## Admin 用法

登入 admin → **Admin**：

1. Create user（自動建 home）
2. Create share（路徑相對 `DATA_ROOT`，例如 `shared/family`）
3. Grant → 填 user id + `read`/`write`

User A **睇唔到** User B 嘅 home；只見到自己 home + 被授權嘅 shares。

## 保安預設

- 無 remote command execution
- bcrypt passwords
- Session cookie：`HttpOnly`、`SameSite=Lax`、可開 `Secure`
- Login rate limit
- Path traversal 防護（所有檔案操作限於 scope root）
- 容器以非 root `filehub` (uid 1000) 跑

## API 摘要

- `POST /api/login` `POST /api/logout` `GET /api/me`
- `GET /api/roots` `GET /api/list`
- `POST /api/mkdir|rename|delete|upload`
- `GET /api/download` `GET /api/raw` `GET /api/thumb`
- Admin: `/api/admin/users` `/api/admin/shares`

## 授權 License

Apache License 2.0 — see [LICENSE](LICENSE).
