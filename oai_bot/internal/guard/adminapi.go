package guard

import (
	"context"
	"fmt"
	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"time"

	"oai_bot/internal/store"
)

// 以下方法供内网管理接口（AstrBot 面板插件）调用

func (g *Guard) Store() *store.Store { return g.st }

// Info 机器人基本状态
func (g *Guard) Info() map[string]any {
	me := g.tg.Me()
	return map[string]any{
		"observe_only": g.cfg.Bot.ObserveOnly,
		"bot_id":       me.ID,
		"bot_username": me.Username,
		"bot_name":     me.FirstName,
		"captcha":      g.captcha != nil,
		"ai":           g.ai.enabled(),
		"ai_model":     g.ai.conf().Model,
		"cas":          g.cfg.CAS.Enabled,
		"log_chat_id":  g.cfg.Bot.LogChatID,
		"timezone":     g.cfg.Bot.Timezone,
		"started_at":   g.started.Format(time.RFC3339),
		"uptime_sec":   int(time.Since(g.started).Seconds()),
		"open_reports": g.st.OpenReportCount(),
	}
}

// PanelUnban 面板里解封某人
func (g *Guard) PanelUnban(ctx context.Context, chatID, userID int64) error {
	if !g.cfg.GroupAllowed(chatID) {
		return fmt.Errorf("group not allowed")
	}
	g.noteUndo(chatID, userID, undoNote{By: "面板"})
	if err := clearMemberRestriction(ctx, g.tg, chatID, userID); err != nil {
		return err
	}
	g.st.UpdateMember(chatID, userID, map[string]any{"warns": 0})
	g.logf(ctx, "面板解封 %s\n群：%d", mentionID(userID, ""), chatID)
	return nil
}

type memberRestrictionClient interface {
	GetChatMember(context.Context, *tgbot.GetChatMemberParams) (*models.ChatMember, error)
	Unmute(context.Context, int64, int64) error
	Unban(context.Context, int64, int64) error
}

func clearMemberRestriction(ctx context.Context, tg memberRestrictionClient, chatID, userID int64) error {
	m, err := tg.GetChatMember(ctx, &tgbot.GetChatMemberParams{ChatID: chatID, UserID: userID})
	if err != nil {
		return err
	}
	if m == nil {
		return fmt.Errorf("empty member status")
	}
	if m.Type == models.ChatMemberTypeRestricted {
		return tg.Unmute(ctx, chatID, userID)
	}
	return tg.Unban(ctx, chatID, userID)
}

// PanelGBan 面板全局封禁 / 解除：同时在所有已知群执行
func (g *Guard) PanelGBan(ctx context.Context, userID int64, reason string, on bool) error {
	if err := g.st.SetGBan(userID, reason, on); err != nil {
		return err
	}
	gs, _ := g.st.Groups()
	for _, grp := range gs {
		if !g.cfg.GroupAllowed(grp.ID) {
			continue
		}
		if on {
			g.tg.Ban(ctx, grp.ID, userID, 0, true)
		} else {
			g.tg.Unban(ctx, grp.ID, userID)
		}
	}
	g.logf(ctx, "%s %s（面板，%d 个群）%s", onOff(on, "全局封禁", "解除全局封禁"), mentionID(userID, ""), len(gs), onOff(reason != "", "\n原因："+esc(reason), ""))
	return nil
}

// PanelSend 面板向群里发一条文字
func (g *Guard) PanelSend(ctx context.Context, chatID int64, text string) error {
	if _, err := g.tg.HTML(ctx, chatID, esc(text), nil); err != nil {
		return fmt.Errorf("发送失败：%w", err)
	}
	return nil
}
