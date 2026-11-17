package telegram

import (
	"context"
	"sync"
	"time"
)

// Limiter keeps the bot inside Telegram's broadcast limits: a global token bucket (25 messages
// per second by default, below the documented ~30) and at most one message per second per chat.
type Limiter struct {
	Rate    float64       // tokens per second
	Burst   float64       // bucket size
	PerChat time.Duration // minimum gap between two messages to one chat
	Now     func() time.Time
	Sleep   func(ctx context.Context, d time.Duration) error

	mu     sync.Mutex
	tokens float64
	last   time.Time
	chats  map[string]time.Time
	swept  time.Time
}

// NewLimiter is the default 25 msg/s, 1 msg/s per chat.
func NewLimiter() *Limiter {
	return &Limiter{Rate: 25, Burst: 25, PerChat: time.Second}
}

func (l *Limiter) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

func (l *Limiter) sleep(ctx context.Context, d time.Duration) error {
	if l.Sleep != nil {
		return l.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// reserve takes a slot now and returns how long the caller must wait before using it. The slot
// is committed immediately, so concurrent callers queue up behind each other.
func (l *Limiter) reserve(chatID string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if l.chats == nil {
		l.chats = map[string]time.Time{}
		l.tokens = l.Burst
		l.last = now
	}
	// refill
	if el := now.Sub(l.last).Seconds(); el > 0 {
		l.tokens += el * l.Rate
		if l.tokens > l.Burst {
			l.tokens = l.Burst
		}
		l.last = now
	}
	l.tokens--
	var wait time.Duration
	if l.tokens < 0 {
		wait = time.Duration(-l.tokens / l.Rate * float64(time.Second))
	}
	if chatID != "" && l.PerChat > 0 {
		at := now.Add(wait)
		if next, ok := l.chats[chatID]; ok && next.After(at) {
			wait = next.Sub(now)
			at = next
		}
		l.chats[chatID] = at.Add(l.PerChat)
	}
	if now.Sub(l.swept) > time.Minute { // forget chats idle for a while
		for k, t := range l.chats {
			if now.Sub(t) > time.Minute {
				delete(l.chats, k)
			}
		}
		l.swept = now
	}
	return wait
}

// Wait blocks until a message to chatID may be sent ("" = no per-chat limit).
func (l *Limiter) Wait(ctx context.Context, chatID string) error {
	if l == nil {
		return nil
	}
	if d := l.reserve(chatID); d > 0 {
		return l.sleep(ctx, d)
	}
	return ctx.Err()
}
