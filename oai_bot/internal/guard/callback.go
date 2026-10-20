package guard

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// onCallback 群内按钮：解封 / 解禁 / 撤销警告 / 放行 / 踢出，仅限有封禁权限的管理员
func (g *Guard) onCallback(ctx context.Context, cq *models.CallbackQuery) {
	answer := func(text string) {
		g.tg.AnswerCallbackQuery(ctx, &tgbot.AnswerCallbackQueryParams{CallbackQueryID: cq.ID, Text: text})
	}
	if strings.HasPrefix(cq.Data, "s:") {
		answer(g.onPanelCallback(ctx, cq, cq.Data))
		return
	}
	if strings.HasPrefix(cq.Data, "bsb:") {
		var rid uint
		var idx int
		if _, err := fmt.Sscanf(cq.Data, "bsb:%d:%d", &rid, &idx); err != nil {
			answer("")
			return
		}
		n, err := g.BanBioCluster(ctx, 0, rid, idx, cq.From.ID)
		if err != nil {
			answer(err.Error())
			return
		}
		answer(fmt.Sprintf("已封禁 %d 人", n))
		return
	}
	if msg, ok := g.onChallengeCallback(ctx, cq); ok {
		answer(msg)
		return
	}
	parts := strings.Split(cq.Data, ":")
	if len(parts) != 3 {
		answer("")
		return
	}
	chatID, err1 := strconv.ParseInt(parts[1], 10, 64)
	uid, err2 := strconv.ParseInt(parts[2], 10, 64)
	if err1 != nil || err2 != nil {
		answer("")
		return
	}
	// 按钮里带的群 ID 必须和按钮所在的群一致，防止伪造
	if m := cq.Message.Message; m == nil || m.Chat.ID != chatID {
		answer("无效操作")
		return
	}
	if !g.cfg.IsOwner(cq.From.ID) && !g.admins.canRestrict(ctx, chatID, cq.From.ID) {
		answer("仅限有封禁权限的管理员")
		return
	}
	if act, ok := strings.CutPrefix(parts[0], "rp"); ok && reportActions[act] != "" {
		r, err := g.st.Report(uint(uid))
		if err != nil || r == nil || r.GroupID != chatID || !g.cfg.GroupAllowed(chatID) {
			answer("无效操作")
			return
		}
		text, err := g.HandleReport(ctx, uint(uid), act, displayName(&cq.From))
		if err != nil {
			answer(err.Error())
			return
		}
		answer(text)
		return
	}
	var err error
	var text string
	switch parts[0] {
	case "unban":
		g.noteUndo(chatID, uid, undoNote{By: displayName(&cq.From)})
		err, text = g.tg.Unban(ctx, chatID, uid), "已解除封禁"
	case "unmute":
		g.noteUndo(chatID, uid, undoNote{By: displayName(&cq.From)})
		err, text = g.tg.Unmute(ctx, chatID, uid), "已解除禁言"
	case "unwarn":
		n, e := g.st.AddWarn(chatID, uid, -1)
		err, text = e, fmt.Sprintf("已撤销，当前 %d 次", n)
	case "rdel":
		g.tg.Delete(ctx, chatID, int(uid))
		text = "已删除"
	case "rban":
		g.note(chatID, uid, punNote{Source: "report", Reason: "举报处理", Operator: displayName(&cq.From)})
		err, text = g.tg.Ban(ctx, chatID, uid, 0, true), "已封禁"
	case "rign":
		text = "已忽略"
	case "pass":
		g.q.Do(memberKey(chatID, uid), func() { g.pass(ctx, chatID, uid, "管理员放行") })
		text = "已放行"
	case "kick":
		if p, _ := g.st.TakePending(chatID, uid); p != nil && p.PromptMsg != 0 {
			g.tg.Delete(ctx, chatID, p.PromptMsg)
		}
		err, text = g.tg.Kick(ctx, chatID, uid), "已踢出"
	default:
		answer("")
		return
	}
	if err != nil {
		answer("失败：" + err.Error())
		return
	}
	answer(text)
	if m := cq.Message.Message; m != nil && (parts[0] == "rdel" || parts[0] == "rban" || parts[0] == "rign") {
		g.tg.Delete(ctx, chatID, m.ID)
		return
	}
	if m := cq.Message.Message; m != nil && parts[0] != "pass" && parts[0] != "kick" {
		g.tg.EditMessageReplyMarkup(ctx, &tgbot.EditMessageReplyMarkupParams{ChatID: chatID, MessageID: m.ID})
		g.logf(ctx, "%s：%s，操作人 %s", text, mentionID(uid, ""), mention(&cq.From))
	}
}
