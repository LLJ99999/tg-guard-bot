// Package guard 群管核心：入群验证、反垃圾、管理命令。
package guard

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"oai_bot/internal/captcha"
	"oai_bot/internal/config"
	"oai_bot/internal/queue"
	"oai_bot/internal/store"
	"oai_bot/internal/telegram"
)

// VerifyPath 验证页路径
const (
	VerifyPath  = "/verify"
	WebhookPath = "/tg/webhook"
)

type Guard struct {
	cfg     *config.Config
	tg      *telegram.Client
	st      *store.Store
	q       *queue.Keyed
	admins  *adminCache
	flood   *floodTracker
	raid    *raidTracker
	dup     *dupTracker
	replyCD *cooldown
	aiErrCD *cooldown
	cas     *casClient
	ai      *aiClient
	bioScan bioScanner
	wl      whitelist
	learn   learnState
	captcha captcha.Provider // 第一个已配置的验证方式；nil 表示无法做 Mini App 验证
	started time.Time
}

func New(cfg *config.Config, tg *telegram.Client, st *store.Store) *Guard {
	g := &Guard{
		cfg: cfg, tg: tg, st: st, started: time.Now(),
		q:       queue.New(queue.DefaultSize),
		admins:  newAdminCache(tg),
		flood:   newFloodTracker(),
		raid:    newRaidTracker(),
		dup:     newDupTracker(),
		replyCD: &cooldown{d: 30 * time.Second},
		aiErrCD: &cooldown{d: 10 * time.Minute},
		cas:     newCASClient(cfg.CAS),
		ai:      newAIClient(cfg.AI),
	}
	if ps := captcha.FromConfig(cfg.Captcha); len(ps) > 0 && cfg.Web.PublicURL != "" {
		g.captcha = ps[0]
	} else {
		slog.Warn("未配置人机验证或 web.public_url，入群验证将不可用（join / restrict 模式会改为交给管理员处理）")
	}
	g.loadAISetting()
	g.admins.extra = g.inWhitelist
	tg.OnAction = g.onAction
	if !g.ai.enabled() {
		slog.Info("未配置 ai，AI 审核关闭")
	}
	tg.RegisterHandlerMatchFunc(func(*models.Update) bool { return true }, g.dispatch)
	return g
}

func (g *Guard) Captcha() captcha.Provider { return g.captcha }

func (g *Guard) WebhookHandler() http.Handler { return g.tg.WebhookHandler() }

// Run 开始接收更新并运行超时检查，阻塞到 ctx 结束
func (g *Guard) Run(ctx context.Context) error {
	g.syncCommands(ctx)
	if !g.cfg.Bot.ObserveOnly {
		go g.expireLoop(ctx)
		go g.flood.cleanupLoop(ctx)
		go g.dup.cleanupLoop(ctx)
		go g.timerLoop(ctx)
		go g.RunBioPrefetch(ctx)
	}
	if g.cfg.Bot.Mode == config.ModeWebhook {
		if _, err := g.tg.SetWebhook(ctx, &tgbot.SetWebhookParams{
			URL: g.cfg.Web.PublicURL + WebhookPath, SecretToken: g.tg.WebhookSecret, AllowedUpdates: telegram.AllowedUpdates,
		}); err != nil {
			return err
		}
		g.tg.StartWebhook(ctx)
		return nil
	}
	if _, err := g.tg.DeleteWebhook(ctx, nil); err != nil {
		return err
	}
	g.tg.Start(ctx)
	return nil
}

