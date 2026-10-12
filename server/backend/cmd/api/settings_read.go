package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/db"
)

// settingOrEmpty reads an app setting: a missing row is "", any other error is returned as is.
// biz.GetSettingOrDefault cannot be used where the difference matters: it turns a DB outage
// into "not set" (e.g. "obfs password not set" resolves the Bamboo alerts, "no Telegram token"
// reports alerting as not configured) — a false verdict in the middle of an outage.
func settingOrEmpty(get func(key string) (string, error), key string) (string, error) {
	v, err := get(key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(v), nil
}

// dbBounded runs fn with a context that expires after timeout and returns at that moment even
// if fn is still blocked: lib/pq only sends a cancel request when the context ends and keeps
// waiting on the socket, so a hung PostgreSQL would otherwise stall the caller for minutes.
// The abandoned call finishes in the background; callers must tolerate that.
func dbBounded[T any](timeout time.Duration, fn func(ctx context.Context) (T, error)) (T, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	type result struct {
		v   T
		err error
	}
	ch := make(chan result, 1) // buffered: the abandoned goroutine never blocks on send
	go func() {
		v, err := fn(ctx)
		ch <- result{v, err}
	}()
	select {
	case r := <-ch:
		return r.v, r.err
	case <-ctx.Done():
		var zero T
		return zero, fmt.Errorf("БД не ответила за %v: %w", timeout, ctx.Err())
	}
}

// appSettingBounded is biz.GetSetting with a hard deadline (sql.ErrNoRows for a missing key).
func appSettingBounded(key string, timeout time.Duration) (string, error) {
	conn := db.DB
	if conn == nil {
		return "", errAlertsNoDB
	}
	return dbBounded(timeout, func(ctx context.Context) (string, error) {
		var v string
		err := conn.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = $1`, key).Scan(&v)
		return v, err
	})
}
