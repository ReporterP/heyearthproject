package main

import (
	"context"

	tg "github.com/heyearth/backend/internal/telegram"
)

// Temporary: filled in by stages TG-C (support) and TG-D (news).
func tgSupportStart(c *tgCtx)                               {}
func tgSupportEnd(c *tgCtx, msgID int)                      {}
func tgSupportMessage(c *tgCtx, m *tg.Message)              {}
func isReplyToSupport(c *tgCtx, m *tg.Message) bool         { return false }
func tgNews(c *tgCtx)                                       {}
func handleAlertBotUpdate(ctx context.Context, u tg.Update) {}
