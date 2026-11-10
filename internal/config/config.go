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
	ThumbHWAccel    string // auto|off|vaapi|nvdec; empty defers to ThumbVAAPI
	ThumbCacheDir   string
	MaxUploadMB     int64
	RateLimitPerMin int
	Force2FAAll     bool   // env FORCE_2FA_ALL; OR with DB setting force_2fa_all
	PublicBaseURL   string // PUBLIC_BASE_URL for password-reset links (optional)
	// TrustedProxies is the raw TRUSTED_PROXIES value (comma-separated IPs or CIDRs).
	// Loopback is always trusted in addition to this list.
	TrustedProxies string
	LockoutLimit   int // LOGIN_LOCKOUT_LIMIT; failed logins per username before lock
	LockoutMinutes int // LOGIN_LOCKOUT_MINUTES; lock window (further attempts do not extend it)
	// Optional SMTP env overrides (take precedence over DB settings when host set)
	SMTPHost     string
	SMTPPort     int
	SMTPUsername string
	SMTPPassword string
	SMTPFrom     string
	SMTPEnabled  bool // SMTP_ENABLED; if env host set, may force enable
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
		ThumbHWAccel:    strings.ToLower(strings.TrimSpace(getenv("THUMB_HWACCEL", ""))),
		ThumbCacheDir:   getenv("THUMB_CACHE_DIR", "/data/thumbs"),
		MaxUploadMB:     int64(getenvInt("MAX_UPLOAD_MB", 2048)),
		RateLimitPerMin: getenvInt("LOGIN_RATE_LIMIT", 10),
		Force2FAAll:     getenvBool("FORCE_2FA_ALL", false),
		PublicBaseURL:   strings.TrimRight(getenv("PUBLIC_BASE_URL", ""), "/"),
		TrustedProxies:  getenv("TRUSTED_PROXIES", ""),
		LockoutLimit:    getenvInt("LOGIN_LOCKOUT_LIMIT", 10),
		LockoutMinutes:  getenvInt("LOGIN_LOCKOUT_MINUTES", 15),
		SMTPHost:        getenv("SMTP_HOST", ""),
		SMTPPort:        getenvInt("SMTP_PORT", 0),
		SMTPUsername:    getenv("SMTP_USERNAME", ""),
		SMTPPassword:    getenv("SMTP_PASSWORD", ""),
		SMTPFrom:        getenv("SMTP_FROM", ""),
		SMTPEnabled:     getenvBool("SMTP_ENABLED", false),
	}
}

func (c Config) SessionTTL() time.Duration {
	return 7 * 24 * time.Hour
}

func (c Config) EffectiveLockoutLimit() int {
	if c.LockoutLimit <= 0 {
		return 10
	}
	return c.LockoutLimit
}

func (c Config) EffectiveLockoutWindow() time.Duration {
	m := c.LockoutMinutes
	if m <= 0 {
		m = 15
	}
	return time.Duration(m) * time.Minute
}
