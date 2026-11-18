package main

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"unicode/utf16"

	tg "github.com/heyearth/backend/internal/telegram"
)

// Hedgehog scenes for the bot and the channel (spec 12 §12.6): GIF loops drawn by
// docs/telegram-brand/scenes/render.py, embedded so a deploy needs no extra files.
//
//go:embed tgscenes/*.gif
var tgSceneFS embed.FS

var tgScenes = []string{"wave", "coin", "piggy", "gift", "alarm", "wallet", "sleep", "news", "support"}

// tgSceneIDs: tgSceneKey(token, slug) → file_id after the first upload. file_ids belong to the
// bot that uploaded them, so the key carries the token: a send racing a token change can never
// hand the new bot the old bot's id (the change also clears it, together with tgPhotoIDs).
var tgSceneIDs sync.Map

// tgCaptionMax is Telegram's caption limit (UTF-16 units of the text without markup).
const tgCaptionMax = 1024

func isTGScene(slug string) bool {
	for _, s := range tgScenes {
		if s == slug {
			return true
		}
	}
	return false
}

func tgSceneGIF(slug string) ([]byte, bool) {
	if !isTGScene(slug) {
		return nil, false
	}
	data, err := tgSceneFS.ReadFile("tgscenes/" + slug + ".gif")
	return data, err == nil
}

// tgCaptionLen is the caption length as Telegram counts it: UTF-16 units of the text without markup.
func tgCaptionLen(text string) int { return len(utf16.Encode([]rune(tg.StripHTML(text)))) }

// tgCaptionFits: the text can go as a caption under a picture.
func tgCaptionFits(text string) bool { return tgCaptionLen(text) <= tgCaptionMax }

// GET /api/v1/tg/scenes/<slug>.gif — public brand art (the admin news picker shows it).
func handleTGScene(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/api/v1/tg/scenes/")
	slug, ok := strings.CutSuffix(name, ".gif")
	data, found := tgSceneGIF(slug)
	if !ok || !found {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/gif")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(data)
}

// tgSceneKey: the cache key of a scene's file_id — per bot (a short hash of its token) and scene.
func tgSceneKey(token, slug string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:6]) + ":" + slug
}
