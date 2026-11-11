package bus

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// Redis shares messages, locks and rate limits between instances. If Redis becomes unreachable,
// messages are still delivered on this instance, rate limits fall back to this instance's own count,
// and locks are refused (a scheduled task skips a beat rather than running twice).
type Redis struct {
	rdb      *redis.Client
	prefix   string
	mu       sync.RWMutex
	handlers map[string][]Handler
	started  bool
	fallback *limiter
	warnMu   sync.Mutex
	warnedAt time.Time
}

// OpenRedis connects (waiting up to 30 s for the server, as containers start together) and returns the bus.
func OpenRedis(ctx context.Context, url, prefix string) (*Redis, error) {
	opt, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("VB_REDIS_URL 格式不对（例如 redis://redis:6379/0）：%w", err)
	}
	rdb := redis.NewClient(opt)
	deadline := time.Now().Add(30 * time.Second)
	for {
		pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err = rdb.Ping(pctx).Err()
		cancel()
		if err == nil {
			break
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			rdb.Close()
			return nil, fmt.Errorf("连接 Redis 失败（%s）：%w", opt.Addr, err)
		}
		time.Sleep(time.Second)
	}
	return &Redis{rdb: rdb, prefix: prefix, handlers: map[string][]Handler{}, fallback: newLimiter()}, nil
}

func (r *Redis) warn(msg string, err error) {
	r.warnMu.Lock()
	defer r.warnMu.Unlock()
	if time.Since(r.warnedAt) > time.Minute {
		r.warnedAt = time.Now()
		slog.Warn(msg, "err", err)
	}
}

func (r *Redis) deliver(topic string, payload []byte) {
	r.mu.RLock()
	hs := r.handlers[topic]
	r.mu.RUnlock()
	for _, h := range hs {
		func() {
			defer func() {
				if p := recover(); p != nil {
					slog.Error("bus handler panic", "topic", topic, "panic", p)
				}
			}()
			h(payload)
		}()
	}
}

func (r *Redis) Publish(ctx context.Context, topic string, payload []byte) error {
	if err := r.rdb.Publish(ctx, r.prefix+topic, payload).Err(); err != nil {
		r.warn("redis publish failed; delivering on this instance only", err)
		r.deliver(topic, payload)
		return err
	}
	return nil
}

func (r *Redis) Subscribe(topic string, fn Handler) {
	r.mu.Lock()
	r.handlers[topic] = append(r.handlers[topic], fn)
	r.mu.Unlock()
}

func (r *Redis) Start(ctx context.Context) {
	r.mu.Lock()
	if r.started {
		r.mu.Unlock()
		return
	}
	r.started = true
	channels := make([]string, 0, len(r.handlers))
	for t := range r.handlers {
		channels = append(channels, r.prefix+t)
	}
	r.mu.Unlock()
	if len(channels) == 0 {
		return
	}
	ps := r.rdb.Subscribe(ctx, channels...)
	// wait for the subscription so nothing published right after start is missed
	if _, err := ps.Receive(ctx); err != nil {
		r.warn("redis subscribe failed", err)
	}
	go func() {
		defer ps.Close()
		ch := ps.Channel(redis.WithChannelSize(1024))
		for {
			select {
			case <-ctx.Done():
				return
			case m, ok := <-ch:
				if !ok {
					return
				}
				r.deliver(m.Channel[len(r.prefix):], []byte(m.Payload))
			}
		}
	}()
}

func (r *Redis) Lock(ctx context.Context, key string, ttl time.Duration) bool {
	ok, err := r.rdb.SetNX(ctx, r.prefix+"lock:"+key, InstanceID, ttl).Result()
	if err != nil {
		r.warn("redis lock failed; skipping this run", err)
		return false
	}
	return ok
}

// Allow counts events in fixed one-minute windows shared by all instances.
func (r *Redis) Allow(ctx context.Context, key string, perMinute int) bool {
	if perMinute <= 0 {
		return true
	}
	k := r.prefix + "rl:" + key + ":" + strconv.FormatInt(time.Now().Unix()/60, 10)
	pipe := r.rdb.TxPipeline()
	incr := pipe.Incr(ctx, k)
	pipe.Expire(ctx, k, 90*time.Second)
	if _, err := pipe.Exec(ctx); err != nil {
		r.warn("redis rate limit failed; counting on this instance", err)
		return r.fallback.allow(key, perMinute)
	}
	return incr.Val() <= int64(perMinute)
}

func (r *Redis) Ping(ctx context.Context) error { return r.rdb.Ping(ctx).Err() }
func (r *Redis) Kind() string                   { return "redis" }
func (r *Redis) Close() error                   { return r.rdb.Close() }
