package guard

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"oai_bot/internal/model"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// ======================= 举报自动禁言 =======================

// reportAutoMute 同一条消息被足够多的人举报：先禁言 24 小时，等管理员处理
func (g *Guard) reportAutoMute(ctx context.Context, grp *model.Group, r *model.Report) {
	if grp.ReportAuto <= 0 || r.Count != grp.ReportAuto || g.admins.isAdmin(ctx, grp.ID, r.TargetID) {
		return
	}
	g.note(grp.ID, r.TargetID, punNote{Source: "report", Reason: fmt.Sprintf("%d 人举报，自动禁言", r.Count), Text: r.Text, Name: r.Target})
	if err := g.tg.Mute(ctx, grp.ID, r.TargetID, 24*time.Hour); err != nil {
		return
	}
	g.count(grp.ID, "muted")
	g.tg.Notify(ctx, grp.ID, fmt.Sprintf("%s 的消息已被 %d 人举报，先禁言 24 小时，等待管理员处理。", mentionID(r.TargetID, r.Target), r.Count), unmuteButton(grp.ID, r.TargetID), 10*time.Minute)
	g.logf(ctx, "举报 #%d 达到 %d 人，自动禁言 %s，群：%s", r.ID, r.Count, mentionID(r.TargetID, r.Target), esc(grp.Title))
}

// ======================= 投票踢人 =======================

const voteKickTTL = 10 * time.Minute

func (g *Guard) voteKick(ctx context.Context, grp *model.Group, m *models.Message) {
	r := m.ReplyToMessage
	if grp.VoteKick <= 0 {
		g.tg.Notify(ctx, grp.ID, "本群没有开启投票踢人", nil, 20*time.Second)
		return
	}
	if r == nil || r.From == nil || r.From.IsBot || m.From == nil {
		g.tg.Notify(ctx, grp.ID, "请回复要投票踢出的人的消息，再发 /votekick", nil, 20*time.Second)
		return
	}
	if r.From.ID == m.From.ID || g.admins.isAdmin(ctx, grp.ID, r.From.ID) || g.st.Approved(grp.ID, r.From.ID) {
		g.tg.Notify(ctx, grp.ID, "不能对这个人发起投票", nil, 20*time.Second)
		return
	}
	if v := g.st.OpenVoteKick(grp.ID, r.From.ID); v != nil && time.Since(v.CreatedAt) < voteKickTTL {
		g.tg.Notify(ctx, grp.ID, "已经有针对这个人的投票了，点上面的按钮投票", nil, 20*time.Second)
		return
	}
	v := &model.VoteKick{GroupID: grp.ID, TargetID: r.From.ID, Target: displayName(r.From), Reported: r.ID, Voters: strconv.FormatInt(m.From.ID, 10),
		Need: grp.VoteKick, Status: "open", CreatedAt: time.Now()}
	if err := g.st.CreateVoteKick(v); err != nil {
		return
	}
	msg := g.tg.Notify(ctx, grp.ID, voteKickText(v), voteKickMarkup(v), 0)
	if msg != nil {
		g.st.UpdateVoteKick(v.ID, map[string]any{"msg_id": msg.ID})
		g.deleteAfter(grp.ID, msg.ID, voteKickTTL+time.Minute)
	}
}

func voteKickText(v *model.VoteKick) string {
	n := len(strings.Fields(v.Voters))
	return fmt.Sprintf("🗳 投票踢出 %s\n%d 分钟内满 %d 票即踢出（当前 %d 票）。管理员可直接处理。", mentionID(v.TargetID, v.Target), int(voteKickTTL.Minutes()), v.Need, n)
}

func voteKickMarkup(v *model.VoteKick) *models.InlineKeyboardMarkup {
	n := len(strings.Fields(v.Voters))
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{
		{Text: fmt.Sprintf("👍 同意踢出（%d/%d）", n, v.Need), CallbackData: fmt.Sprintf("vk:%d", v.ID)},
	}}}
}

// onVoteKick 投票按钮
func (g *Guard) onVoteKick(ctx context.Context, cq *models.CallbackQuery, id uint) string {
	v := g.st.VoteKick(id)
	if v == nil || v.Status != "open" {
		return "投票已结束"
	}
	if time.Since(v.CreatedAt) > voteKickTTL {
		g.st.UpdateVoteKick(id, map[string]any{"status": "expired"})
		return "投票已过期"
	}
	uid := cq.From.ID
	if uid == v.TargetID || cq.From.IsBot {
		return "你不能投票"
	}
	me := strconv.FormatInt(uid, 10)
	for _, x := range strings.Fields(v.Voters) {
		if x == me {
			return "你已经投过票了"
		}
	}
	if cm, err := g.tg.GetChatMember(ctx, &tgbot.GetChatMemberParams{ChatID: v.GroupID, UserID: uid}); err != nil || cm == nil || !isIn(*cm) || cm.Type == models.ChatMemberTypeRestricted {
		return "只有能发言的群成员可以投票"
	}
	v.Voters = strings.TrimSpace(v.Voters + " " + me)
	g.st.UpdateVoteKick(id, map[string]any{"voters": v.Voters})
	if len(strings.Fields(v.Voters)) < v.Need {
		if m := cq.Message.Message; m != nil {
			g.tg.EditMessageText(ctx, &tgbot.EditMessageTextParams{ChatID: v.GroupID, MessageID: m.ID, Text: voteKickText(v), ParseMode: models.ParseModeHTML, ReplyMarkup: voteKickMarkup(v)})
		}
		return "已投票"
	}
	g.st.UpdateVoteKick(id, map[string]any{"status": "done"})
	g.note(v.GroupID, v.TargetID, punNote{Source: "vote", Reason: fmt.Sprintf("%d 人投票踢出", len(strings.Fields(v.Voters))), Name: v.Target})
	g.tg.Delete(ctx, v.GroupID, v.Reported)
	g.tg.Kick(ctx, v.GroupID, v.TargetID)
	if m := cq.Message.Message; m != nil {
		g.tg.EditMessageText(ctx, &tgbot.EditMessageTextParams{ChatID: v.GroupID, MessageID: m.ID, ParseMode: models.ParseModeHTML,
			Text: fmt.Sprintf("🗳 投票通过，已踢出 %s", mentionID(v.TargetID, v.Target))})
	}
	g.logf(ctx, "投票踢出 %s（%d 票），群 %d", mentionID(v.TargetID, v.Target), len(strings.Fields(v.Voters)), v.GroupID)
	return "已踢出"
}
