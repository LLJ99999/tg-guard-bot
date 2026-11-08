package guard

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"oai_bot/internal/model"
)

// ---------- 联邦封禁 ----------

// isCreator 群主（或机器人主人）
func (g *Guard) isCreator(ctx context.Context, chatID, uid int64) bool {
	if g.cfg.IsOwner(uid) {
		return true
	}
	m, ok := g.admins.get(ctx, chatID)[uid]
	return ok && m.Type == models.ChatMemberTypeOwner
}

// FedBan 在联邦所有成员群封禁 / 解封，返回影响的群数
func (g *Guard) FedBan(ctx context.Context, fedID string, uid int64, reason string, by int64, on bool) (int, error) {
	f, _ := g.st.Fed(fedID)
	if f == nil {
		return 0, errors.New("联邦不存在")
	}
	if on && (g.cfg.IsOwner(uid) || g.st.IsFedAdmin(fedID, uid)) {
		return 0, errors.New("不能封禁联邦管理员或机器人主人")
	}
	if err := g.st.SetFedBan(&model.FedBan{FedID: fedID, UserID: uid, Reason: reason, By: by}, on); err != nil {
		return 0, err
	}
	gs, err := g.st.FedGroups(fedID)
	if err != nil {
		return 0, err
	}
	succeeded, failed := 0, 0
	for _, grp := range gs {
		if !g.cfg.GroupAllowed(grp.ID) {
			continue
		}
		var err error
		if on {
			err = g.tg.Ban(ctx, grp.ID, uid, 0, true)
		} else {
			err = g.tg.Unban(ctx, grp.ID, uid)
		}
		if err != nil {
			failed++
		} else {
			succeeded++
		}
	}
	g.logf(ctx, "%s %s\n联邦：%s（成功 %d 个群，失败 %d 个群）%s", onOff(on, "联邦封禁", "解除联邦封禁"), mentionID(uid, ""), esc(f.Name), succeeded, failed, onOff(reason != "", "\n原因："+esc(reason), ""))
	if failed > 0 {
		return succeeded, fmt.Errorf("封禁名单已更新；群操作成功 %d 个，失败 %d 个，请检查权限后重试同一操作", succeeded, failed)
	}
	return succeeded, nil
}

// JoinFed 群加入联邦，并把联邦已有的封禁名单应用到本群（后台慢慢封，避免触发限流）
func (g *Guard) JoinFed(ctx context.Context, groupID int64, fedID string) error {
	if !g.cfg.GroupAllowed(groupID) {
		return errors.New("group not allowed")
	}
	if fedID != "" {
		if f, _ := g.st.Fed(fedID); f == nil {
			return errors.New("联邦不存在，请检查联邦 ID")
		}
	}
	if err := g.st.UpdateGroup(groupID, map[string]any{"fed_id": fedID}); err != nil {
		return err
	}
	if fedID == "" {
		return nil
	}
	bans, _ := g.st.FedBans(fedID)
	go func() {
		bg := context.WithoutCancel(ctx)
		for _, b := range bans {
			grp, err := g.st.Group(groupID, "")
			if err != nil || grp.FedID != fedID || !g.cfg.GroupAllowed(groupID) {
				return
			}
			if g.cfg.IsOwner(b.UserID) || g.st.IsFedAdmin(fedID, b.UserID) {
				continue
			}
			if err := g.tg.Ban(bg, groupID, b.UserID, 0, false); err != nil {
				g.logf(bg, "联邦名单同步失败，群 %d，用户 %d；请检查权限后重试", groupID, b.UserID)
			}
			time.Sleep(300 * time.Millisecond)
		}
	}()
	return nil
}

