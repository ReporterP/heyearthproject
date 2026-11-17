package telegram

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Path performs a request over one network path.
type Path func(ctx context.Context, req *Request) ([]byte, error)

// Doer performs a request (a Route, or a fake in tests).
type Doer interface {
	Do(ctx context.Context, req *Request) ([]byte, error)
}

// Route sends direct first and falls back to the exit tunnel on transport errors. After a failed
// direct attempt the direct path is skipped for Pause, so that each call does not first wait out
// the direct timeout. A Telegram verdict (4xx) is final on either path.
type Route struct {
	Now     func() time.Time
	Direct  Path
	ViaExit Path // ErrNoExitRoute when no exit is reachable
	Pause   time.Duration
	Logf    func(format string, args ...any)

	mu              sync.Mutex
	directDownUntil time.Time
}

func (r *Route) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Route) logf(format string, args ...any) {
	if r.Logf != nil {
		r.Logf(format, args...)
	}
}

// DirectDown reports whether the direct path is being skipped now.
func (r *Route) DirectDown() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.now().Before(r.directDownUntil)
}

func (r *Route) setDirectDown(down bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if down {
		pause := r.Pause
		if pause <= 0 {
			pause = 10 * time.Minute
		}
		r.directDownUntil = r.now().Add(pause)
	} else {
		r.directDownUntil = time.Time{}
	}
}

// Do performs req over the best path.
func (r *Route) Do(ctx context.Context, req *Request) ([]byte, error) {
	if r.DirectDown() {
		res, err := r.ViaExit(ctx, req)
		if err == nil || IsFinal(err) {
			return res, err
		}
		if ctx.Err() != nil {
			return nil, err
		}
		// exit path unavailable or failing: the direct path may have recovered
		res, derr := r.Direct(ctx, req)
		if derr == nil {
			r.setDirectDown(false)
			return res, nil
		}
		if IsFinal(derr) {
			return nil, derr
		}
		if errors.Is(err, ErrNoExitRoute) {
			return nil, derr
		}
		return nil, fmt.Errorf("%v; напрямую: %v", err, derr)
	}
	res, derr := r.Direct(ctx, req)
	if derr == nil {
		return res, nil
	}
	if IsFinal(derr) || ctx.Err() != nil {
		return nil, derr
	}
	r.setDirectDown(true) // api.telegram.org may be throttled from the RU Hub
	res, err := r.ViaExit(ctx, req)
	if err == nil {
		r.logf("[telegram] напрямую недоступен (%v) — следующие %s ходим через exit", derr, r.Pause)
		return res, nil
	}
	if IsFinal(err) {
		return nil, err
	}
	if errors.Is(err, ErrNoExitRoute) {
		return nil, derr
	}
	return nil, fmt.Errorf("напрямую: %v; %v", derr, err)
}
