package guard

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"
	"unicode"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"oai_bot/internal/model"
)

// onGroupMessage 群消息入口：服务消息清理 → 命令 → 反垃圾检查
func (g *Guard) onGroupMessage(ctx context.Context, m *models.Message) {
	if !g.cfg.GroupAllowed(m.Chat.ID) {
		if m.From != nil && !g.cfg.IsOwner(m.From.ID) {
			g.tg.LeaveChat(ctx, &tgbot.LeaveChatParams{ChatID: m.Chat.ID})
		}
		return
	}
	// 清理「某某加入了群 / 离开了群」服务消息
	if len(m.NewChatMembers) > 0 || m.LeftChatMember != nil {
		if len(m.NewChatMembers) > 0 {
			g.markJoinedFallback(ctx, m)
		}
		g.tg.Delete(ctx, m.Chat.ID, m.ID)
		return
	}
	if m.From != nil {
		g.st.SaveUser(m.From.ID, m.From.Username, m.From.FirstName, m.From.LastName)
	}
	g.count(m.Chat.ID, "messages")
	if name, arg := parseCommand(m.Text, g.tg.Me().Username); name != "" {
		if g.runCommand(ctx, m, name, arg) {
			return
		}
	}
	if !g.checkMessage(ctx, m, false) {
		g.respond(ctx, m)
	}
}

// exempt 不检查的发送者：管理员、匿名管理员、关联频道自动转发、机器人主人
func (g *Guard) exempt(ctx context.Context, m *models.Message) bool {
	if m.IsAutomaticForward {
		return true
	}
	if m.SenderChat != nil && (m.SenderChat.ID == m.Chat.ID || m.SenderChat.ID == g.admins.linkedChannel(ctx, m.Chat.ID)) {
		return true // 匿名管理员 / 关联频道身份发言
	}
	if m.From == nil {
		return true
	}
	if m.From.ID == g.tg.Me().ID || g.cfg.IsOwner(m.From.ID) {
		return true
	}
	return g.admins.isAdmin(ctx, m.Chat.ID, m.From.ID) || g.st.Approved(m.Chat.ID, m.From.ID)
}

// verdict 一次检查的结论
type verdict struct {
	reason string
	action string // warn | ban | flood
	source string // ai / learned / rule（处罚记录用）
}

// checkMessage 反垃圾流水线：便宜的规则在前，AI 只给新人的可疑消息用。返回 true 表示已处罚
func (g *Guard) checkMessage(ctx context.Context, m *models.Message, edited bool) bool {
	if m.From != nil && !m.From.IsBot && m.SenderChat == nil && !edited {
		g.st.Member(m.Chat.ID, m.From.ID) // 管理员和白名单也记进成员列表（面板、主页查重要用）
		g.queueBio(m.Chat.ID, m.From.ID)
	}
	if g.exempt(ctx, m) {
		return false
	}
	grp, err := g.st.Group(m.Chat.ID, m.Chat.Title)
	if err != nil {
		slog.Error("group", "chat", m.Chat.ID, "err", err)
		return false
	}
	uid := senderID(m)
	mem, err := g.st.Member(m.Chat.ID, uid)
	if err != nil {
		return false
	}
	if !edited {
		g.st.IncMsgCount(m.Chat.ID, uid)
	}
	// 只有机器人在群里之后进群的人才算新人；机器人加入前就在群里的老成员不受新人限制
	newbie := grp.NewbieMsgs > 0 && mem.Newcomer && mem.MsgCount < grp.NewbieMsgs

	v := g.ruleCheck(ctx, grp, m, mem, newbie)
	if v == nil && !edited {
		if ids := g.flood.hit(memberKey(m.Chat.ID, uid), m.ID, grp.FloodCount, time.Duration(grp.FloodSecond)*time.Second); ids != nil {
			g.tg.Delete(ctx, m.Chat.ID, ids...)
			v = &verdict{reason: fmt.Sprintf("%d 秒内发送超过 %d 条消息", grp.FloodSecond, grp.FloodCount), action: "flood"}
		}
	}
	if v == nil && !edited && grp.DupCheck {
		if msgs, hit := g.dup.hit(m.Chat.ID, uid, m.ID, msgText(m)); hit {
			v = g.dupWave(ctx, grp, m, msgs)
		}
	}
	skipAI := false
	if v == nil && msgText(m) != "" {
		v, skipAI = g.learnedCheck(grp, m, newbie || grp.AIAll)
	}
	if v == nil && !skipAI && grp.AI && (newbie || grp.AIAll) {
		v = g.aiCheck(ctx, grp, m, newbie)
	}
	if v != nil {
		g.punish(ctx, grp, m, v)
		return true
	}
	return false
}

