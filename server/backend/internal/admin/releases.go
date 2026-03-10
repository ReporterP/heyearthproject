package admin

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/config"
)

var releaseFileSanitizer = regexp.MustCompile(`[^a-zA-Z0-9._-]`)

type AppReleaseResponse struct {
	ID           int    `json:"id"`
	Platform     string `json:"platform"`
	Version      string `json:"version"`
	ReleaseNotes string `json:"release_notes"`
	FileName     string `json:"file_name"`
	PublicPath   string `json:"public_path"`
	ContentType  string `json:"content_type"`
	FileSize     int64  `json:"file_size"`
	IsActive     bool   `json:"is_active"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

type activateReleaseRequest struct {
	ID int `json:"id"`
}

func ListReleasesHandler(w http.ResponseWriter, r *http.Request) {
	releases, err := biz.ListAppReleases()
	if err != nil {
		http.Error(w, "Failed to list releases", http.StatusInternalServerError)
		return
	}

	response := make([]AppReleaseResponse, 0, len(releases))
	for _, release := range releases {
		response = append(response, releaseToResponse(release))
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

func UploadReleaseHandler(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(512 << 20); err != nil {
		http.Error(w, "Invalid multipart body", http.StatusBadRequest)
		return
	}

	platform := biz.NormalizeReleasePlatform(r.FormValue("platform"))
	version := strings.TrimSpace(r.FormValue("version"))
	releaseNotes := strings.TrimSpace(r.FormValue("release_notes"))
	activate := strings.EqualFold(strings.TrimSpace(r.FormValue("activate")), "true")

	if !biz.IsSupportedReleasePlatform(platform) {
		http.Error(w, "Unsupported platform", http.StatusBadRequest)
		return
	}
	if version == "" {
		http.Error(w, "Version is required", http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Release file is required", http.StatusBadRequest)
		return
	}
	defer file.Close()

	fileName := sanitizeReleaseFileName(header.Filename)
	if fileName == "" {
		http.Error(w, "Invalid file name", http.StatusBadRequest)
		return
	}

	relativePublicPath := path.Join("/releases", platform, version, fileName)
	storedPath := filepath.Join(config.ReleasesDir(), platform, version, fileName)
	if err := os.MkdirAll(filepath.Dir(storedPath), 0755); err != nil {
		http.Error(w, "Failed to prepare release storage", http.StatusInternalServerError)
		return
	}

	dst, err := os.Create(storedPath)
	if err != nil {
		http.Error(w, "Failed to store release file", http.StatusInternalServerError)
		return
	}

	fileSize, copyErr := io.Copy(dst, file)
	closeErr := dst.Close()
	if copyErr != nil || closeErr != nil {
		http.Error(w, "Failed to write release file", http.StatusInternalServerError)
		return
	}

	release, err := biz.CreateAppRelease(biz.AppRelease{
		Platform:     platform,
		Version:      version,
		ReleaseNotes: releaseNotes,
		FileName:     fileName,
		FilePath:     storedPath,
		PublicPath:   relativePublicPath,
		ContentType:  header.Header.Get("Content-Type"),
		FileSize:     fileSize,
	})
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create release: %v", err), http.StatusInternalServerError)
		return
	}

	if activate {
		if err := biz.ActivateAppRelease(release.ID); err != nil {
			http.Error(w, fmt.Sprintf("Failed to activate release: %v", err), http.StatusInternalServerError)
			return
		}
		release.IsActive = true
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(releaseToResponse(*release))
}

func ActivateReleaseHandler(w http.ResponseWriter, r *http.Request) {
	var req activateReleaseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if req.ID <= 0 {
		http.Error(w, "Release id is required", http.StatusBadRequest)
		return
	}

	if err := biz.ActivateAppRelease(req.ID); err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(strings.ToLower(err.Error()), "not found") {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error(), status)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"message": "Release activated successfully"})
}

func sanitizeReleaseFileName(name string) string {
	name = strings.TrimSpace(filepath.Base(name))
	if name == "." || name == "" {
		return ""
	}
	return releaseFileSanitizer.ReplaceAllString(name, "_")
}

func releaseToResponse(release biz.AppRelease) AppReleaseResponse {
	return AppReleaseResponse{
		ID:           release.ID,
		Platform:     release.Platform,
		Version:      release.Version,
		ReleaseNotes: release.ReleaseNotes,
		FileName:     release.FileName,
		PublicPath:   release.PublicPath,
		ContentType:  release.ContentType,
		FileSize:     release.FileSize,
		IsActive:     release.IsActive,
		CreatedAt:    release.CreatedAt.Format(time.RFC3339),
		UpdatedAt:    release.UpdatedAt.Format(time.RFC3339),
	}
}
