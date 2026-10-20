package guard

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"oai_bot/internal/model"
)

// 私聊设置面板：管理员在私聊里点按钮修改群设置，不在群里刷屏。
// 回调数据格式 s:<群ID>:<项>

// panelGroups 列出该用户能管理的群
func (g *Guard) panelGroups(ctx context.Context, userID int64) []model.Group {
	gs, _ := g.st.Groups()
	var out []model.Group
	for _, grp := range gs {
		if g.cfg.IsOwner(userID) || g.admins.canRestrict(ctx, grp.ID, userID) {
			out = append(out, grp)
		}
	}
	return out
}

func (g *Guard) sendGroupPicker(ctx context.Context, chatID, userID int64) {
	gs := g.panelGroups(ctx, userID)
	if len(gs) == 0 {
		g.tg.Notify(ctx, chatID, "没有找到你能管理的群。请先把我拉进群并设为管理员，在群里发一次 /settings。", nil, 0)
		return
	}
	var rows [][]models.InlineKeyboardButton
	for _, grp := range gs {
		rows = append(rows, []models.InlineKeyboardButton{{Text: truncate(grp.Title, 30), CallbackData: fmt.Sprintf("s:%d:r", grp.ID)}})
	}
	g.tg.Notify(ctx, chatID, "选择要设置的群：", &models.InlineKeyboardMarkup{InlineKeyboard: rows}, 0)
}

func (g *Guard) sendPanel(ctx context.Context, chatID, groupID int64) {
	grp, err := g.st.Group(groupID, "")
	if err != nil {
		return
	}
	g.tg.Notify(ctx, chatID, settingsText(grp, g.captcha != nil, g.ai.enabled()), g.panelMarkupFor(grp, false), 0)
}

// panelMarkupFor 设置面板按钮。inGroup 为 true 时是群里弹出的面板：底部换成「私聊打开 / 关闭」
func (g *Guard) panelMarkupFor(grp *model.Group, inGroup bool) *models.InlineKeyboardMarkup {
	mk := panelMarkup(grp)
	rows := mk.InlineKeyboard
	if inGroup {
		rows[len(rows)-1] = []models.InlineKeyboardButton{
			{Text: "刷新", CallbackData: fmt.Sprintf("s:%d:r", grp.ID)},
			{Text: "私聊打开", URL: fmt.Sprintf("https://t.me/%s?start=s%d", g.tg.Me().Username, grp.ID)},
			{Text: "关闭", CallbackData: fmt.Sprintf("s:%d:x", grp.ID)},
		}
	}
	return mk
}

func panelMarkup(grp *model.Group) *models.InlineKeyboardMarkup {
	b := func(text, key string) models.InlineKeyboardButton {
		return models.InlineKeyboardButton{Text: text, CallbackData: fmt.Sprintf("s:%d:%s", grp.ID, key)}
	}
	ck := func(on bool, name string) string { return onOff(on, "✓ ", "✗ ") + name }
	flood := "防刷屏：关"
	if grp.FloodCount > 0 {
		flood = fmt.Sprintf("防刷屏：%ds/%d条", grp.FloodSecond, grp.FloodCount)
	}
	raid := "防爆群：关"
	if grp.RaidLimit > 0 {
		raid = fmt.Sprintf("防爆群：%d人/分", grp.RaidLimit)
	}
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{
		{b("验证："+verifyName(grp.VerifyMode), "v")},
		{b("题目："+captchaName(grp.CaptchaType), "ct"), b("超时："+humanDur(time.Duration(grp.VerifyTimeout)*time.Second), "vt")},
		{b(flood, "fl"), b(raid, "rd")},
		{b(fmt.Sprintf("警告 %d 次后%s", grp.WarnLimit, actionName(grp.WarnAction)), "wa"), b(fmt.Sprintf("新人检查：%d 条", grp.NewbieMsgs), "nb")},
		{b(ck(grp.CAS, "CAS"), "cas"), b(ck(grp.AI, "AI 审核"), "ai"), b(ck(grp.DupCheck, "重复检测"), "dup")},
		{b("AI 范围："+onOff(grp.AIAll, "所有人", "仅新人"), "aa"), b("AI 处理："+aiActionName(grp.AIAction), "ax")},
		{b(ck(grp.LockLink, "链接"), "l:link"), b(ck(grp.LockForward, "转发"), "l:forward"), b(ck(grp.LockButton, "按钮"), "l:button")},
		{b(ck(grp.LockViaBot, "inline"), "l:viabot"), b(ck(grp.LockStory, "动态"), "l:story"), b(ck(grp.LockSticker, "贴纸"), "l:sticker")},
		{b(ck(grp.LockGIF, "GIF"), "l:gif"), b(ck(grp.LockMedia, "图片视频"), "l:media"), b(ck(grp.LockVoice, "语音"), "l:voice")},
		{b(ck(grp.LockInvite, "邀请链接"), "l:invite"), b(ck(grp.LockContact, "名片位置"), "l:contact"), b(ck(grp.LockForeign, "外文"), "l:foreign")},
		{b(ck(grp.ProfileCheck, "资料检查"), "pc"), b(ck(grp.CleanWelcome, "只留最新欢迎"), "cw")},
		{b(ck(grp.NightOn, fmt.Sprintf("夜间 %s-%s", grp.NightStart, grp.NightEnd)), "night")},
		{b("刷新", "r"), b("选择其它群", "list")},
	}}
}