// dupWave 重复内容触发：删除所有同文消息，其他发送者各警告一次（当前发送者由调用方处罚）
func (g *Guard) dupWave(ctx context.Context, grp *model.Group, m *models.Message, msgs []dupMsg) *verdict {
	uid := senderID(m)
	var ids []int
	others := map[int64]bool{}
	for _, d := range msgs {
		ids = append(ids, d.msgID)
		if d.userID != uid {
			others[d.userID] = true
		}
	}
	g.tg.Delete(ctx, m.Chat.ID, ids...)
	for id := range others {
		if !g.admins.isAdmin(ctx, m.Chat.ID, id) && !g.st.Approved(m.Chat.ID, id) {
			g.warn(ctx, grp, m.Chat.ID, id, mentionID(id, ""), "重复发送相同内容（疑似广告）")
		}
	}
	return &verdict{reason: "重复发送相同内容（疑似广告）", action: "warn"}
}

func (g *Guard) ruleCheck(ctx context.Context, grp *model.Group, m *models.Message, mem *model.Member, newbie bool) *verdict {
	uid := senderID(m)
	if g.st.GBanned(uid) {
		return &verdict{reason: "全局封禁", action: "ban"}
	}
	// 只在前几条消息查 CAS，避免每条都请求
	if newbie && grp.CAS && m.From != nil && g.cas.banned(ctx, uid) {
		return &verdict{reason: "CAS 黑名单", action: "ban"}
	}
	if m.SenderChat != nil {
		// 以其它频道身份发言（马甲频道）：直接删除并封禁该频道
		return &verdict{reason: "以频道身份发言", action: "ban"}
	}
	switch {
	case grp.LockForward && m.ForwardOrigin != nil:
		return &verdict{reason: "禁止转发消息", action: "warn"}
	case grp.LockButton && m.ReplyMarkup != nil && len(m.ReplyMarkup.InlineKeyboard) > 0:
		return &verdict{reason: "禁止带按钮的消息", action: "warn"}
	case grp.LockViaBot && m.ViaBot != nil:
		return &verdict{reason: "禁止通过 inline 机器人发消息", action: "warn"}
	case grp.LockStory && m.Story != nil:
		return &verdict{reason: "禁止转发动态", action: "warn"}
	case grp.LockSticker && m.Sticker != nil:
		return &verdict{reason: "禁止发送贴纸", action: "warn"}
	case grp.LockGIF && m.Animation != nil:
		return &verdict{reason: "禁止发送动图", action: "warn"}
	case grp.LockMedia && (len(m.Photo) > 0 || m.Video != nil || m.Document != nil || m.Audio != nil) && m.Animation == nil:
		return &verdict{reason: "禁止发送图片 / 视频 / 文件", action: "warn"}
	case grp.LockVoice && (m.Voice != nil || m.VideoNote != nil):
		return &verdict{reason: "禁止发送语音", action: "warn"}
	case grp.LockContact && (m.Contact != nil || m.Location != nil || m.Venue != nil):
		return &verdict{reason: "禁止发送名片和位置", action: "warn"}
	case grp.LockPoll && m.Poll != nil:
		return &verdict{reason: "禁止发起投票", action: "warn"}
	case grp.LockGame && (m.Dice != nil || m.Game != nil):
		return &verdict{reason: "禁止发送骰子和游戏", action: "warn"}
	case grp.LockInvite && hasInvite(m):
		return &verdict{reason: "禁止发送其它群的邀请链接", action: "warn"}
	case (grp.LockLink || newbie) && hasLink(m):
		if newbie && !grp.LockLink {
			return &verdict{reason: "新成员暂时不能发链接", action: "warn"}
		}
		return &verdict{reason: "禁止发送链接", action: "warn"}
	}
	text := msgText(m)
	if text == "" {
		return nil
	}
	if grp.LockForeign && foreignText(text) {
		return &verdict{reason: "禁止发送外文消息", action: "warn"}
	}
	fs, _ := g.st.Filters(grp.ID)
	low := strings.ToLower(text)
	for _, f := range fs {
		if f.Regex {
			re, err := regexp.Compile(f.Pattern)
			if err == nil && re.MatchString(text) {
				return &verdict{reason: "触发屏蔽规则", action: filterAction(f.Action)}
			}
		} else if strings.Contains(low, strings.ToLower(f.Pattern)) {
			return &verdict{reason: "触发屏蔽词", action: filterAction(f.Action)}
		}
	}
	return nil
}

