package guard

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"oai_bot/internal/model"
	"oai_bot/internal/telegram"

	tgbot "github.com/go-telegram/bot"

	"github.com/go-telegram/bot/models"
)

// ======================= 发言统计 / 话题 =======================

var msgPointsCD = &cooldown{d: time.Minute}

// trackActivity 每条群消息都记：成员发言数（管理员也算）、每日发言榜、发言积分
func (g *Guard) trackActivity(grp *model.Group, m *models.Message) {
	if m.From == nil || m.From.IsBot || m.SenderChat != nil {
		return
	}
	uid := m.From.ID
	g.st.Member(m.Chat.ID, uid)
	g.st.IncMsgCount(m.Chat.ID, uid)
	day := g.today()
	g.st.IncUserDay(m.Chat.ID, uid, day)
	if grp.PointsOn && grp.MsgPoints > 0 && len([]rune(strings.TrimSpace(msgText(m)))) >= 2 &&
		msgPointsCD.allow(memberKey(m.Chat.ID, uid)) {
		g.st.AddMsgPoints(m.Chat.ID, uid, day, grp.MsgPoints, max(grp.MsgPointsCap, grp.MsgPoints))
	}
}

// trackTopic 话题群：从服务消息和话题内消息里记下话题名，面板里就能下拉选择
func (g *Guard) trackTopic(m *models.Message) bool {
	switch {
	case m.ForumTopicCreated != nil:
		g.st.SaveTopic(&model.ForumTopic{GroupID: m.Chat.ID, ThreadID: m.MessageThreadID, Name: m.ForumTopicCreated.Name, UpdatedAt: time.Now()})
		return true
	case m.ForumTopicEdited != nil:
		g.st.SaveTopic(&model.ForumTopic{GroupID: m.Chat.ID, ThreadID: m.MessageThreadID, Name: m.ForumTopicEdited.Name, UpdatedAt: time.Now()})
		return true
	case m.ForumTopicClosed != nil:
		g.st.SetTopicClosed(m.Chat.ID, m.MessageThreadID, true)
		return true
	case m.ForumTopicReopened != nil:
		g.st.SetTopicClosed(m.Chat.ID, m.MessageThreadID, false)
		return true
	}
	if m.IsTopicMessage && m.MessageThreadID != 0 {
		name := ""
		if r := m.ReplyToMessage; r != nil && r.ForumTopicCreated != nil {
			name = r.ForumTopicCreated.Name
		}
		if topicSeen.allow(fmt.Sprintf("%d:%d:%s", m.Chat.ID, m.MessageThreadID, name)) {
			g.st.SaveTopic(&model.ForumTopic{GroupID: m.Chat.ID, ThreadID: m.MessageThreadID, Name: name, UpdatedAt: time.Now()})
		}
	}
	return false
}

var topicSeen = &cooldown{d: time.Hour}

// msgCtx 处理一条群消息时的 ctx：机器人的提示回到同一个话题（或群设置里指定的话题）
func msgCtx(ctx context.Context, grp *model.Group, m *models.Message) context.Context {
	thread := 0
	if m.IsTopicMessage {
		thread = m.MessageThreadID
	}
	if grp != nil && grp.BotThread > 0 {
		thread = grp.BotThread
	}
	return telegram.WithThread(ctx, m.Chat.ID, thread)
}

// ======================= 新人限制 =======================

// applyNewbieLimit 新人进群 N 小时内只能发文字（不能发图片视频文件贴纸），到时间 Telegram 自动解除
func (g *Guard) applyNewbieLimit(ctx context.Context, grp *model.Group, uid int64) {
	if grp.NewbieHours <= 0 {
		return
	}
	until := time.Now().Add(time.Duration(grp.NewbieHours) * time.Hour).Unix()
	if _, err := g.tg.RestrictChatMember(ctx, &tgbot.RestrictChatMemberParams{ChatID: grp.ID, UserID: uid,
		Permissions: telegram.TextOnly, UntilDate: int(until)}); err != nil {
		slog.Debug("newbie limit", "chat", grp.ID, "user", uid, "err", err)
	}
}

// newbieByTime 进群不满 NewbieHours 小时的新人
func newbieByTime(grp *model.Group, mem *model.Member) bool {
	return grp.NewbieHours > 0 && mem.Newcomer && time.Since(mem.JoinedAt) < time.Duration(grp.NewbieHours)*time.Hour
}
