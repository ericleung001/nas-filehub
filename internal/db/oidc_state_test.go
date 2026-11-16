package db

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestConsumeOIDCStateAtomicAndExpired(t *testing.T) {
	d := openTestDB(t)

	exp := time.Now().Add(10 * time.Minute)
	if err := d.CreateOIDCState("state-ok", "nonce-ok", "verifier-ok", true, exp); err != nil {
		t.Fatal(err)
	}
	got, err := d.ConsumeOIDCState("state-ok")
	if err != nil || got == nil {
		t.Fatalf("first consume: %+v %v", got, err)
	}
	if got.Nonce != "nonce-ok" || got.CodeVerifier != "verifier-ok" || !got.RememberMe {
		t.Fatalf("unexpected state: %+v", got)
	}
	again, err := d.ConsumeOIDCState("state-ok")
	if err != nil || again != nil {
		t.Fatalf("second consume should be nil: %+v %v", again, err)
	}

	if err := d.CreateOIDCState("state-exp", "nonce-exp", "verifier-exp", false, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	expired, err := d.ConsumeOIDCState("state-exp")
	if err != nil || expired != nil {
		t.Fatalf("expired should return nil: %+v %v", expired, err)
	}
	// Expired row must still have been deleted.
	var n int
	if err := d.SQL.QueryRow(`SELECT COUNT(*) FROM oidc_states WHERE state=?`, "state-exp").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("expired state still present: %d", n)
	}

	missing, err := d.ConsumeOIDCState("no-such-state")
	if err != nil || missing != nil {
		t.Fatalf("missing: %+v %v", missing, err)
	}
}

func TestConsumeOIDCStateConcurrentSingleWinner(t *testing.T) {
	d := openTestDB(t)
	if err := d.CreateOIDCState("state-race", "nonce-race", "verifier-race", false, time.Now().Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}

	const goroutines = 16
	var wins atomic.Int32
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			got, err := d.ConsumeOIDCState("state-race")
			if err != nil {
				t.Errorf("consume: %v", err)
				return
			}
			if got != nil {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("want exactly one successful consume, got %d", wins.Load())
	}
}
