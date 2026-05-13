package services

import (
	"sync"
	"time"
)

// nowFn is the time source used by CircuitBreaker.
// Overridden in tests to control failure-window timing.
var nowFn = time.Now

type circuitState int

const (
	stateClosed circuitState = iota
	stateOpen
	stateHalfOpen
)

// CircuitBreaker prevents cascading failures by short-circuiting calls
// to an unhealthy dependency after repeated failures within a window.
type CircuitBreaker struct {
	name     string
	maxFails int
	cooldown time.Duration
	// window is the sliding interval during which failures accumulate.
	// A failure older than `window` resets the counter so a slow trickle
	// of unrelated failures cannot trip the breaker over hours or days.
	// Zero disables decay (failures persist until RecordSuccess).
	window time.Duration

	mu            sync.Mutex
	state         circuitState
	failures      int
	openedAt      time.Time
	halfOpenAt    time.Time
	lastFailureAt time.Time
}

func NewCircuitBreaker(name string, maxFails int, cooldown, window time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		name:     name,
		maxFails: maxFails,
		cooldown: cooldown,
		window:   window,
	}
}

// Allow returns true if the call should proceed.
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	now := nowFn()
	switch cb.state {
	case stateClosed:
		return true
	case stateOpen:
		if now.Sub(cb.openedAt) >= cb.cooldown {
			cb.state = stateHalfOpen
			cb.halfOpenAt = now
			return true
		}
		return false
	case stateHalfOpen:
		// One probe at a time, but recover if probe vanished (panic, drop).
		// After cooldown elapses without RecordSuccess/Failure, re-arm.
		if now.Sub(cb.halfOpenAt) >= cb.cooldown {
			cb.halfOpenAt = now
			return true
		}
		return false
	}
	return true
}

func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failures = 0
	cb.state = stateClosed
}

func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	now := nowFn()
	if cb.window > 0 && !cb.lastFailureAt.IsZero() && now.Sub(cb.lastFailureAt) > cb.window {
		cb.failures = 0
	}
	cb.lastFailureAt = now
	cb.failures++
	if cb.state == stateHalfOpen || cb.failures >= cb.maxFails {
		cb.state = stateOpen
		cb.openedAt = now
	}
}

// defaultHostBreakerCap caps the number of per-host breakers retained.
// Above this, the oldest closed breaker is evicted on insert. Open or
// half-open breakers are preserved so an active outage doesn't get
// forgotten under unrelated insert pressure.
const defaultHostBreakerCap = 1024

// HostCircuitBreaker keeps an independent CircuitBreaker per host so a single
// failing destination cannot trip delivery for unrelated hosts.
type HostCircuitBreaker struct {
	name     string
	maxFails int
	cooldown time.Duration
	window   time.Duration

	mu       sync.Mutex
	breakers map[string]*CircuitBreaker
	cap      int
}

func NewHostCircuitBreaker(name string, maxFails int, cooldown, window time.Duration) *HostCircuitBreaker {
	return &HostCircuitBreaker{
		name:     name,
		maxFails: maxFails,
		cooldown: cooldown,
		window:   window,
		breakers: make(map[string]*CircuitBreaker),
		cap:      defaultHostBreakerCap,
	}
}

// breakerFor returns the CircuitBreaker for host, creating it on first use.
// When the map is at capacity, evicts a closed breaker first; if all are
// open or half-open the eviction falls back to an arbitrary key.
func (h *HostCircuitBreaker) breakerFor(host string) *CircuitBreaker {
	h.mu.Lock()
	defer h.mu.Unlock()

	if cb, ok := h.breakers[host]; ok {
		return cb
	}
	if h.cap > 0 && len(h.breakers) >= h.cap {
		h.evictLocked()
	}
	cb := NewCircuitBreaker(h.name+":"+host, h.maxFails, h.cooldown, h.window)
	h.breakers[host] = cb
	return cb
}

// evictLocked removes one breaker. Must be called with h.mu held.
// Prefers closed breakers (oldest by lastFailureAt); falls back to any.
func (h *HostCircuitBreaker) evictLocked() {
	var victim string
	var victimT time.Time
	hasClosed := false
	for k, b := range h.breakers {
		b.mu.Lock()
		state := b.state
		lastT := b.lastFailureAt
		b.mu.Unlock()
		if state != stateClosed {
			continue
		}
		if !hasClosed || lastT.Before(victimT) {
			victim, victimT, hasClosed = k, lastT, true
		}
	}
	if victim == "" {
		// All non-closed — evict any (range order is randomized by Go).
		for k := range h.breakers {
			victim = k
			break
		}
	}
	delete(h.breakers, victim)
}

// Allow consults the per-host breaker. Empty host is treated as any other
// key (callers should pre-bucket unparseable inputs to a sentinel) so the
// breaker never silently bypasses protection.
func (h *HostCircuitBreaker) Allow(host string) bool {
	return h.breakerFor(host).Allow()
}

func (h *HostCircuitBreaker) RecordSuccess(host string) {
	h.breakerFor(host).RecordSuccess()
}

func (h *HostCircuitBreaker) RecordFailure(host string) {
	h.breakerFor(host).RecordFailure()
}