// fedCommand 联邦相关命令，返回 false 表示不是联邦命令
func (g *Guard) fedCommand(ctx context.Context, grp *model.Group, m *models.Message, name, arg string, reply func(string)) bool {
	uid := int64(0)
	if m.From != nil {
		uid = m.From.ID
	}
	needFed := func() *model.Fed {
		if grp.FedID == "" {
			reply("本群还没加入联邦。群主发 /newfed 名称 创建，或 /joinfed 联邦ID 加入")
			return nil
		}
		f, _ := g.st.Fed(grp.FedID)
		if f == nil {
			reply("本群加入的联邦已被删除")
		}
		return f
	}
	switch name {
	case "newfed":
		if !g.isCreator(ctx, m.Chat.ID, uid) {
			reply("只有群主可以创建联邦")
			return true
		}
		if arg == "" {
			reply("用法：/newfed 联邦名称")
			return true
		}
		f, err := g.st.CreateFed(truncate(arg, 64), uid)
		if err != nil {
			reply("创建失败：" + esc(err.Error()))
			return true
		}
		g.JoinFed(ctx, grp.ID, f.ID)
		reply(fmt.Sprintf("已创建联邦 <b>%s</b>，本群已加入。\n联邦 ID：<code>%s</code>\n其它群的群主发 <code>/joinfed %s</code> 即可加入。", esc(f.Name), f.ID, f.ID))
	case "joinfed":
		if !g.isCreator(ctx, m.Chat.ID, uid) {
			reply("只有群主可以让本群加入联邦")
			return true
		}
		if arg == "" {
			reply("用法：/joinfed 联邦ID")
			return true
		}
		if err := g.JoinFed(ctx, grp.ID, strings.TrimSpace(arg)); err != nil {
			reply(esc(err.Error()))
			return true
		}
		f, _ := g.st.Fed(strings.TrimSpace(arg))
		reply(fmt.Sprintf("本群已加入联邦 <b>%s</b>，联邦封禁名单（%d 人）会同步到本群", esc(f.Name), g.st.FedBanCount(f.ID)))
	case "leavefed":
		if !g.isCreator(ctx, m.Chat.ID, uid) {
			reply("只有群主可以让本群退出联邦")
			return true
		}
		g.JoinFed(ctx, grp.ID, "")
		reply("本群已退出联邦（已封禁的人不会自动解封）")
	case "fedinfo":
		f := needFed()
		if f == nil {
			return true
		}
		gs, _ := g.st.FedGroups(f.ID)
		as, _ := g.st.FedAdmins(f.ID)
		reply(fmt.Sprintf("<b>%s</b>\nID：<code>%s</code>\n主人：%s\n管理员：%d 人\n成员群：%d 个\n封禁名单：%d 人",
			esc(f.Name), f.ID, mentionID(f.OwnerID, ""), len(as), len(gs), g.st.FedBanCount(f.ID)))
	case "fedadmin", "fedunadmin":
		f := needFed()
		if f == nil {
			return true
		}
		if f.OwnerID != uid && !g.cfg.IsOwner(uid) {
			reply("只有联邦主人可以设置联邦管理员")
			return true
		}
		tid, who, _, err := g.target(m, arg)
		if err != nil {
			reply(esc(err.Error()))
			return true
		}
		on := name == "fedadmin"
		g.st.SetFedAdmin(f.ID, tid, on)
		reply(fmt.Sprintf("%s %s", onOff(on, "已设为联邦管理员：", "已取消联邦管理员："), mentionID(tid, who)))
	case "fban", "unfban":
		f := needFed()
		if f == nil {
			return true
		}
		if !g.cfg.IsOwner(uid) && !g.st.IsFedAdmin(f.ID, uid) {
			reply("只有联邦管理员可以联邦封禁")
			return true
		}
		tid, who, rest, err := g.target(m, arg)
		if err != nil {
			reply(esc(err.Error()))
			return true
		}
		on := name == "fban"
		n, err := g.FedBan(ctx, f.ID, tid, rest, uid, on)
		if err != nil {
			reply(esc(err.Error()))
			return true
		}
		if on && m.ReplyToMessage != nil {
			g.tg.Delete(ctx, m.Chat.ID, m.ReplyToMessage.ID)
		}
		reply(fmt.Sprintf("%s %s（联邦 %s，%d 个群）%s", onOff(on, "已联邦封禁", "已解除联邦封禁"), mentionID(tid, who), esc(f.Name), n, onOff(rest != "", "\n原因："+esc(rest), "")))
	default:
		return false
	}
	return true
}

// ---------- 举报处理 ----------

var reportActions = map[string]string{"del": model.ReportDeleted, "mute": model.ReportMuted, "ban": model.ReportBanned, "ign": model.ReportIgnored}

// HandleReport 处理一条举报：del 删除消息 | mute 删除并禁言 24 小时 | ban 删除并封禁 | ign 忽略
func (g *Guard) HandleReport(ctx context.Context, id uint, act, by string) (string, error) {
	status, ok := reportActions[act]
	if !ok {
		return "", errors.New("未知操作")
	}
	r, _ := g.st.Report(id)
	if r == nil {
		return "", errors.New("举报不存在")
	}
	if !g.cfg.GroupAllowed(r.GroupID) {
		return "", errors.New("group not allowed")
	}
	closed, err := g.st.BeginReport(id, by)
	if err != nil {
		return "", err
	}
	if !closed {
		return "这条举报已经被处理过了或正在处理中", nil
	}
	finished := false
	defer func() {
		if !finished {
			if e := g.st.FinishReport(id, model.ReportOpen, ""); e != nil {
				g.logf(ctx, "举报 #%d 状态恢复失败，需检查数据库", id)
			}
		}
	}()
	// 删除已不存在的消息视为幂等成功，其它错误必须保留重试入口。
	if act != "ign" {
		if e := g.deleteReportedMessage(ctx, r.GroupID, r.MsgID); e != nil {
			return "", fmt.Errorf("删除消息失败，举报保留待处理：%w", e)
		}
	}
	var text string
	if act == "mute" || act == "ban" {
		g.note(r.GroupID, r.TargetID, punNote{Source: "report", Reason: "举报处理（" + by + "）", Text: r.Text, Name: r.Target, Operator: by})
	}
	switch act {
	case "del":
		text = "已删除消息"
	case "mute":
		err, text = g.tg.Mute(ctx, r.GroupID, r.TargetID, 24*time.Hour), "已删除并禁言 24 小时"
	case "ban":
		err, text = g.tg.Ban(ctx, r.GroupID, r.TargetID, 0, true), "已删除并封禁"
	case "ign":
		text = "已忽略"
	}
	if err != nil {
		return "", fmt.Errorf("群操作失败，举报保留待处理（消息可能已删除）：%w", err)
	}
	if err := g.st.FinishReport(id, status, by); err != nil {
		return "", err
	}
	finished = true
	if act != "ign" { // 管理员确认举报属实 = 广告样本，让 AI 学习
		go g.learnSpam(context.Background(), r.GroupID, r.Text, "report", by)
	}
	if act == "mute" {
		g.count(r.GroupID, "muted")
	}
	if act == "ban" {
		g.count(r.GroupID, "banned")
	}
	if r.NotifyMsg != 0 {
		g.tg.Delete(ctx, r.GroupID, r.NotifyMsg)
	}
	g.logf(ctx, "处理举报 #%d：%s %s\n操作人：%s", r.ID, text, mentionID(r.TargetID, r.Target), esc(by))
	return text, nil
}

func (g *Guard) deleteReportedMessage(ctx context.Context, chatID int64, messageID int) error {
	_, err := g.tg.DeleteMessage(ctx, &tgbot.DeleteMessageParams{ChatID: chatID, MessageID: messageID})
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "message to delete not found") {
		return nil
	}
	return err
}
