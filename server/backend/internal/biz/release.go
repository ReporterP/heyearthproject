package biz

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/db"
)

var SupportedReleasePlatforms = []string{"android", "windows", "macos", "linux", "ios"}

type AppRelease struct {
	ID           int
	Platform     string
	Version      string
	ReleaseNotes string
	FileName     string
	FilePath     string
	PublicPath   string
	ContentType  string
	FileSize     int64
	IsActive     bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func NormalizeReleasePlatform(platform string) string {
	return strings.ToLower(strings.TrimSpace(platform))
}

func IsSupportedReleasePlatform(platform string) bool {
	normalized := NormalizeReleasePlatform(platform)
	for _, supported := range SupportedReleasePlatforms {
		if normalized == supported {
			return true
		}
	}
	return false
}

func ListAppReleases() ([]AppRelease, error) {
	rows, err := db.DB.Query(`
		SELECT id, platform, version, COALESCE(release_notes, ''), file_name, file_path, public_path,
		       COALESCE(content_type, ''), file_size, is_active, created_at, updated_at
		FROM app_releases
		ORDER BY created_at DESC, id DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("list app releases: %w", err)
	}
	defer rows.Close()

	var releases []AppRelease
	for rows.Next() {
		var release AppRelease
		if err := rows.Scan(
			&release.ID,
			&release.Platform,
			&release.Version,
			&release.ReleaseNotes,
			&release.FileName,
			&release.FilePath,
			&release.PublicPath,
			&release.ContentType,
			&release.FileSize,
			&release.IsActive,
			&release.CreatedAt,
			&release.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan app release: %w", err)
		}
		releases = append(releases, release)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate app releases: %w", err)
	}

	return releases, nil
}

func CreateAppRelease(release AppRelease) (*AppRelease, error) {
	if !IsSupportedReleasePlatform(release.Platform) {
		return nil, fmt.Errorf("unsupported platform")
	}

	created := &AppRelease{}
	err := db.DB.QueryRow(`
		INSERT INTO app_releases (
			platform, version, release_notes, file_name, file_path, public_path, content_type, file_size, is_active
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, FALSE)
		RETURNING id, platform, version, COALESCE(release_notes, ''), file_name, file_path, public_path,
		          COALESCE(content_type, ''), file_size, is_active, created_at, updated_at
	`,
		NormalizeReleasePlatform(release.Platform),
		strings.TrimSpace(release.Version),
		strings.TrimSpace(release.ReleaseNotes),
		strings.TrimSpace(release.FileName),
		strings.TrimSpace(release.FilePath),
		strings.TrimSpace(release.PublicPath),
		strings.TrimSpace(release.ContentType),
		release.FileSize,
	).Scan(
		&created.ID,
		&created.Platform,
		&created.Version,
		&created.ReleaseNotes,
		&created.FileName,
		&created.FilePath,
		&created.PublicPath,
		&created.ContentType,
		&created.FileSize,
		&created.IsActive,
		&created.CreatedAt,
		&created.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create app release: %w", err)
	}

	return created, nil
}

func ActivateAppRelease(id int) error {
	tx, err := db.DB.Begin()
	if err != nil {
		return fmt.Errorf("begin activate release tx: %w", err)
	}
	defer tx.Rollback()

	var platform string
	err = tx.QueryRow(`SELECT platform FROM app_releases WHERE id = $1`, id).Scan(&platform)
	if err == sql.ErrNoRows {
		return fmt.Errorf("release not found")
	}
	if err != nil {
		return fmt.Errorf("load release platform: %w", err)
	}

	if _, err := tx.Exec(`UPDATE app_releases SET is_active = FALSE, updated_at = CURRENT_TIMESTAMP WHERE platform = $1`, platform); err != nil {
		return fmt.Errorf("deactivate platform releases: %w", err)
	}
	if _, err := tx.Exec(`UPDATE app_releases SET is_active = TRUE, updated_at = CURRENT_TIMESTAMP WHERE id = $1`, id); err != nil {
		return fmt.Errorf("activate release: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit activate release tx: %w", err)
	}
	return nil
}

func GetActiveAppRelease(platform string) (*AppRelease, error) {
	if platform != "" && !IsSupportedReleasePlatform(platform) {
		return nil, fmt.Errorf("unsupported platform")
	}

	query := `
		SELECT id, platform, version, COALESCE(release_notes, ''), file_name, file_path, public_path,
		       COALESCE(content_type, ''), file_size, is_active, created_at, updated_at
		FROM app_releases
		WHERE is_active = TRUE
	`
	args := []any{}
	if platform != "" {
		query += ` AND platform = $1`
		args = append(args, NormalizeReleasePlatform(platform))
	}
	query += ` ORDER BY updated_at DESC, id DESC LIMIT 1`

	var release AppRelease
	err := db.DB.QueryRow(query, args...).Scan(
		&release.ID,
		&release.Platform,
		&release.Version,
		&release.ReleaseNotes,
		&release.FileName,
		&release.FilePath,
		&release.PublicPath,
		&release.ContentType,
		&release.FileSize,
		&release.IsActive,
		&release.CreatedAt,
		&release.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get active app release: %w", err)
	}
	return &release, nil
}

func GetAllActiveAppReleases() (map[string]AppRelease, error) {
	rows, err := db.DB.Query(`
		SELECT id, platform, version, COALESCE(release_notes, ''), file_name, file_path, public_path,
		       COALESCE(content_type, ''), file_size, is_active, created_at, updated_at
		FROM app_releases
		WHERE is_active = TRUE
		ORDER BY updated_at DESC, id DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("get active app releases: %w", err)
	}
	defer rows.Close()

	result := make(map[string]AppRelease)
	for rows.Next() {
		var release AppRelease
		if err := rows.Scan(
			&release.ID,
			&release.Platform,
			&release.Version,
			&release.ReleaseNotes,
			&release.FileName,
			&release.FilePath,
			&release.PublicPath,
			&release.ContentType,
			&release.FileSize,
			&release.IsActive,
			&release.CreatedAt,
			&release.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan active app release: %w", err)
		}
		if _, exists := result[release.Platform]; !exists {
			result[release.Platform] = release
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active app releases: %w", err)
	}
	return result, nil
}
