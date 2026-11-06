package auth

import (
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/http"
	"sync"
	"time"
)

const CookieName = "filehub_session"

func NewToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// LoginLimiter is a simple per-IP fixed-window rate limiter.
type LoginLimiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	hits    map[string][]time.Time
}

func NewLoginLimiter(perMin int) *LoginLimiter {
	if perMin <= 0 {
		perMin = 10
	}
	return &LoginLimiter{
		limit:  perMin,
		window: time.Minute,
		hits:   map[string][]time.Time{},
	}
}

func (l *LoginLimiter) Allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	cutoff := now.Add(-l.window)
	arr := l.hits[ip]
	kept := arr[:0]
	for _, t := range arr {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.limit {
		l.hits[ip] = kept
		return false
	}
	kept = append(kept, now)
	l.hits[ip] = kept
	return true
}
