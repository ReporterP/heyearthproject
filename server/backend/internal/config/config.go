package config

import "os"

// AppDir is the base directory for runtime assets on the main server.
func AppDir() string {
	if d := os.Getenv("APP_DIR"); d != "" {
		return d
	}
	return "/opt/heyearth/mainserver"
}