func (g *Guard) aiCheck(ctx context.Context, grp *model.Group, m *models.Message, newbie bool) *verdict {
	text := msgText(m)
	if len([]rune(text)) < 6 && !hasLink(m) {
		return nil
	}
	if m.ForwardOrigin != nil {
		text = "[转发消息] " + text
	}
	v, err := g.ai.check(ctx, displayName(m.From), text, newbie, g.aiExamples())
	if err != nil {
		slog.Warn("ai check", "err", err)
		if g.aiErrCD.allow("ai") { // 每 10 分钟最多记一次，方便在面板日志里发现 AI 配置问题
			g.logf(ctx, "AI 审核调用失败：%s", esc(truncate(err.Error(), 200)))
		}
		return nil
	}
	if v == nil || !v.Spam || v.Confidence < g.ai.conf().Threshold {
		return nil
	}
	action := grp.AIAction
	if action == "" {
		action = model.ActionBan
	}
	return &verdict{reason: fmt.Sprintf("AI 判定：%s（%.0f%%）%s", v.Category, v.Confidence*100, v.Reason), action: action, source: "ai"}
}

// punish 删消息并按结论处罚
func (g *Guard) punish(ctx context.Context, grp *model.Group, m *models.Message, v *verdict) {
	g.tg.Delete(ctx, m.Chat.ID, m.ID)
	g.count(m.Chat.ID, "deleted")
	uid := senderID(m)
	who := mentionID(uid, displayNameOrChat(m))
	src := v.source
	if src == "" {
		src = "rule"
		if v.action == "flood" {
			src = "flood"
		}
	}
	g.note(m.Chat.ID, uid, punNote{Source: src, Reason: v.reason, Text: msgText(m), Name: displayNameOrChat(m)})
	switch v.action {
	case "ban":
		if m.SenderChat != nil {
			g.tg.BanChatSenderChat(ctx, &tgbot.BanChatSenderChatParams{ChatID: m.Chat.ID, SenderChatID: m.SenderChat.ID})
		} else {
			g.tg.Ban(ctx, m.Chat.ID, uid, 0, true)
		}
		g.count(m.Chat.ID, "banned")
		g.tg.Notify(ctx, m.Chat.ID, fmt.Sprintf("已封禁 %s\n原因：%s", who, esc(v.reason)), unbanButton(m.Chat.ID, uid), time.Minute)
		g.logf(ctx, "封禁 %s（%d）\n群：%s\n原因：%s\n内容：%s", who, uid, esc(grp.Title), esc(v.reason), esc(truncate(msgText(m), 200)))
	case "delete": // 只删除，不提示
		g.logf(ctx, "删除 %s（%d）的消息\n群：%s\n原因：%s\n内容：%s", who, uid, esc(grp.Title), esc(v.reason), esc(truncate(msgText(m), 200)))
	case model.ActionMute:
		g.tg.Mute(ctx, m.Chat.ID, uid, 24*time.Hour)
		g.count(m.Chat.ID, "muted")
		g.tg.Notify(ctx, m.Chat.ID, fmt.Sprintf("已禁言 %s 24 小时\n原因：%s", who, esc(v.reason)), unmuteButton(m.Chat.ID, uid), time.Minute)
		g.logf(ctx, "禁言 %s（%d）\n群：%s\n原因：%s\n内容：%s", who, uid, esc(grp.Title), esc(v.reason), esc(truncate(msgText(m), 200)))
	case "flood":
		g.tg.Mute(ctx, m.Chat.ID, uid, 10*time.Minute)
		g.count(m.Chat.ID, "muted")
		g.tg.Notify(ctx, m.Chat.ID, fmt.Sprintf("%s 刷屏，禁言 10 分钟\n原因：%s", who, esc(v.reason)), unmuteButton(m.Chat.ID, uid), time.Minute)
		g.logf(ctx, "刷屏禁言 %s（%d），群：%s", who, uid, esc(grp.Title))
	default:
		g.warn(ctx, grp, m.Chat.ID, uid, who, v.reason)
	}
}

