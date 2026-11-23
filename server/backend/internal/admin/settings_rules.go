package admin

import (
	"fmt"
	"net"
	"net/mail"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// The «Настройки» page writes only these keys (C9): each is read by the code, each value is
// checked before it reaches app_settings. Everything else has its own page (MTProto, alerts,
// entries, the Hub's numbers) or is gone (migration 000069).
type settingRule struct {
	kind     string // bool | int | text | email | secret | host | inn | slug | enum
	min, max int
	values   []string // enum: the allowed values, lower case
}

var editableSettings = map[string]settingRule{
	// Платежи
	"yookassa_shop_id":             {kind: "text"},
	"yookassa_secret_key":          {kind: "secret"},
	"yookassa_test_mode":           {kind: "bool"},
	"receipt_fallback_email":       {kind: "email"},
	"wallet_min_topup_rub":         {kind: "int", min: 1, max: 100000},
	"early_renewal_days_threshold": {kind: "int", min: 0, max: 30},
	// Почта
	"smtp_enabled":    {kind: "bool"},
	"smtp_host":       {kind: "host"},
	"smtp_port":       {kind: "int", min: 1, max: 65535},
	"smtp_user":       {kind: "text"},
	"smtp_password":   {kind: "secret"},
	"smtp_from_email": {kind: "email"},
	"smtp_from_name":  {kind: "text"},
	"smtp_use_tls":    {kind: "bool"},
	// Юр. данные и поддержка
	"legal_company_name": {kind: "text"},
	"legal_inn":          {kind: "inn"},
	"legal_email":        {kind: "email"},
	"legal_phone":        {kind: "text"},
	"legal_address":      {kind: "text"},
	"support_contact":    {kind: "text"},
	// Лимиты
	"quota_cutoff_enabled": {kind: "bool"},
	// Автоматика, которая рвёт соединения (docs/prd/stability-operations.prd.md, A6 и A7).
	// xray_revoke_restart: what a revoked user still online leads to — off (nothing, default),
	// night (one xray restart in 04:00–05:30 MSK), now (restart at once: К3, drops everyone).
	"xray_revoke_restart": {kind: "enum", values: []string{"off", "night", "now"}},
	// bamboo_optimise: moving Бамбук to a faster exit — night (quiet window only, default) | off.
	"bamboo_optimise": {kind: "enum", values: []string{"night", "off"}},
	// Синтетический клиент (cmd/api/synth_monitor.go): the timer's switch, and the token a
	// probe in an operator's network reports with (empty: no intake).
	"synth_enabled":     {kind: "bool"},
	"probe_agent_token": {kind: "secret"},
	// Приведи друга
	"referral_enabled":             {kind: "bool"},
	"referral_reward_referrer_rub": {kind: "int", min: 0, max: 10000},
	"referral_reward_friend_rub":   {kind: "int", min: 0, max: 10000},
	"referral_min_purchase_rub":    {kind: "int", min: 0, max: 100000},
	"referral_monthly_cap":         {kind: "int", min: 0, max: 1000},
	// Подключение
	"hub_main_server_host": {kind: "host"},
	"api_endpoints":        {kind: "text"},
	"b2b_enabled":          {kind: "bool"},
	"b2b_member_plan":      {kind: "slug"}, // the plan members of an active company get (jzd5.24)
}

var innRe = regexp.MustCompile(`^(\d{10}|\d{12})$`)

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// normalizeSetting checks a value against its rule and returns what is stored.
func normalizeSetting(key, value string) (string, error) {
	rule, ok := editableSettings[strings.TrimSpace(key)]
	if !ok {
		return "", fmt.Errorf("ключ %q не меняется на этой странице", key)
	}
	v := strings.TrimSpace(value)
	if len(v) > 2000 {
		return "", fmt.Errorf("слишком длинное значение")
	}
	switch rule.kind {
	case "bool":
		if v != "true" && v != "false" {
			return "", fmt.Errorf("значение: true или false")
		}
	case "int":
		n, err := strconv.Atoi(v)
		if err != nil || n < rule.min || n > rule.max {
			return "", fmt.Errorf("целое число от %d до %d", rule.min, rule.max)
		}
		v = strconv.Itoa(n)
	case "email":
		if v != "" {
			a, err := mail.ParseAddress(v)
			if err != nil || a.Address != v {
				return "", fmt.Errorf("неверный email")
			}
		}
	case "host":
		if v != "" && net.ParseIP(v) == nil && !hostRe.MatchString(v) {
			return "", fmt.Errorf("имя хоста или IP, без http:// и порта")
		}
	case "inn":
		if v != "" && !innRe.MatchString(v) {
			return "", fmt.Errorf("ИНН — 10 или 12 цифр")
		}
	case "slug":
		v = strings.ToLower(v)
		if v != "" && !slugRe.MatchString(v) {
			return "", fmt.Errorf("слаг тарифа: латиница, цифры и дефис")
		}
	case "enum":
		v = strings.ToLower(v)
		if !slices.Contains(rule.values, v) {
			return "", fmt.Errorf("значение: %s", strings.Join(rule.values, " | "))
		}
	case "secret":
		if v == "" {
			return "", fmt.Errorf("пустой секрет не сохраняется: введите новое значение")
		}
		return value, nil // stored exactly as typed
	}
	return v, nil
}

var hostRe = regexp.MustCompile(`^([a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)+[a-zA-Z]{2,63}$`)
