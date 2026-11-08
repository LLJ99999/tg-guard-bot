package guard

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"oai_bot/internal/model"
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

// PanelSendRich 面板发消息：Markdown + 媒体 + 按钮，可指定话题（0 用群设置的机器人话题）
func (g *Guard) PanelSendRich(ctx context.Context, groupID int64, text, media, buttons string, thread int) error {
	grp, err := g.st.Group(groupID, "")
	if err != nil {
		return err
	}
	if thread == 0 {
		thread = grp.BotThread
	}
	var mk models.ReplyMarkup
	if kb, err := ParseButtons(buttons); err != nil {
		return err
	} else if kb != nil {
		mk = kb
	}
	_, err = g.sendRich(ctx, groupID, thread, MarkdownToHTML(text), media, mk, 0)
	return err
}

// TopicsOf 面板下拉用的话题列表
func (g *Guard) TopicsOf(groupID int64) []model.ForumTopic {
	ts, _ := g.st.Topics(groupID)
	return ts
}

// AdminsOf 面板「谁能管理」用：群里的真实管理员列表
func (g *Guard) AdminsOf(ctx context.Context, groupID int64) []map[string]any {
	var out []map[string]any
	for id, cm := range g.admins.get(ctx, groupID) {
		u := memberUser(cm)
		if u == nil || u.IsBot {
			continue
		}
		can := cm.Type == models.ChatMemberTypeOwner || (cm.Administrator != nil && cm.Administrator.CanRestrictMembers)
		out = append(out, map[string]any{"id": id, "name": displayName(u), "username": u.Username,
			"owner": cm.Type == models.ChatMemberTypeOwner, "can_restrict": can})
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["owner"].(bool) && !out[j]["owner"].(bool) })
	return out
}

// PreviewWelcome 面板「私聊预览」：把欢迎语私聊发给机器人主人和管理员白名单
func (g *Guard) PreviewWelcome(ctx context.Context, groupID int64) (int, []string) {
	grp, err := g.st.Group(groupID, "")
	if err != nil {
		return 0, []string{err.Error()}
	}
	if grp.Welcome == "" && grp.WelcomeMedia == "" {
		return 0, []string{"还没有设置欢迎语"}
	}
	sent := 0
	var failed []string
	for _, uid := range g.previewTargets(grp) {
		u := &models.User{ID: uid, FirstName: "新人"}
		if x, _ := g.st.UserByID(uid); x != nil {
			u = &models.User{ID: x.ID, FirstName: x.FirstName, LastName: x.LastName, Username: x.Username}
		}
		body, markup := parseButtons(grp.Welcome)
		text := strings.NewReplacer("{name}", mention(u), "{group}", esc(grp.Title), "{id}", strconv.FormatInt(u.ID, 10),
			"{username}", onOff(u.Username != "", "@"+esc(u.Username), ""), "{first}", esc(u.FirstName)).Replace(MarkdownToHTML(body))
		var mk models.ReplyMarkup
		if markup != nil {
			mk = markup
		}
		if _, err := g.sendRich(ctx, uid, 0, text, grp.WelcomeMedia, mk, 0); err != nil {
			failed = append(failed, strconv.FormatInt(uid, 10))
			continue
		}
		sent++
	}
	return sent, failed
}