// warn 警告 +1，达到上限执行处罚并清零
func (g *Guard) warn(ctx context.Context, grp *model.Group, chatID, uid int64, who, reason string) {
	n, err := g.st.AddWarn(chatID, uid, 1, time.Duration(grp.WarnExpire)*time.Hour)
	if err != nil {
		return
	}
	limit := max(grp.WarnLimit, 1)
	if n < limit {
		g.tg.Notify(ctx, chatID, fmt.Sprintf("%s 警告 %d/%d\n原因：%s", who, n, limit, esc(reason)),
			&models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{
				{Text: "撤销警告（管理员）", CallbackData: fmt.Sprintf("unwarn:%d:%d", chatID, uid)},
			}}}, time.Minute)
		return
	}
	g.st.UpdateMember(chatID, uid, map[string]any{"warns": 0})
	g.note(chatID, uid, punNote{Source: "warn", Reason: fmt.Sprintf("警告满 %d 次，最后一次：%s", limit, reason)})
	var did string
	var markup models.ReplyMarkup
	switch grp.WarnAction {
	case model.ActionBan:
		g.count(chatID, "banned")
		g.tg.Ban(ctx, chatID, uid, 0, false)
		did, markup = "封禁", unbanButton(chatID, uid)
	case model.ActionKick:
		g.tg.Kick(ctx, chatID, uid)
		did = "踢出"
	default:
		g.count(chatID, "muted")
		g.tg.Mute(ctx, chatID, uid, 24*time.Hour)
		did, markup = "禁言 24 小时", unmuteButton(chatID, uid)
	}
	g.tg.Notify(ctx, chatID, fmt.Sprintf("%s 警告已满 %d 次，%s\n最后一次原因：%s", who, limit, did, esc(reason)), markup, 2*time.Minute)
	g.logf(ctx, "警告满 %d 次 %s %s（%d），群：%s", limit, did, who, uid, esc(grp.Title))
}

func unbanButton(chatID, uid int64) *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{
		{Text: "解除封禁（管理员）", CallbackData: fmt.Sprintf("unban:%d:%d", chatID, uid)},
	}}}
}

func unmuteButton(chatID, uid int64) *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{
		{Text: "解除禁言（管理员）", CallbackData: fmt.Sprintf("unmute:%d:%d", chatID, uid)},
	}}}
}

func displayNameOrChat(m *models.Message) string {
	if m.SenderChat != nil {
		return m.SenderChat.Title
	}
	return displayName(m.From)
}

func msgText(m *models.Message) string {
	if m.Text != "" {
		return m.Text
	}
	return m.Caption
}

var linkRe = regexp.MustCompile(`(?i)(https?://|www\.|t\.me/|telegram\.me/|tg://)`)

// hasLink 是否含链接或 @ 频道 / 群（文字和说明文字的 entities 都看）
func hasLink(m *models.Message) bool {
	for _, es := range [][]models.MessageEntity{m.Entities, m.CaptionEntities} {
		for _, e := range es {
			switch e.Type {
			case models.MessageEntityTypeURL, models.MessageEntityTypeTextLink, models.MessageEntityTypeMention:
				return true
			}
		}
	}
	return linkRe.MatchString(msgText(m))
}

func filterAction(a string) string {
	switch a {
	case "delete", model.ActionMute, model.ActionBan:
		return a
	}
	return "warn"
}

var inviteRe = regexp.MustCompile(`(?i)(t\.me|telegram\.me|telegram\.dog)/(\+|joinchat/)|tg://join`)

// hasInvite 是否含 Telegram 群 / 频道邀请链接（文字或隐藏在文字链接里）
func hasInvite(m *models.Message) bool {
	if inviteRe.MatchString(msgText(m)) {
		return true
	}
	for _, es := range [][]models.MessageEntity{m.Entities, m.CaptionEntities} {
		for _, e := range es {
			if e.Type == models.MessageEntityTypeTextLink && inviteRe.MatchString(e.URL) {
				return true
			}
		}
	}
	return false
}

// foreignText 外文为主：阿拉伯文、希伯来文、西里尔文、天城文、泰文等字母占一半以上（至少 8 个字母）
func foreignText(s string) bool {
	var letters, foreign int
	for _, r := range s {
		if !unicode.IsLetter(r) {
			continue
		}
		letters++
		if unicode.In(r, unicode.Arabic, unicode.Hebrew, unicode.Cyrillic, unicode.Devanagari, unicode.Thai,
			unicode.Bengali, unicode.Tamil, unicode.Khmer, unicode.Myanmar, unicode.Georgian, unicode.Armenian) {
			foreign++
		}
	}
	return foreign >= 8 && foreign*2 >= letters
}
