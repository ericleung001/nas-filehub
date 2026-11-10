package auth

import (
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/http"
	"strings"
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

// TrustedProxies is a set of IP addresses or CIDRs whose X-Forwarded-For
// and X-Real-IP headers may be honored.
type TrustedProxies struct {
	nets []*net.IPNet
}

// ParseTrustedProxies always trusts loopback (127.0.0.1 and ::1) and also
// any comma-separated IPs or CIDRs in extra (from TRUSTED_PROXIES).
func ParseTrustedProxies(extra string) *TrustedProxies {
	items := []string{"127.0.0.1/32", "::1/128"}
	for _, p := range strings.Split(extra, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if strings.Contains(p, "/") {
			items = append(items, p)
			continue
		}
		ip := net.ParseIP(p)
		if ip == nil {
			continue
		}
		if ip.To4() != nil {
			items = append(items, ip.String()+"/32")
		} else {
			items = append(items, ip.String()+"/128")
		}
	}
	tp := &TrustedProxies{}
	for _, cidr := range items {
		_, n, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		tp.nets = append(tp.nets, n)
	}
	return tp
}

func (t *TrustedProxies) Contains(ip net.IP) bool {
	if t == nil || ip == nil {
		return false
	}
	for _, n := range t.nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func (t *TrustedProxies) ContainsIP(s string) bool {
	return t.Contains(parseIP(s))
}

func peerIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func parseIP(s string) net.IP {
	return net.ParseIP(strings.TrimSpace(s))
}

// ClientIP returns the immediate peer address, unless that peer is a trusted
// proxy, in which case X-Forwarded-For (then X-Real-IP) is walked from the
// right, skipping trusted hops, to find the first untrusted client address.
// Headers from an untrusted peer are ignored to prevent spoofing.
func ClientIP(r *http.Request, trusted *TrustedProxies) string {
	peer := peerIP(r)
	peerParsed := parseIP(peer)
	if trusted == nil || peerParsed == nil || !trusted.Contains(peerParsed) {
		return peer
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		// Walk from the right; skip trusted hops; take the first untrusted.
		for i := len(parts) - 1; i >= 0; i-- {
			ip := parseIP(parts[i])
			if ip == nil {
				continue
			}
			if trusted.Contains(ip) {
				continue
			}
			return ip.String()
		}
		// Every hop was trusted; use the leftmost (original client).
		for _, p := range parts {
			if ip := parseIP(p); ip != nil {
				return ip.String()
			}
		}
	}
	if rip := r.Header.Get("X-Real-IP"); rip != "" {
		if ip := parseIP(rip); ip != nil {
			return ip.String()
		}
	}
	return peer
}

// LoginLimiter is a simple per-IP fixed-window rate limiter.
type LoginLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	hits   map[string][]time.Time
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
