package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/admin"
	"github.com/heyearth/backend/internal/biz"
	tg "github.com/heyearth/backend/internal/telegram"
)

// ─────────────────────── Admin «Telegram» (plan TG7) ───────────────────────
//
// GET/PUT /api/admin/telegram/settings, POST /api/admin/telegram/check,
// POST /api/admin/telegram/channel/check. The token is write-only: GET returns a hint only.

func registerTelegramAdminRoutes() {
	http.HandleFunc("/api/admin/telegram/settings", func(w http.ResponseWriter, r *http.Request) {
		perm := "settings.write"
		if r.Method == http.MethodGet {
			perm = "settings.read"
		}
		admin.RequirePermission(perm, handleAdminTelegramSettings)(w, r)
	})
	http.HandleFunc("/api/admin/telegram/check", admin.RequirePermission("settings.write", handleAdminTelegramCheck))
	http.HandleFunc("/api/admin/telegram/channel/check", admin.RequirePermission("settings.write", handleAdminTelegramChannelCheck))
}

type tgAdminSettingsRequest struct {
	BotToken   string  `json:"bot_token"`
	ClearToken bool    `json:"clear_token"`
	ChannelID  *string `json:"channel_id"`
}

func tgAdminState() map[string]any {
	token := userBotToken()
	cfg, _ := alertTelegram.load()
	return map[string]any{
		"bot": map[string]any{
			"token_set": token != "", "token_hint": maskAlertToken(token), "username": userBotUsername(),
			"mini_app_url": siteURL() + "/tg", "bot_url": botLink(""),
		},
		"channel": map[string]any{"id": tgChannelID()},
		"alerts":  map[string]any{"token_set": cfg.token != "", "chat_id": cfg.chatID},
		"route":   map[string]any{"direct_down": telegram.DirectDown()},
	}
}

func handleAdminTelegramSettings(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	switch r.Method {
	case http.MethodGet:
		tgCfg.forget(tgBotTokenSetting, tgBotUsernameSetting, tgChannelSetting)
		writeJSON(w, http.StatusOK, tgAdminState())
	case http.MethodPut:
		var req tgAdminSettingsRequest
		if err := parseJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
			return
		}
		token := strings.TrimSpace(req.BotToken)
		if token != "" && !telegramTokenRe.MatchString(token) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "неверный формат токена (ожидается 123456789:AA…)"})
			return
		}
		details := map[string]any{"token_changed": token != "" || req.ClearToken}
		var setupErrors []string
		if token != "" {
			if alertTok, _ := alertTelegram.load(); alertTok.token == token {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "это токен бота алертов — для пользователей нужен отдельный бот"})
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
			me, err := (&tg.Bot{Token: token, Doer: telegram}).GetMe(ctx)
			cancel()
			if err != nil {
				writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Telegram не принял токен: " + err.Error()})
				return
			}
			if err := biz.SetSetting(tgBotTokenSetting, token); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save token"})
				return
			}
			_ = biz.SetSetting(tgBotUsernameSetting, me.Username)
			_ = biz.SetSetting(tgBotOffsetSetting, "0") // a new bot starts its updates from scratch
			details["username"] = me.Username
			tgCfg.forget(tgBotTokenSetting, tgBotUsernameSetting)
			tgPhotoIDs.Clear() // file_ids belong to the old bot (audit F-10)
			setupErrors = setupUserBot(r.Context(), &tg.Bot{Token: token, Doer: telegram}, true)
		} else if req.ClearToken {
			_ = biz.SetSetting(tgBotTokenSetting, "")
			_ = biz.SetSetting(tgBotUsernameSetting, "")
			tgCfg.forget(tgBotTokenSetting, tgBotUsernameSetting)
			tgPhotoIDs.Clear()
		}
		if req.ChannelID != nil {
			ch := strings.TrimSpace(*req.ChannelID)
			if ch != "" && !telegramChatIDRe.MatchString(ch) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "неверный канал: @имя или число -100…"})
				return
			}
			if err := biz.SetSetting(tgChannelSetting, ch); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save channel"})
				return
			}
			details["channel_id"] = ch
			tgCfg.forget(tgChannelSetting)
		}
		admin.WriteAuditLog(r, "update_telegram_settings", "setting", "telegram_bot", details)
		out := tgAdminState()
		out["setup_errors"] = setupErrors
		writeJSON(w, http.StatusOK, out)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

