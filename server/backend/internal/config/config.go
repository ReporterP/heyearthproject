package config

import (
	"os"
	"path/filepath"
)

// AppDir is the base directory for scripts and data (e.g. /opt/vpn-bot).
func AppDir() string {
	if d := os.Getenv("APP_DIR"); d != "" {
		return d
	}
	return "/opt/vpn-bot"
}

// AddSSScript returns the path to add-ss-user.sh.
func AddSSScript() string {
	return filepath.Join(AppDir(), "add-ss-user.sh")
}

// DataFile returns the path to registered users file (bot).
func DataFile() string {
	return filepath.Join(AppDir(), "registered_users.txt")
}

// MappingFile returns the path to app user_id -> instance mapping (API).
func MappingFile() string {
	return filepath.Join(AppDir(), "app-user-instance.json")
}

// PlatformFile returns the path to user_id -> platform file (bot: android, windows, ios, macos).
func PlatformFile() string {
	return filepath.Join(AppDir(), "user_platform.txt")
}

// BotUserMapFile stores Telegram user ID -> canonical HeYearth user_id mappings.
func BotUserMapFile() string {
	return filepath.Join(AppDir(), "bot_user_map.json")
}

// ReleasesDir returns the path to released app files (APK, zip) on the server.
func ReleasesDir() string {
	return filepath.Join(AppDir(), "releases")
}
