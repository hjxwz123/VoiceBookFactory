// Package bus connects the instances of one deployment.
//
// A single instance needs nothing: Local keeps messages, locks and rate limits in the process. When
// several instances run behind a load balancer (VB_REDIS_URL set), Redis carries them instead:
//
//   - Publish/Subscribe: realtime events, job wake-ups and cancellations, cache invalidation reach
//     every instance, including the one that published;
//   - Lock: a scheduled task runs on one instance per interval instead of on all of them;
//   - Allow: rate limits count requests across instances.
package bus

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"sync"
	"time"
)

type Handler func(payload []byte)

type Bus interface {
	// Publish delivers payload to the topic's handlers on every instance (this one included).
	Publish(ctx context.Context, topic string, payload []byte) error
	// Subscribe registers a handler; call it before Start.
	Subscribe(topic string, fn Handler)
	// Start begins receiving messages for the subscribed topics.
	Start(ctx context.Context)
	// Lock reports whether this instance may run the work named key; once granted, other instances are
	// refused until ttl passes.
	Lock(ctx context.Context, key string, ttl time.Duration) bool
	// Allow permits perMinute events per key.
	Allow(ctx context.Context, key string, perMinute int) bool
	Ping(ctx context.Context) error
	Kind() string // "local" | "redis"
	Close() error
}

// Open returns Local for an empty URL and a connected Redis bus otherwise.
func Open(ctx context.Context, redisURL string) (Bus, error) {
	if redisURL == "" {
		return NewLocal(), nil
	}
	return OpenRedis(ctx, redisURL, "vb:")
}

// InstanceID names this process in locks, for debugging.
var InstanceID = func() string {
	h, _ := os.Hostname()
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return h + "-" + hex.EncodeToString(b)
}()

// ---------------- in-process ----------------

type Local struct {
	mu       sync.RWMutex
	handlers map[string][]Handler
	locks    map[string]time.Time
	limiter  *limiter
}

func NewLocal() *Local {
	return &Local{handlers: map[string][]Handler{}, locks: map[string]time.Time{}, limiter: newLimiter()}
}

func (l *Local) Publish(_ context.Context, topic string, payload []byte) error {
	l.mu.RLock()
	hs := l.handlers[topic]
	l.mu.RUnlock()
	for _, h := range hs {
		h(payload)
	}
	return nil
}

func (l *Local) Subscribe(topic string, fn Handler) {
	l.mu.Lock()
	l.handlers[topic] = append(l.handlers[topic], fn)
	l.mu.Unlock()
}

func (l *Local) Start(context.Context) {}

func (l *Local) Lock(_ context.Context, key string, ttl time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if until, ok := l.locks[key]; ok && now.Before(until) {
		return false
	}
	l.locks[key] = now.Add(ttl)
	return true
}

func (l *Local) Allow(_ context.Context, key string, perMinute int) bool {
	return l.limiter.allow(key, perMinute)
}

func (l *Local) Ping(context.Context) error { return nil }
func (l *Local) Kind() string               { return "local" }
func (l *Local) Close() error               { return nil }

// limiter is a token bucket per key: perMinute tokens, refilled continuously.
type limiter struct {
	mu sync.Mutex
	b  map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newLimiter() *limiter { return &limiter{b: map[string]*bucket{}} }

func (r *limiter) allow(key string, perMinute int) bool {
	if perMinute <= 0 {
		return true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	t := time.Now()
	bk, ok := r.b[key]
	if !ok {
		bk = &bucket{tokens: float64(perMinute), last: t}
		r.b[key] = bk
	}
	bk.tokens += t.Sub(bk.last).Minutes() * float64(perMinute)
	if bk.tokens > float64(perMinute) {
		bk.tokens = float64(perMinute)
	}
	bk.last = t
	if bk.tokens < 1 {
		return false
	}
	bk.tokens--
	return true
}