// dispatch 更新循环是串行的，这里只做分类并按「群:用户」入队；
// 同一个人在同一个群里的事件严格按序处理，不同人之间并发。
func (g *Guard) dispatch(ctx context.Context, _ *tgbot.Bot, upd *models.Update) {
	if g.cfg.Bot.ObserveOnly {
		if upd.Message != nil && upd.Message.Chat.Type == models.ChatTypePrivate && upd.Message.From != nil {
			m := upd.Message
			g.q.Do(queue.UserKey(m.From.ID), func() { g.onPrivate(ctx, m) })
		}
		return
	}
	if !g.updateAllowed(upd) {
		return
	}
	switch {
	case upd.MyChatMember != nil:
		u := upd.MyChatMember
		g.q.Do(chatKey(u.Chat.ID), func() { g.onMyChatMember(ctx, u) })
	case upd.ChatJoinRequest != nil:
		r := upd.ChatJoinRequest
		g.q.Do(memberKey(r.Chat.ID, r.From.ID), func() { g.onJoinRequest(ctx, r) })
	case upd.ChatMember != nil:
		u := upd.ChatMember
		slog.Info("chat_member", "chat", u.Chat.ID, "user", memberUserID(u.NewChatMember), "old", u.OldChatMember.Type, "new", u.NewChatMember.Type)
		g.q.Do(memberKey(u.Chat.ID, memberUserID(u.NewChatMember)), func() { g.onChatMember(ctx, u) })
	case upd.CallbackQuery != nil:
		cq := upd.CallbackQuery
		g.q.Do("cb:"+fmt.Sprint(cq.From.ID), func() { g.onCallback(ctx, cq) })
	case upd.Message != nil:
		m := upd.Message
		switch m.Chat.Type {
		case models.ChatTypePrivate:
			if m.From != nil {
				g.q.Do(queue.UserKey(m.From.ID), func() { g.onPrivate(ctx, m) })
			}
		case models.ChatTypeGroup, models.ChatTypeSupergroup:
			g.q.Do(memberKey(m.Chat.ID, senderID(m)), func() { g.onGroupMessage(ctx, m) })
		}
	case upd.EditedMessage != nil:
		// 编辑后再塞广告是常见手法：编辑过的消息同样过一遍检查
		m := upd.EditedMessage
		if m.Chat.Type == models.ChatTypeSupergroup || m.Chat.Type == models.ChatTypeGroup {
			g.q.Do(memberKey(m.Chat.ID, senderID(m)), func() { g.checkMessage(ctx, m, true) })
		}
	}
}

func (g *Guard) updateAllowed(upd *models.Update) bool {
	var id int64
	switch {
	case upd.MyChatMember != nil:
		id = upd.MyChatMember.Chat.ID
	case upd.ChatJoinRequest != nil:
		id = upd.ChatJoinRequest.Chat.ID
	case upd.ChatMember != nil:
		id = upd.ChatMember.Chat.ID
	case upd.EditedMessage != nil:
		id = upd.EditedMessage.Chat.ID
	case upd.CallbackQuery != nil:
		m := upd.CallbackQuery.Message.Message
		if m == nil || m.Chat.Type == models.ChatTypePrivate {
			return true
		}
		id = m.Chat.ID
	case upd.Message != nil:
		if upd.Message.Chat.Type == models.ChatTypePrivate {
			return true
		}
		id = upd.Message.Chat.ID
	default:
		return true
	}
	return g.cfg.GroupAllowed(id)
}

func chatKey(chatID int64) string { return fmt.Sprintf("c:%d", chatID) }

func memberKey(chatID, userID int64) string { return fmt.Sprintf("m:%d:%d", chatID, userID) }

func senderID(m *models.Message) int64 {
	if m.SenderChat != nil {
		return m.SenderChat.ID
	}
	if m.From != nil {
		return m.From.ID
	}
	return 0
}

// onMyChatMember 机器人自己在群里的身份变化：被拉进群 / 被设为管理员 / 被踢
func (g *Guard) onMyChatMember(ctx context.Context, u *models.ChatMemberUpdated) {
	if u.Chat.Type != models.ChatTypeGroup && u.Chat.Type != models.ChatTypeSupergroup {
		return
	}
	g.admins.invalidate(u.Chat.ID)
	if !isIn(u.NewChatMember) {
		return
	}
	if !g.cfg.GroupAllowed(u.Chat.ID) && !g.cfg.IsOwner(u.From.ID) {
		slog.Info("leave unallowed group", "chat", u.Chat.ID, "title", u.Chat.Title)
		g.tg.LeaveChat(ctx, &tgbot.LeaveChatParams{ChatID: u.Chat.ID})
		return
	}
	if _, err := g.st.Group(u.Chat.ID, u.Chat.Title); err != nil {
		slog.Error("init group", "chat", u.Chat.ID, "err", err)
	}
	if !isIn(u.OldChatMember) {
		g.tg.Notify(ctx, u.Chat.ID, "oai_bot 已就位。请把我设为管理员（删除消息、封禁用户、邀请用户），然后发 /settings 打开按钮面板。", nil, 0)
	} else if u.NewChatMember.Type == models.ChatMemberTypeAdministrator && u.OldChatMember.Type != models.ChatMemberTypeAdministrator {
		g.tg.Notify(ctx, u.Chat.ID, "已获得管理员权限，防护已开启。", nil, time.Minute)
	}
}

