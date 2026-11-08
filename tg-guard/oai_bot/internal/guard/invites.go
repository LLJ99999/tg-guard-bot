package guard

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"oai_bot/internal/model"

	tgbot "github.com/go-telegram/bot"

	"github.com/go-telegram/bot/models"
)

// ======================= 邀请统计 =======================

const invitePrefix = "inv:"

// InviteLinkFor 成员的专属邀请链接（没有就创建，需要机器人有「邀请用户」权限）
func (g *Guard) InviteLinkFor(ctx context.Context, grp *model.Group, uid int64) (string, error) {
	if l := g.st.InviteLinkOf(grp.ID, uid); l != nil && l.Link != "" {
		return l.Link, nil
	}
	link, err := g.tg.CreateChatInviteLink(ctx, &tgbot.CreateChatInviteLinkParams{ChatID: grp.ID, Name: invitePrefix + strconv.FormatInt(uid, 10),
		CreatesJoinRequest: grp.VerifyMode == model.VerifyJoinRequest})
	if err != nil {
		return "", fmt.Errorf("创建邀请链接失败（机器人需要「邀请用户」管理员权限）：%v", err)
	}
	g.st.SaveInviteLink(&model.InviteLink{GroupID: grp.ID, UserID: uid, Link: link.InviteLink, CreatedAt: time.Now()})
	return link.InviteLink, nil
}

// inviterOf 从邀请链接名字里取邀请人
func inviterOf(l *models.ChatInviteLink) int64 {
	if l == nil || !strings.HasPrefix(l.Name, invitePrefix) {
		return 0
	}
	id, _ := strconv.ParseInt(strings.TrimPrefix(l.Name, invitePrefix), 10, 64)
	return id
}

// recordInvite 进群 / 申请时记下是谁邀请的
func (g *Guard) recordInvite(groupID, uid int64, l *models.ChatInviteLink) {
	if by := inviterOf(l); by != 0 && by != uid {
		g.st.AddInvite(&model.Invite{GroupID: groupID, UserID: uid, InviterID: by, CreatedAt: time.Now()})
	}
}

// inviteVerified 被邀请的人通过验证：计入邀请数，给邀请人加积分
func (g *Guard) inviteVerified(ctx context.Context, grp *model.Group, uid int64) {
	inv := g.st.InviteOf(grp.ID, uid)
	if inv == nil || inv.Verified {
		return
	}
	g.st.UpdateInvite(grp.ID, uid, map[string]any{"verified": true, "has_left": false})
	if grp.InvitePoints > 0 && grp.PointsOn {
		g.st.AddPoints(grp.ID, inv.InviterID, grp.InvitePoints)
	}
	if !grp.InviteNotify {
		return
	}
	n := g.st.InviteCount(grp.ID, inv.InviterID)
	g.tg.Notify(ctx, inv.InviterID, fmt.Sprintf("🎉 你邀请的 %s 已加入「%s」，你已累计邀请 %d 人%s。", mentionID(uid, g.st.UserName(uid)), esc(grp.Title), n,
		onOff(grp.InvitePoints > 0 && grp.PointsOn, fmt.Sprintf("，获得 %d 积分", grp.InvitePoints), "")),
		&models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{{Text: "👥 我邀请的人", CallbackData: fmt.Sprintf("iv:%d:0", grp.ID)}}}}, 0)
}

// defaultInviteText 发链接的默认文案
const defaultInviteText = "🔗 你在「{group}」的专属邀请链接：\n{link}\n\n通过这个链接进群并完成验证的人会算在你名下，当前已邀请 {count} 人。"

