package services

import (
	"testing"
	"time"
)

// Tests in this file mutate the package-global nowFn for time control.
// They are NOT safe to run with t.Parallel(); add a per-CB clock field
// before parallelizing.

func TestCircuitBreaker_FailureWindow_ResetsStaleCounter(t *testing.T) {
	fakeNow := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	nowFn = func() time.Time { return fakeNow }
	t.Cleanup(func() { nowFn = time.Now })

	cb := NewCircuitBreaker("t", 3, 60*time.Second, 5*time.Minute)
	cb.RecordFailure()
	cb.RecordFailure()

	fakeNow = fakeNow.Add(6 * time.Minute)

	cb.RecordFailure()
	if !cb.Allow() {
		t.Fatal("breaker tripped on failures separated by > window; counter should have reset")
	}
}

func TestCircuitBreaker_FailureWindow_TripsInsideWindow(t *testing.T) {
	fakeNow := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	nowFn = func() time.Time { return fakeNow }
	t.Cleanup(func() { nowFn = time.Now })

	cb := NewCircuitBreaker("t", 3, 60*time.Second, 5*time.Minute)
	cb.RecordFailure()
	fakeNow = fakeNow.Add(1 * time.Minute)
	cb.RecordFailure()
	fakeNow = fakeNow.Add(1 * time.Minute)
	cb.RecordFailure()

	if cb.Allow() {
		t.Fatal("breaker should be open: 3 failures within window")
	}
}

func TestCircuitBreaker_WindowZero_NoDecay(t *testing.T) {
	fakeNow := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	nowFn = func() time.Time { return fakeNow }
	t.Cleanup(func() { nowFn = time.Now })

	cb := NewCircuitBreaker("t", 3, 60*time.Second, 0)

	cb.RecordFailure()
	fakeNow = fakeNow.Add(24 * time.Hour)
	cb.RecordFailure()
	fakeNow = fakeNow.Add(24 * time.Hour)
	cb.RecordFailure()

	if cb.Allow() {
		t.Fatal("window=0 must disable decay; 3 failures across days should trip breaker")
	}
}

func TestCircuitBreaker_HalfOpen_ProbeTimeoutRearms(t *testing.T) {
	fakeNow := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	nowFn = func() time.Time { return fakeNow }
	t.Cleanup(func() { nowFn = time.Now })

	cb := NewCircuitBreaker("t", 1, 60*time.Second, 5*time.Minute)

	// open the breaker
	cb.RecordFailure()
	if cb.Allow() {
		t.Fatal("breaker should be open")
	}

	// cooldown elapsed -> next Allow probes (state -> halfOpen, returns true)
	fakeNow = fakeNow.Add(61 * time.Second)
	if !cb.Allow() {
		t.Fatal("expected probe after cooldown")
	}

	// probe vanishes (e.g. FireAndForget panic). No RecordSuccess/Failure.
	// Immediate subsequent Allow must still be denied (one probe at a time).
	if cb.Allow() {
		t.Fatal("concurrent probe must be denied")
	}

	// after another cooldown, breaker should re-arm and allow a new probe
	fakeNow = fakeNow.Add(61 * time.Second)
	if !cb.Allow() {
		t.Fatal("breaker stuck in half-open after probe timeout; should re-probe")
	}
}

func TestHostCircuitBreaker_IsolatesHosts(t *testing.T) {
	cb := NewHostCircuitBreaker("test", 2, 60*time.Second, 5*time.Minute)

	cb.RecordFailure("a.example.com")
	cb.RecordFailure("a.example.com")

	if cb.Allow("a.example.com") {
		t.Fatal("host A breaker should be open after maxFails")
	}
	if !cb.Allow("b.example.com") {
		t.Fatal("host B breaker must stay closed; failures on A leaked to B")
	}
}

func TestHostCircuitBreaker_SuccessResetsHost(t *testing.T) {
	cb := NewHostCircuitBreaker("test", 1, 60*time.Second, 5*time.Minute)

	cb.RecordFailure("a.example.com")
	if cb.Allow("a.example.com") {
		t.Fatal("breaker should be open after one failure when maxFails=1")
	}

	cb.RecordSuccess("a.example.com")
	if !cb.Allow("a.example.com") {
		t.Fatal("breaker should be closed after RecordSuccess")
	}
}

func TestHostCircuitBreaker_EvictsWhenAtCap(t *testing.T) {
	cb := NewHostCircuitBreaker("test", 1, 60*time.Second, 5*time.Minute)
	cb.cap = 2

	cb.RecordSuccess("a")
	cb.RecordSuccess("b")
	cb.RecordSuccess("c")

	if len(cb.breakers) != 2 {
		t.Fatalf("map size after eviction = %d, want 2", len(cb.breakers))
	}
	if _, ok := cb.breakers["c"]; !ok {
		t.Fatal("newest entry c should be present")
	}
}

func TestHostCircuitBreaker_EvictionPrefersClosed(t *testing.T) {
	cb := NewHostCircuitBreaker("test", 1, 60*time.Second, 5*time.Minute)
	cb.cap = 2

	cb.RecordFailure("a") // state=open
	cb.RecordSuccess("b") // state=closed

	cb.RecordSuccess("c") // forces eviction; b (closed) preferred over a (open)

	if _, ok := cb.breakers["a"]; !ok {
		t.Fatal("open breaker a was evicted while closed b existed")
	}
	if _, ok := cb.breakers["b"]; ok {
		t.Fatal("closed breaker b should have been evicted in favor of open a")
	}
}

func TestHostCircuitBreaker_EmptyHostGatedLikeAnyOther(t *testing.T) {
	cb := NewHostCircuitBreaker("test", 1, 60*time.Second, 5*time.Minute)
	cb.RecordFailure("")
	if cb.Allow("") {
		t.Fatal("empty host must trip its own breaker, not bypass protection")
	}
	if !cb.Allow("other.example.com") {
		t.Fatal("failures on empty host must not leak to other hosts")
	}
}