// expireLoop 定时处理超时未验证的入群
func (g *Guard) expireLoop(ctx context.Context) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			ps, err := g.st.ExpiredPendings(time.Now())
			if err != nil {
				slog.Error("expired pendings", "err", err)
				continue
			}
			for _, p := range ps {
				g.q.Do(memberKey(p.GroupID, p.UserID), func() { g.expire(ctx, p.GroupID, p.UserID) })
			}
		}
	}
}

func (g *Guard) syncCommands(ctx context.Context) {
	toCmds := func(list [][2]string) []models.BotCommand {
		out := make([]models.BotCommand, 0, len(list))
		for _, c := range list {
			out = append(out, models.BotCommand{Command: c[0], Description: c[1]})
		}
		return out
	}
	// 管理员菜单会覆盖「所有群」菜单，所以把群友命令也带上
	admin := append(toCmds(adminMenu), toCmds(memberMenu)...)
	sets := []struct {
		cmds  []models.BotCommand
		scope models.BotCommandScope
	}{
		{admin, &models.BotCommandScopeAllChatAdministrators{}},
		{toCmds(memberMenu), &models.BotCommandScopeAllGroupChats{}},
		{[]models.BotCommand{{Command: "settings", Description: "选择群并打开按钮面板"}, {Command: "help", Description: "使用说明"}}, &models.BotCommandScopeAllPrivateChats{}},
	}
	for _, s := range sets {
		if _, err := g.tg.SetMyCommands(ctx, &tgbot.SetMyCommandsParams{Commands: s.cmds, Scope: s.scope}); err != nil {
			slog.Warn("set commands", "err", err)
		}
	}
	// 清掉旧版本可能留在默认范围里的长菜单
	if _, err := g.tg.DeleteMyCommands(ctx, &tgbot.DeleteMyCommandsParams{Scope: &models.BotCommandScopeDefault{}}); err != nil {
		slog.Debug("delete default commands", "err", err)
	}
}

// logf 发送操作日志到 log_chat_id
func (g *Guard) logf(ctx context.Context, format string, args ...any) {
	text := fmt.Sprintf(format, args...)
	g.st.AddLog(stripHTML(text))
	if g.cfg.Bot.LogChatID == 0 {
		return
	}
	g.tg.Notify(ctx, g.cfg.Bot.LogChatID, text, nil, 0)
}

var tagRe = regexp.MustCompile(`<[^>]+>`)

// stripHTML 去掉标签、还原实体，日志存纯文本
func stripHTML(s string) string {
	return html.UnescapeString(tagRe.ReplaceAllString(s, ""))
}

// isIn 成员状态是否在群里
func isIn(m models.ChatMember) bool {
	switch m.Type {
	case models.ChatMemberTypeOwner, models.ChatMemberTypeAdministrator, models.ChatMemberTypeMember:
		return true
	case models.ChatMemberTypeRestricted:
		return m.Restricted != nil && m.Restricted.IsMember
	}
	return false
}

// esc HTML 转义
func esc(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

func displayName(u *models.User) string {
	if u == nil {
		return "未知用户"
	}
	n := strings.TrimSpace(u.FirstName + " " + u.LastName)
	if n == "" {
		n = "无名氏"
	}
	if r := []rune(n); len(r) > 32 {
		n = string(r[:32]) + "…"
	}
	return n
}

func mention(u *models.User) string {
	if u == nil {
		return "未知用户"
	}
	return fmt.Sprintf(`<a href="tg://user?id=%d">%s</a>`, u.ID, esc(displayName(u)))
}

func mentionID(id int64, name string) string {
	if name == "" {
		name = fmt.Sprint(id)
	}
	return fmt.Sprintf(`<a href="tg://user?id=%d">%s</a>`, id, esc(name))
}
