package telegram

import (
	"context"
	"errors"
	"time"
)

// OffsetStore keeps the next getUpdates offset across restarts.
type OffsetStore interface {
	Load(ctx context.Context) (int, error)
	Save(ctx context.Context, offset int) error
}

// Poller reads updates with long polling and hands them to Handle one by one, in order. The
// offset is saved after each handled update, so a restart neither loses nor repeats updates
// (except the one being handled at the moment of a crash; handlers are idempotent where it
// matters).
type Poller struct {
	Name    string
	Bot     func() *Bot // nil while the bot is not configured
	Store   OffsetStore
	Handle  func(ctx context.Context, u Update)
	Allowed []string
	Timeout int // long-poll seconds; 0 → 25
	Logf    func(format string, args ...any)
	Sleep   func(ctx context.Context, d time.Duration) error
	// OnPoll, when set, is told how every getUpdates went: nil — Telegram answered.
	OnPoll func(err error)
}

// ErrNotConfigured: Bot() returned nil.
var ErrNotConfigured = errors.New("бот не настроен")

func (p *Poller) logf(format string, args ...any) {
	if p.Logf != nil {
		p.Logf(format, args...)
	}
}

func (p *Poller) sleep(ctx context.Context, d time.Duration) error {
	if p.Sleep != nil {
		return p.Sleep(ctx, d)
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

// RunOnce performs one getUpdates and handles what it returned. It returns the number of
// updates handled.
func (p *Poller) RunOnce(ctx context.Context) (int, error) {
	bot := p.Bot()
	if bot == nil {
		if p.OnPoll != nil {
			p.OnPoll(ErrNotConfigured)
		}
		return 0, ErrNotConfigured
	}
	offset, err := p.Store.Load(ctx)
	if err != nil {
		return 0, err
	}
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = 25
	}
	ups, err := bot.GetUpdates(ctx, offset, timeout, p.Allowed)
	if p.OnPoll != nil {
		p.OnPoll(err)
	}
	if err != nil {
		return 0, err
	}
	n := 0
	for _, u := range ups {
		if u.UpdateID < offset {
			continue // already handled (Telegram never sends these after a confirmed offset)
		}
		p.Handle(ctx, u)
		offset = u.UpdateID + 1
		if err := p.Store.Save(ctx, offset); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// Run polls until ctx ends, backing off after errors (not configured: 30 s; 409 webhook
// conflict or bad token: 5 min; other errors: 5 s growing to 60 s).
func (p *Poller) Run(ctx context.Context) {
	backoff := 5 * time.Second
	for ctx.Err() == nil {
		_, err := p.RunOnce(ctx)
		if err == nil {
			backoff = 5 * time.Second
			continue
		}
		if ctx.Err() != nil {
			return
		}
		wait := backoff
		var apiErr *APIError
		switch {
		case errors.Is(err, ErrNotConfigured):
			wait = 30 * time.Second
		case errors.As(err, &apiErr) && apiErr.FromTelegram && (apiErr.Status == 409 || apiErr.Status == 401 || apiErr.Status == 404):
			wait = 5 * time.Minute
			p.logf("[%s] getUpdates: %v — пауза %s", p.Name, err, wait)
		default:
			p.logf("[%s] getUpdates: %v — повтор через %s", p.Name, err, wait)
			if backoff < time.Minute {
				backoff *= 2
			}
		}
		if p.sleep(ctx, wait) != nil {
			return
		}
	}
}