// inviteMessage 按群里自定义的文案拼出链接消息和按钮
func (g *Guard) inviteMessage(grp *model.Group, u *models.User, link string) (string, *models.InlineKeyboardMarkup) {
	tpl := grp.InviteText
	if strings.TrimSpace(tpl) == "" {
		tpl = defaultInviteText
	}
	body, custom := parseButtons(tpl)
	text := strings.NewReplacer("{link}", esc(link), "{count}", strconv.Itoa(g.st.InviteCount(grp.ID, u.ID)), "{group}", esc(grp.Title),
		"{name}", mention(u)).Replace(MarkdownToHTML(body))
	if !strings.Contains(tpl, "{link}") {
		text += "\n" + esc(link)
	}
	share := "https://t.me/share/url?url=" + url.QueryEscape(link) + "&text=" + url.QueryEscape("来「"+grp.Title+"」一起聊")
	rows := [][]models.InlineKeyboardButton{
		{{Text: "📤 分享链接", URL: share}, {Text: "👥 我邀请的人", CallbackData: fmt.Sprintf("iv:%d:0", grp.ID)}},
	}
	if custom != nil {
		rows = append(custom.InlineKeyboard, rows...)
	}
	return text, &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}

// sendInviteLink /link：把专属链接私聊发给成员
func (g *Guard) sendInviteLink(ctx context.Context, grp *model.Group, u *models.User, inGroup bool) {
	link, err := g.InviteLinkFor(ctx, grp, u.ID)
	if err != nil {
		if inGroup {
			g.tg.Notify(ctx, grp.ID, esc(err.Error()), nil, 30*time.Second)
		} else {
			g.tg.Notify(ctx, u.ID, "暂时拿不到邀请链接，请联系群管理员（机器人需要「邀请用户」权限）。", nil, 0)
		}
		return
	}
	text, mk := g.inviteMessage(grp, u, link)
	if _, err := g.tg.HTML(ctx, u.ID, text, mk); err != nil && inGroup {
		g.tg.Notify(ctx, grp.ID, fmt.Sprintf("%s 请先点下面按钮私聊机器人，链接会发给你", mention(u)),
			&models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{{Text: "获取邀请链接", URL: fmt.Sprintf("https://t.me/%s?start=link%d", g.tg.Me().Username, grp.ID)}}}}, time.Minute)
		return
	}
	if inGroup {
		g.tg.Notify(ctx, grp.ID, fmt.Sprintf("%s 邀请链接已私聊发给你", mention(u)), nil, 20*time.Second)
	}
}

// inviteGroupsOf 开了邀请统计、并且这个人还在里面的群
func (g *Guard) inviteGroupsOf(ctx context.Context, uid int64) []model.Group {
	gs, _ := g.st.Groups()
	var out []model.Group
	for _, grp := range gs {
		if grp.BotLeft || !grp.InviteOn {
			continue
		}
		if cm, err := g.tg.GetChatMember(ctx, &tgbot.GetChatMemberParams{ChatID: grp.ID, UserID: uid}); err == nil && cm != nil && isIn(*cm) {
			out = append(out, grp)
		}
	}
	return out
}

// privateInvite 私聊发 /link 或「邀请链接」、/myinvites 或「我邀请的人」。list=true 看名单
func (g *Guard) privateInvite(ctx context.Context, u *models.User, list bool) {
	gs := g.inviteGroupsOf(ctx, u.ID)
	switch len(gs) {
	case 0:
		g.tg.Notify(ctx, u.ID, "你所在的群都没有开启邀请统计。", nil, 0)
		return
	case 1:
		if list {
			text, mk := g.inviteesText(&gs[0], u.ID, 0)
			g.tg.HTML(ctx, u.ID, text, mk)
		} else {
			g.sendInviteLink(ctx, &gs[0], u, false)
		}
		return
	}
	var rows [][]models.InlineKeyboardButton
	for _, grp := range gs {
		data := fmt.Sprintf("il:%d", grp.ID)
		if list {
			data = fmt.Sprintf("iv:%d:0", grp.ID)
		}
		rows = append(rows, []models.InlineKeyboardButton{{Text: truncate(grp.Title, 30), CallbackData: data}})
	}
	g.tg.Notify(ctx, u.ID, onOff(list, "要看哪个群的邀请名单？", "要哪个群的邀请链接？"), &models.InlineKeyboardMarkup{InlineKeyboard: rows}, 0)
}

