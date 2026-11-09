package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	ListenAddr      string
	DataRoot        string
	DBPath          string
	AdminUser       string
	AdminPassword   string
	SessionSecret   string
	SecureCookies   bool
	ThumbEnable     bool
	ThumbVAAPI      bool
	ThumbCacheDir   string
	MaxUploadMB     int64
	RateLimitPerMin int
	Force2FAAll     bool // env FORCE_2FA_ALL; OR with DB setting force_2fa_all
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func getenvBool(k string, def bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(k)))
	if v == "" {
		return def
	}
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

func getenvInt(k string, def int) int {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func Load() Config {
	dataRoot := getenv("DATA_ROOT", "/data/files")
	dbPath := getenv("DB_PATH", "/data/filehub.db")
	return Config{
		ListenAddr:      getenv("LISTEN_ADDR", ":8080"),
		DataRoot:        dataRoot,
		DBPath:          dbPath,
		AdminUser:       getenv("ADMIN_USER", "admin"),
		AdminPassword:   getenv("ADMIN_PASSWORD", ""),
		SessionSecret:   getenv("SESSION_SECRET", ""),
		SecureCookies:   getenvBool("SECURE_COOKIES", false),
		ThumbEnable:     getenvBool("THUMB_ENABLE", true),
		ThumbVAAPI:      getenvBool("THUMB_VAAPI", false),
		ThumbCacheDir:   getenv("THUMB_CACHE_DIR", "/data/thumbs"),
		MaxUploadMB:     int64(getenvInt("MAX_UPLOAD_MB", 2048)),
		RateLimitPerMin: getenvInt("LOGIN_RATE_LIMIT", 10),
		Force2FAAll:     getenvBool("FORCE_2FA_ALL", false),
	}
}

func (c Config) SessionTTL() time.Duration {
	return 7 * 24 * time.Hour
}
