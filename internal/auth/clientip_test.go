package auth

import (
	"net/http"
	"testing"
)

func req(remote string, hdr map[string]string) *http.Request {
	r, _ := http.NewRequest(http.MethodGet, "http://example.local/", nil)
	r.RemoteAddr = remote
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	return r
}

func TestClientIPIgnoresSpoofedHeadersFromUntrustedPeer(t *testing.T) {
	trusted := ParseTrustedProxies("")
	r := req("203.0.113.10:51234", map[string]string{
		"X-Forwarded-For": "1.2.3.4, 127.0.0.1",
		"X-Real-IP":       "9.9.9.9",
	})
	got := ClientIP(r, trusted)
	if got != "203.0.113.10" {
		t.Fatalf("untrusted peer must ignore forwarded headers: got %q", got)
	}
}

func TestClientIPHonorsForwardedFromLoopback(t *testing.T) {
	trusted := ParseTrustedProxies("")
	r := req("127.0.0.1:8080", map[string]string{
		"X-Forwarded-For": "198.51.100.20, 127.0.0.1",
	})
	got := ClientIP(r, trusted)
	if got != "198.51.100.20" {
		t.Fatalf("got %q want 198.51.100.20", got)
	}
}

func TestClientIPWalksFromTheRight(t *testing.T) {
	trusted := ParseTrustedProxies("10.0.0.0/8")
	// client, trusted hop, trusted hop (rightmost is the immediate peer, already checked)
	r := req("10.0.0.2:80", map[string]string{
		"X-Forwarded-For": "203.0.113.9, 10.0.0.8, 10.0.0.2",
	})
	got := ClientIP(r, trusted)
	if got != "203.0.113.9" {
		t.Fatalf("got %q want 203.0.113.9", got)
	}
}

func TestClientIPFallbackXRealIP(t *testing.T) {
	trusted := ParseTrustedProxies("")
	r := req("[::1]:443", map[string]string{
		"X-Real-IP": "192.0.2.44",
	})
	got := ClientIP(r, trusted)
	if got != "192.0.2.44" {
		t.Fatalf("got %q want 192.0.2.44", got)
	}
}

func TestClientIPNoHeadersUsesPeer(t *testing.T) {
	trusted := ParseTrustedProxies("")
	r := req("127.0.0.1:9", nil)
	got := ClientIP(r, trusted)
	if got != "127.0.0.1" {
		t.Fatalf("got %q", got)
	}
}

func TestClientIPNilTrustedUsesPeer(t *testing.T) {
	r := req("127.0.0.1:9", map[string]string{"X-Forwarded-For": "1.2.3.4"})
	got := ClientIP(r, nil)
	if got != "127.0.0.1" {
		t.Fatalf("nil trusted must not honor headers: got %q", got)
	}
}

func TestParseTrustedProxiesSkipsInvalid(t *testing.T) {
	tp := ParseTrustedProxies("not-an-ip, 192.0.2.1, 198.51.100.0/24")
	if !tp.Contains(parseIP("127.0.0.1")) || !tp.Contains(parseIP("::1")) {
		t.Fatal("loopback should always be trusted")
	}
	if !tp.Contains(parseIP("192.0.2.1")) || !tp.Contains(parseIP("198.51.100.50")) {
		t.Fatal("configured proxies should be trusted")
	}
	if tp.Contains(parseIP("203.0.113.1")) {
		t.Fatal("unlisted address must not be trusted")
	}
}