const inviteePage = 20

// inviteesText 某人邀请的名单（私聊里分页看）
func (g *Guard) inviteesText(grp *model.Group, inviter int64, page int) (string, *models.InlineKeyboardMarkup) {
	rows, total := g.st.InvitesBy(grp.ID, inviter, inviteePage, page*inviteePage)
	ok := g.st.InviteCount(grp.ID, inviter)
	var b strings.Builder
	fmt.Fprintf(&b, "<b>👥 你在「%s」邀请的人</b>\n有效 <b>%d</b> 人 · 共 %d 人\n", esc(grp.Title), ok, total)
	if total == 0 {
		b.WriteString("\n还没有人通过你的链接进群。")
	}
	for i, r := range rows {
		fmt.Fprintf(&b, "\n%d. %s　%s　<i>%s</i>", page*inviteePage+i+1, mentionID(r.UserID, r.Name()), inviteState(r.Verified, r.Left), r.CreatedAt.In(cst).Format("01-02 15:04"))
	}
	var nav []models.InlineKeyboardButton
	if page > 0 {
		nav = append(nav, models.InlineKeyboardButton{Text: "‹ 上一页", CallbackData: fmt.Sprintf("iv:%d:%d", grp.ID, page-1)})
	}
	if (page+1)*inviteePage < int(total) {
		nav = append(nav, models.InlineKeyboardButton{Text: "下一页 ›", CallbackData: fmt.Sprintf("iv:%d:%d", grp.ID, page+1)})
	}
	kb := [][]models.InlineKeyboardButton{{{Text: "🔗 我的邀请链接", CallbackData: fmt.Sprintf("il:%d", grp.ID)}}}
	if len(nav) > 0 {
		kb = append([][]models.InlineKeyboardButton{nav}, kb...)
	}
	return b.String(), &models.InlineKeyboardMarkup{InlineKeyboard: kb}
}

func inviteState(verified, left bool) string {
	switch {
	case left:
		return "🚪 已退群"
	case verified:
		return "✅ 有效"
	}
	return "⏳ 待验证"
}

// onInviteCallback il:<群> 发链接；iv:<群>:<页> 看名单。只给自己看，不用校验权限
func (g *Guard) onInviteCallback(ctx context.Context, cq *models.CallbackQuery) bool {
	parts := strings.Split(cq.Data, ":")
	if len(parts) < 2 || (parts[0] != "il" && parts[0] != "iv") {
		return false
	}
	answer := func(t string) {
		g.tg.AnswerCallbackQuery(ctx, &tgbot.AnswerCallbackQueryParams{CallbackQueryID: cq.ID, Text: t})
	}
	gid, err := strconv.ParseInt(parts[1], 10, 64)
	grp, err2 := g.st.Group(gid, "")
	if err != nil || err2 != nil || !grp.InviteOn {
		answer("这个群没有开启邀请统计")
		return true
	}
	if parts[0] == "il" {
		answer("")
		g.sendInviteLink(ctx, grp, &cq.From, false)
		return true
	}
	page := 0
	if len(parts) > 2 {
		page, _ = strconv.Atoi(parts[2])
	}
	text, mk := g.inviteesText(grp, cq.From.ID, max(page, 0))
	answer("")
	if m := cq.Message.Message; m != nil && m.Chat.ID == cq.From.ID && strings.Contains(m.Text, "邀请的人") {
		g.tg.EditMessageText(ctx, &tgbot.EditMessageTextParams{ChatID: m.Chat.ID, MessageID: m.ID, Text: text, ParseMode: models.ParseModeHTML, ReplyMarkup: mk,
			LinkPreviewOptions: &models.LinkPreviewOptions{IsDisabled: tgbot.True()}})
		return true
	}
	g.tg.HTML(ctx, cq.From.ID, text, mk)
	return true
}