// cycle 在候选值里取下一个
func cycle[T comparable](cur T, opts ...T) T {
	for i, o := range opts {
		if o == cur {
			return opts[(i+1)%len(opts)]
		}
	}
	return opts[0]
}

// onPanelCallback 处理私聊面板按钮
func (g *Guard) onPanelCallback(ctx context.Context, cq *models.CallbackQuery, data string) string {
	parts := strings.SplitN(data, ":", 3)
	if len(parts) != 3 {
		return ""
	}
	gid, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return ""
	}
	if !g.cfg.IsOwner(cq.From.ID) && !g.admins.canRestrict(ctx, gid, cq.From.ID) {
		return "你不是该群有封禁权限的管理员"
	}
	grp, err := g.st.Group(gid, "")
	if err != nil {
		return "读取失败"
	}
	key := parts[2]
	fields := map[string]any{}
	inGroup := false
	if m := cq.Message.Message; m != nil && m.Chat.Type != models.ChatTypePrivate {
		inGroup = true
	}
	switch {
	case key == "x":
		if m := cq.Message.Message; m != nil {
			g.tg.Delete(ctx, m.Chat.ID, m.ID)
		}
		return ""
	case key == "r":
	case key == "list":
		g.sendGroupPicker(ctx, cq.From.ID, cq.From.ID)
		return ""
	case key == "v":
		fields["verify_mode"] = cycle(grp.VerifyMode, model.VerifyOff, model.VerifyJoinRequest, model.VerifyRestrict)
	case key == "ct":
		types := []string{model.CaptchaImage, model.CaptchaMath, model.CaptchaEmoji, model.CaptchaButton}
		if g.captcha != nil {
			types = append(types, model.CaptchaWeb)
		}
		fields["captcha_type"] = cycle(grp.CaptchaType, types...)
	case key == "pc":
		fields["profile_check"] = !grp.ProfileCheck
	case key == "cw":
		fields["clean_welcome"] = !grp.CleanWelcome
	case key == "aa":
		fields["ai_all"] = !grp.AIAll
	case key == "ax":
		fields["ai_action"] = cycle(grp.AIAction, model.ActionBan, model.ActionMute, "warn")
	case key == "vt":
		fields["verify_timeout"] = cycle(grp.VerifyTimeout, 120, 300, 600, 1800)
	case key == "fl":
		cur := fmt.Sprintf("%d/%d", grp.FloodCount, grp.FloodSecond)
		next := cycle(cur, "0/10", "5/10", "8/10", "12/10")
		var c, s int
		fmt.Sscanf(next, "%d/%d", &c, &s)
		fields["flood_count"], fields["flood_second"] = c, s
	case key == "rd":
		fields["raid_limit"] = cycle(grp.RaidLimit, 0, 10, 15, 30)
	case key == "wa":
		cur := fmt.Sprintf("%d%s", grp.WarnLimit, grp.WarnAction)
		next := cycle(cur, "3mute", "3kick", "3ban", "5mute", "5ban")
		fields["warn_limit"], fields["warn_action"] = int(next[0]-'0'), next[1:]
	case key == "nb":
		fields["newbie_msgs"] = cycle(grp.NewbieMsgs, 0, 3, 5, 10)
	case key == "cas":
		fields["cas"] = !grp.CAS
	case key == "ai":
		if !grp.AI && !g.ai.enabled() {
			return "未配置 ai，无法开启"
		}
		fields["ai"] = !grp.AI
	case key == "dup":
		fields["dup_check"] = !grp.DupCheck
	case key == "night":
		fields["night_on"] = !grp.NightOn
	case strings.HasPrefix(key, "l:"):
		col, ok := lockColumns[key[2:]]
		if !ok {
			return ""
		}
		fields[col] = !lockValue(grp, key[2:])
	default:
		return ""
	}
	if len(fields) > 0 {
		if err := g.st.UpdateGroup(gid, fields); err != nil {
			return "保存失败"
		}
		grp, _ = g.st.Group(gid, "")
	}
	if m := cq.Message.Message; m != nil {
		g.tg.EditMessageText(ctx, &tgbot.EditMessageTextParams{
			ChatID: m.Chat.ID, MessageID: m.ID, Text: settingsText(grp, g.captcha != nil, g.ai.enabled()),
			ParseMode: models.ParseModeHTML, ReplyMarkup: g.panelMarkupFor(grp, inGroup),
		})
	}
	if key == "r" {
		return ""
	}
	return "已保存"
}

func lockValue(grp *model.Group, name string) bool {
	switch name {
	case "link":
		return grp.LockLink
	case "forward":
		return grp.LockForward
	case "button":
		return grp.LockButton
	case "viabot":
		return grp.LockViaBot
	case "story":
		return grp.LockStory
	case "sticker":
		return grp.LockSticker
	case "gif":
		return grp.LockGIF
	case "media":
		return grp.LockMedia
	case "voice":
		return grp.LockVoice
	case "invite":
		return grp.LockInvite
	case "contact":
		return grp.LockContact
	case "poll":
		return grp.LockPoll
	case "game":
		return grp.LockGame
	case "foreign":
		return grp.LockForeign
	}
	return false
}

func aiActionName(a string) string {
	switch a {
	case model.ActionMute:
		return "禁言"
	case "warn":
		return "删除+警告"
	}
	return "封禁"
}
