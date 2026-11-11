package bus

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

// redisURL points at a real Redis when VB_TEST_REDIS_URL is set (CI), otherwise at an in-memory one.
func redisURL(t *testing.T) (string, func()) {
	t.Helper()
	if u := os.Getenv("VB_TEST_REDIS_URL"); u != "" {
		return u, func() {}
	}
	m := miniredis.RunT(t)
	return "redis://" + m.Addr() + "/0", m.Close
}

type inbox struct {
	mu  sync.Mutex
	got []string
}

func (b *inbox) add(p []byte) { b.mu.Lock(); b.got = append(b.got, string(p)); b.mu.Unlock() }
func (b *inbox) wait(t *testing.T, n int) []string {
	t.Helper()
	for i := 0; i < 200; i++ {
		b.mu.Lock()
		if len(b.got) >= n {
			out := append([]string(nil), b.got...)
			b.mu.Unlock()
			return out
		}
		b.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	t.Fatalf("got %d messages, want %d: %v", len(b.got), n, b.got)
	return nil
}

func TestLocalBus(t *testing.T) {
	ctx := context.Background()
	l := NewLocal()
	var in inbox
	l.Subscribe("events", in.add)
	l.Start(ctx)
	l.Publish(ctx, "events", []byte("a"))
	if got := in.wait(t, 1); got[0] != "a" {
		t.Fatal(got)
	}
	if !l.Lock(ctx, "cron:x", time.Minute) || l.Lock(ctx, "cron:x", time.Minute) {
		t.Fatal("local lock should be granted once per ttl")
	}
	n := 0
	for i := 0; i < 10; i++ {
		if l.Allow(ctx, "k", 3) {
			n++
		}
	}
	if n != 3 {
		t.Fatalf("allowed %d of 10 with a limit of 3", n)
	}
}

// Two instances on one Redis behave like one deployment.
func TestRedisBusSharesAcrossInstances(t *testing.T) {
	url, stop := redisURL(t)
	defer stop()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	prefix := "vbtest:" + InstanceID + ":"
	a, err := OpenRedis(ctx, url, prefix)
	if err != nil {
		t.Fatal(err)
	}
	b, err := OpenRedis(ctx, url, prefix)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	defer b.Close()
	var inA, inB inbox
	a.Subscribe("events", inA.add)
	b.Subscribe("events", inB.add)
	a.Start(ctx)
	b.Start(ctx)

	if err := a.Publish(ctx, "events", []byte("hello")); err != nil {
		t.Fatal(err)
	}
	if inA.wait(t, 1)[0] != "hello" || inB.wait(t, 1)[0] != "hello" {
		t.Fatal("both instances, the publisher included, must receive the message")
	}

	key := "cron:membership:" + time.Now().Format(time.RFC3339Nano)
	ga, gb := a.Lock(ctx, key, time.Minute), b.Lock(ctx, key, time.Minute)
	if ga == gb {
		t.Fatalf("exactly one instance must get the lock: a=%v b=%v", ga, gb)
	}

	rl := "login:" + time.Now().Format(time.RFC3339Nano)
	allowed := 0
	for i := 0; i < 5; i++ {
		if a.Allow(ctx, rl, 4) {
			allowed++
		}
		if b.Allow(ctx, rl, 4) {
			allowed++
		}
	}
	if allowed != 4 {
		t.Fatalf("allowed %d of 10 across instances with a limit of 4", allowed)
	}
	if a.Kind() != "redis" || a.Ping(ctx) != nil {
		t.Fatal("ping")
	}
}

// When Redis goes away, messages still reach this instance and locks are refused.
func TestRedisBusDegradesWhenRedisIsDown(t *testing.T) {
	if os.Getenv("VB_TEST_REDIS_URL") != "" {
		t.Skip("needs a Redis it can stop")
	}
	m := miniredis.RunT(t)
	ctx := context.Background()
	r, err := OpenRedis(ctx, "redis://"+m.Addr()+"/0", "vb:")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var in inbox
	r.Subscribe("events", in.add)
	m.Close()
	if err := r.Publish(ctx, "events", []byte("still here")); err == nil {
		t.Fatal("publish should report the outage")
	}
	if in.wait(t, 1)[0] != "still here" {
		t.Fatal("message must be delivered locally")
	}
	if r.Lock(ctx, "cron:x", time.Minute) {
		t.Fatal("a lock must be refused while Redis is down")
	}
	if !r.Allow(ctx, "k", 2) || !r.Allow(ctx, "k", 2) || r.Allow(ctx, "k", 2) {
		t.Fatal("rate limit must fall back to a local count")
	}
}

func TestOpenRejectsBadURL(t *testing.T) {
	if _, err := Open(context.Background(), "http://nope"); err == nil {
		t.Fatal("expected an error for a non-redis URL")
	}
	if b, err := Open(context.Background(), ""); err != nil || b.Kind() != "local" {
		t.Fatal("empty URL must give the local bus")
	}
}
