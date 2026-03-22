package biz

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/heyearth/backend/internal/db"
)

func GetSetting(key string) (string, error) {
	var value string
	err := db.DB.QueryRow(`SELECT value FROM app_settings WHERE key = $1`, key).Scan(&value)
	if err != nil {
		return "", err
	}
	return value, nil
}

func SetSetting(key, value string) error {
	_, err := db.DB.Exec(`
		INSERT INTO app_settings (key, value, updated_at) VALUES ($1, $2, CURRENT_TIMESTAMP)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = CURRENT_TIMESTAMP
	`, key, value)
	return err
}

func GetAllSettings() (map[string]string, error) {
	rows, err := db.DB.Query(`SELECT key, value FROM app_settings ORDER BY key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]string)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		result[key] = value
	}
	return result, rows.Err()
}

// GetSettingOrDefault returns a setting value or the default if not found.
func GetSettingOrDefault(key, defaultValue string) string {
	value, err := GetSetting(key)
	if err != nil || strings.TrimSpace(value) == "" {
		return defaultValue
	}
	return value
}

// GetPriceRub возвращает цену плана из app_settings.
// Ключи: price_standard, price_premium. Если нет — используется defaultPrice.
func GetPriceRub(plan string, defaultPrice int) int {
	key := "price_" + plan
	val, err := GetSetting(key)
	if err != nil || val == "" {
		return defaultPrice
	}
	var price int
	if _, err := fmt.Sscanf(val, "%d", &price); err != nil || price <= 0 {
		return defaultPrice
	}
	return price
}

func GetAPIEndpoints() []string {
	raw, err := GetSetting("api_endpoints")
	if err != nil || strings.TrimSpace(raw) == "" {
		return nil
	}
	var endpoints []string
	if err := json.Unmarshal([]byte(raw), &endpoints); err != nil {
		return nil
	}
	filtered := endpoints[:0]
	for _, ep := range endpoints {
		ep = strings.TrimSpace(ep)
		if ep != "" {
			filtered = append(filtered, ep)
		}
	}
	return filtered
}