// setupUserBot sets the menu, the Mini App button and the descriptions; returns what failed.
// setupUserBot sets the menu, the Mini App button and (when descriptions, i.e. on saving a token)
// the descriptions — «Проверить» keeps texts the owner may have changed in @BotFather.
func setupUserBot(parent context.Context, bot *tg.Bot, descriptions bool) []string {
	ctx, cancel := context.WithTimeout(parent, 90*time.Second)
	defer cancel()
	var errs []string
	step := func(name string, err error) {
		if err != nil {
			errs = append(errs, name+": "+err.Error())
			log.Printf("[tg-admin] %s: %v", name, err)
		}
	}
	step("deleteWebhook", bot.DeleteWebhook(ctx))
	step("setMyCommands", bot.SetMyCommands(ctx, tgCommands))
	step("setChatMenuButton", bot.SetChatMenuButton(ctx, tg.MenuButton{Type: "web_app", Text: "Кабинет", WebApp: &tg.WebAppInfo{URL: miniAppURL("")}}))
	if descriptions {
		step("setMyDescription", bot.SetMyDescription(ctx, tgBotDescription))
		step("setMyShortDescription", bot.SetMyShortDescription(ctx, tgBotShortDescription))
	}
	refreshInlineFlag(ctx, bot)
	return errs
}

// POST /api/admin/telegram/check — getMe over the route; re-applies the menu when it works.
func handleAdminTelegramCheck(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	bot := userBot()
	if bot == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Токен бота не задан"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	me, err := bot.GetMe(ctx)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	if me.Username != userBotUsername() {
		_ = biz.SetSetting(tgBotUsernameSetting, me.Username)
		tgCfg.forget(tgBotUsernameSetting)
	}
	setupErrors := setupUserBot(r.Context(), bot, false)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "username": me.Username, "name": me.FirstName, "inline": me.SupportsInlineQueries,
		"direct_down": telegram.DirectDown(), "setup_errors": setupErrors})
}

// POST /api/admin/telegram/channel/check — the bot must be an administrator who may post.
func handleAdminTelegramChannelCheck(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	bot := userBot()
	if bot == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Сначала сохраните токен бота"})
		return
	}
	ch := tgChannelID()
	if ch == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Канал не указан"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	chat, err := bot.GetChat(ctx, ch)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Канал не найден или бот в нём не состоит: " + err.Error()})
		return
	}
	me, err := bot.GetMe(ctx)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	member, err := bot.GetChatMember(ctx, ch, me.ID)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Не удалось проверить права бота: " + err.Error()})
		return
	}
	canPost := member.Status == "creator" || (member.Status == "administrator" && (member.CanPostMessages == nil || *member.CanPostMessages))
	canEdit := member.Status == "creator" || (member.Status == "administrator" && (member.CanEditMessages == nil || *member.CanEditMessages))
	out := map[string]any{"ok": canPost && chat.Type == "channel", "title": chat.Title, "type": chat.Type, "username": chat.Username,
		"status": member.Status, "can_post": canPost, "can_edit": canEdit}
	if chat.Type != "channel" {
		out["error"] = "Это не канал, а " + chat.Type
	} else if !canPost {
		out["error"] = fmt.Sprintf("Бот в канале со статусом «%s» — сделайте его администратором с правом публикации", member.Status)
	}
	writeJSON(w, http.StatusOK, out)
}
