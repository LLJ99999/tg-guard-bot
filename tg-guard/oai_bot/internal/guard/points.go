package guard

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"oai_bot/internal/model"
	"oai_bot/internal/store"
	"oai_bot/internal/telegram"

	"github.com/go-telegram/bot/models"
)

// ======================= 签到 / 积分 / 排行 =======================

// checkin 签到
func (g *Guard) checkin(ctx context.Context, grp *model.Group, m *models.Message) {
	if m.From == nil {
		return
	}
	loc := g.cfg.Location()
	today := time.Now().In(loc).Format("2006-01-02")
	yesterday := time.Now().In(loc).AddDate(0, 0, -1).Format("2006-01-02")
	gain, p, ok := g.st.Checkin(grp.ID, m.From.ID, today, yesterday, max(grp.CheckinPoints, 1), grp.StreakBonus)
	who := mention(m.From)
	if !ok {
		g.tg.Notify(ctx, grp.ID, fmt.Sprintf("%s 今天已经签到过了，当前 %d 分，连续 %d 天。", who, p.Points, p.Streak), nil, 30*time.Second)
		return
	}
	extra := ""
	if p.Streak > 1 {
		extra = fmt.Sprintf("，连续 %d 天", p.Streak)
	}
	g.tg.Notify(ctx, grp.ID, fmt.Sprintf("✅ %s 签到成功 +%d 分%s\n当前积分：<b>%d</b>", who, gain, extra, p.Points), nil, time.Minute)
}

func rankName(r store.RankRow) string {
	n := strings.TrimSpace(r.FirstName + " " + r.LastName)
	if n == "" && r.Username != "" {
		n = "@" + r.Username
	}
	if n == "" {
		n = strconv.FormatInt(r.UserID, 10)
	}
	if rr := []rune(n); len(rr) > 16 {
		n = string(rr[:16]) + "…"
	}
	return n
}

func rankText(title, unit string, rows []store.RankRow) string {
	var b strings.Builder
	b.WriteString("<b>" + title + "</b>\n")
	if len(rows) == 0 {
		b.WriteString("还没有数据")
		return b.String()
	}
	medal := []string{"🥇", "🥈", "🥉"}
	for i, r := range rows {
		p := fmt.Sprintf("%d.", i+1)
		if i < 3 {
			p = medal[i]
		}
		fmt.Fprintf(&b, "%s %s — %d %s\n", p, esc(rankName(r)), r.Value, unit)
	}
	return b.String()
}

func (g *Guard) lastDays(n int) []string {
	loc := g.cfg.Location()
	var days []string
	for i := n - 1; i >= 0; i-- {
		days = append(days, time.Now().In(loc).AddDate(0, 0, -i).Format("2006-01-02"))
	}
	return days
}

// ActivityRank 发言榜文字；week=true 近 7 天
func (g *Guard) ActivityRankText(groupID int64, week bool) string {
	days, title := g.lastDays(1), "💬 今日发言榜"
	if week {
		days, title = g.lastDays(7), "💬 近 7 天发言榜"
	}
	rows, _ := g.st.ActivityRank(groupID, days, 10)
	return rankText(title, "条", rows)
}

func (g *Guard) PointsRankText(groupID int64) string {
	rows, _, _ := g.st.PointsRank(groupID, 10, 0)
	return rankText("⭐ 积分榜", "分", rows)
}

func (g *Guard) InviteRankText(groupID int64) string {
	rows, _ := g.st.InviteRank(groupID, 10)
	return rankText("🔗 邀请榜（已通过验证）", "人", rows)
}

// myStats /me
func (g *Guard) myStats(ctx context.Context, grp *model.Group, u *models.User) {
	p := g.st.PointsOf(grp.ID, u.ID)
	mem, _ := g.st.Member(grp.ID, u.ID)
	today, _ := g.st.ActivityRank(grp.ID, g.lastDays(1), 1000)
	n := 0
	for _, r := range today {
		if r.UserID == u.ID {
			n = r.Value
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "<b>%s</b>\n", mention(u))
	if grp.PointsOn {
		fmt.Fprintf(&b, "积分：<b>%d</b>　连续签到 %d 天　累计签到 %d 次\n", p.Points, p.Streak, p.Checkins)
	}
	if mem != nil {
		fmt.Fprintf(&b, "今日发言 %d 条　总发言 %d 条　警告 %d/%d\n", n, mem.MsgCount, mem.Warns, grp.WarnLimit)
	}
	if grp.InviteOn {
		fmt.Fprintf(&b, "已邀请 %d 人（通过验证）\n", g.st.InviteCount(grp.ID, u.ID))
	}
	g.tg.Notify(ctx, grp.ID, b.String(), nil, time.Minute)
}

// tickRanks 定时发活跃榜（每天 / 每周日），顺便清理 40 天前的每日发言记录
func (g *Guard) tickRanks(ctx context.Context, now time.Time) {
	gs, _ := g.st.Groups()
	day := now.Format("2006-01-02")
	for _, grp := range gs {
		if grp.BotLeft || (grp.RankPost != "daily" && grp.RankPost != "weekly") || grp.RankLast == day || !dueHM(now, grp.RankAt) {
			continue
		}
		if grp.RankPost == "weekly" && now.Weekday() != time.Sunday {
			continue
		}
		g.st.UpdateGroup(grp.ID, map[string]any{"rank_last": day})
		text := g.ActivityRankText(grp.ID, grp.RankPost == "weekly")
		if grp.PointsOn {
			text += "\n" + g.PointsRankText(grp.ID)
		}
		if grp.InviteOn {
			text += "\n" + g.InviteRankText(grp.ID)
		}
		g.tg.Notify(telegram.WithThread(ctx, grp.ID, grp.BotThread), grp.ID, text, nil, 0)
	}
	if now.Hour() == 4 && now.Minute() < 1 {
		g.st.PruneUserDays(now.AddDate(0, 0, -40).Format("2006-01-02"))
		g.st.PruneCaptures()
	}
}
