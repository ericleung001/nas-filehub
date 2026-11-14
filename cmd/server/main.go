package main

import (
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/eric/nas-filehub/internal/api"
	"github.com/eric/nas-filehub/internal/auth"
	"github.com/eric/nas-filehub/internal/config"
	"github.com/eric/nas-filehub/internal/db"
	"github.com/eric/nas-filehub/internal/files"
	"github.com/eric/nas-filehub/internal/secretbox"
	"github.com/eric/nas-filehub/internal/thumb"
	"github.com/eric/nas-filehub/internal/version"
	"github.com/eric/nas-filehub/web"
)

func main() {
	cfg := config.Load()
	if cfg.AdminPassword == "" {
		log.Println("WARN: ADMIN_PASSWORD not set; admin seed skipped if no admin exists")
	}
	secretFile := filepath.Join(filepath.Dir(cfg.DBPath), ".session-secret")
	loaded, fromFile, err := config.LoadOrCreateSessionSecret(cfg.SessionSecret, secretFile)
	if err != nil {
		log.Println("WARN: could not persist a session secret file; TOTP secrets may stay unencrypted:", err)
	} else if loaded != "" {
		cfg.SessionSecret = loaded
		if fromFile {
			log.Println("WARN: SESSION_SECRET empty; using a persistent key file next to the database to encrypt 2FA secrets at rest")
		}
	} else if cfg.SessionSecret == "" {
		log.Println("WARN: SESSION_SECRET empty; sessions still use random tokens stored in SQLite")
	}

	if err := os.MkdirAll(cfg.DataRoot, 0o755); err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(cfg.DBPath), 0o755); err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(cfg.ThumbCacheDir, 0o755); err != nil {
		log.Fatal(err)
	}

	database, err := db.Open(cfg.DBPath)
	if err != nil {
		log.Fatal(err)
	}
	defer database.Close()

	if cfg.SessionSecret != "" {
		database.SetTotpKey(secretbox.DeriveKey(cfg.SessionSecret))
		if err := database.ReencryptLegacyTotpSecrets(); err != nil {
			log.Println("WARN: could not migrate legacy TOTP secrets:", err)
		}
	}
	if err := database.EnsureAdmin(cfg.AdminUser, cfg.AdminPassword); err != nil {
		log.Fatal("seed admin:", err)
	}
	// Ensure admin home exists
	if u, _ := database.GetUserByUsername(cfg.AdminUser); u != nil {
		if abs, err := files.SafeJoin(cfg.DataRoot, u.HomePath); err == nil {
			_ = files.EnsureDir(abs)
		}
	}

	go func() {
		t := time.NewTicker(time.Hour)
		for range t.C {
			database.CleanupSessions()
		}
	}()

	staticRoot, err := fs.Sub(web.Static, "static")
	if err != nil {
		log.Fatal(err)
	}

	srv := &api.Server{
		Cfg:          cfg,
		DB:           database,
		Thumbs:       &thumb.Generator{CacheDir: cfg.ThumbCacheDir, Enable: cfg.ThumbEnable, VAAPI: cfg.ThumbVAAPI, Mode: thumb.ParseMode(cfg.ThumbHWAccel, cfg.ThumbVAAPI)},
		Limiter:      auth.NewLoginLimiter(cfg.RateLimitPerMin),
		ResetLimiter: auth.NewLoginLimiter(5), // password-reset requests per IP per minute
		Static:       staticRoot,
		Uploads:      api.NewUploadManager(cfg.DataRoot, 24*time.Hour),
	}
	go func() {
		t := time.NewTicker(time.Hour)
		for range t.C {
			n := srv.Uploads.Cleanup()
			if n > 0 {
				log.Printf("cleaned %d expired upload session(s)", n)
			}
		}
	}()
	if cfg.WebDAVEnabled {
		log.Printf("WebDAV enabled at /dav/")
	}

	log.Printf("nas-filehub %s listening on %s (data=%s)", version.Version, cfg.ListenAddr, cfg.DataRoot)
	if err := http.ListenAndServe(cfg.ListenAddr, srv.Handler()); err != nil {
		log.Fatal(err)
	}
}
