package main

// The subscription flag registry (docs/prd/stability-operations.prd.md, part 5): one place that
// knows every flag that changes where users' traffic goes, its states and whether it goes
// through the canary. setFlagState writes the setting, bumps the subscription version (clients
// pick it up at their next update) and writes the change to the ops feed (target flag:<key>, К0).
// The entries session's settings (entry mode, rotation) are not here: their «test» state is that
// session's area.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/heyearth/backend/internal/biz"
)

type subFlag struct {
	Key    string   `json:"key"`    // app_settings key
	Label  string   `json:"label"`  // «Прямые сайты по имени»
	States []string `json:"states"` // in order: off, test, on
	Canary bool     `json:"canary"` // false — goes without the canary
	Note   string   `json:"note"`   // what the check does not see
}

var subFlags = []subFlag{
	{Key: "sub_auto_mode", Label: "«⚡ Авто» в подписке Happ", States: []string{"off", "test", "on"}, Canary: true,
		Note: "Проверка смотрит из-за рубежа и из домашних сетей РФ; мобильная сеть не проверена — зонда с SIM нет."},
	{Key: subDirectByNameKey, Label: "Прямые сайты по имени", States: []string{"off", "test", "on"}, Canary: true,
		Note: "Подмену DNS оператором видит только статическая проверка секции dns; мобильная сеть не проверена."},
	{Key: subJSONDNSKey, Label: "DNS через туннель", States: []string{"off", "test", "on"}, Canary: true,
		Note: "Резолвер оператора не проверяется: зонда в сети оператора нет."},
	{Key: "subscription_update_interval_hours", Label: "Интервал обновления подписки, часов", Canary: false,
		Note: "Путей не меняет: идёт без канарейки."},
}

func flagByKey(key string) (subFlag, bool) {
	i := slices.IndexFunc(subFlags, func(f subFlag) bool { return f.Key == key })
	if i < 0 {
		return subFlag{}, false
	}
	return subFlags[i], true
}

// Test seams over the settings and the subscription version.
var (
	flagGet  = biz.GetSettingContext
	flagSet  = biz.SetSettingContext
	flagBump = biz.BumpSubscriptionVersionContext
)

// flagState is the flag's current value ("off" when unset, for a flag with states).
func flagState(ctx context.Context, f subFlag) (string, error) {
	v, err := flagGet(ctx, f.Key)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	if err != nil {
		return "", err
	}
	if v == "" && len(f.States) > 0 {
		v = f.States[0]
	}
	return v, nil
}

var flagStateLabels = map[string]string{"off": "выключено", "test": "тест", "on": "всем"}

func flagStateLabel(s string) string {
	if l, ok := flagStateLabels[s]; ok {
		return l
	}
	return s
}

// applyFlagState writes `to` and bumps the subscription version once; it records nothing.
// A state outside States is refused; the state already set changes nothing (changed=false).
func applyFlagState(ctx context.Context, f subFlag, to string) (from string, changed bool, err error) {
	if len(f.States) > 0 && !slices.Contains(f.States, to) {
		return "", false, fmt.Errorf("флаг %s: состояние %q — допустимы %v", f.Key, to, f.States)
	}
	from, err = flagState(ctx, f)
	if err != nil {
		return "", false, fmt.Errorf("флаг %s: %w", f.Key, err)
	}
	if from == to {
		return from, false, nil
	}
	if err := flagSet(ctx, f.Key, to); err != nil {
		return from, false, fmt.Errorf("флаг %s: %w", f.Key, err)
	}
	if err := flagBump(ctx); err != nil {
		return from, true, fmt.Errorf("флаг %s записан, версия подписки не поднята: %w", f.Key, err)
	}
	return from, true, nil
}

// setFlagState moves the flag to `to` (applyFlagState) and writes the change to the feed:
// «<label>: тест → всем», target flag:<key>, К0.
func setFlagState(ctx context.Context, f subFlag, to, by string) error {
	from, changed, err := applyFlagState(ctx, f, to)
	if err != nil || !changed {
		return err
	}
	recordOps(biz.OpsEvent{Kind: "action", Actor: by, Target: "flag:" + f.Key, Impact: biz.ImpactK0, OK: true,
		Summary: fmt.Sprintf("%s: %s → %s", f.Label, flagStateLabel(from), flagStateLabel(to))})
	return nil
}

// setUrgentUpdate turns «Срочное обновление» on for hours (hourly subscription updates; 0 stops
// it): subUrgentKey = now + hours, the same value the «Входы» page writes.
func setUrgentUpdate(ctx context.Context, hours int, by string) error {
	if hours < 0 || hours > 48 {
		return fmt.Errorf("срочное обновление: от 1 до 48 часов (0 — остановить)")
	}
	v, summary := "", "срочное обновление выключено"
	if hours > 0 {
		v = time.Now().Add(time.Duration(hours) * time.Hour).UTC().Format(time.RFC3339)
		summary = "срочное обновление на " + strconv.Itoa(hours) + " ч"
	}
	if err := flagSet(ctx, subUrgentKey, v); err != nil {
		return fmt.Errorf("срочное обновление: %w", err)
	}
	if err := flagBump(ctx); err != nil {
		return fmt.Errorf("срочное обновление записано, версия подписки не поднята: %w", err)
	}
	recordOps(biz.OpsEvent{Kind: "action", Actor: by, Target: "flag:" + subUrgentKey, Impact: biz.ImpactK0, OK: true, Summary: summary})
	return nil
}
