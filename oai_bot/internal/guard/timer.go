package guard

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"oai_bot/internal/telegram"
)

// timerLoop 每 30 秒检查夜间模式和定时消息
func (g *Guard) timerLoop(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			now := time.Now().In(g.cfg.Location())
			g.tickNight(ctx, now)
			g.tickSchedules(ctx, now)
			g.tickBioScan(now)
			g.tickAds(ctx, now)
		}
	}
}

// inWindow 当前时间是否在 [start, end) 内，支持跨零点（如 23:00~07:00）
func inWindow(now time.Time, start, end string) bool {
	s, err1 := time.Parse("15:04", start)
	e, err2 := time.Parse("15:04", end)
	if err1 != nil || err2 != nil || start == end {
		return false
	}
	cur := now.Hour()*60 + now.Minute()
	sm, em := s.Hour()*60+s.Minute(), e.Hour()*60+e.Minute()
	if sm < em {
		return cur >= sm && cur < em
	}
	return cur >= sm || cur < em
}

func (g *Guard) tickNight(ctx context.Context, now time.Time) {
	gs, err := g.st.Groups()
	if err != nil {
		return
	}
	for _, grp := range gs {
		want := grp.NightOn && inWindow(now, grp.NightStart, grp.NightEnd)
		if want == grp.NightActive {
			continue
		}
		id := grp.ID
		if want {
			// 记下当前群默认权限，结束时原样恢复
			saved := ""
			if info, err := g.tg.GetChat(ctx, &tgbot.GetChatParams{ChatID: id}); err == nil && info.Permissions != nil {
				b, _ := json.Marshal(info.Permissions)
				saved = string(b)
			}
			if _, err := g.tg.SetChatPermissions(ctx, &tgbot.SetChatPermissionsParams{ChatID: id, Permissions: models.ChatPermissions{}}); err != nil {
				slog.Warn("night on", "chat", id, "err", err)
				continue
			}
			g.st.UpdateGroup(id, map[string]any{"night_active": true, "night_perms": saved})
			g.tg.Notify(ctx, id, fmt.Sprintf("夜间模式开启，全员禁言至 %s。", grp.NightEnd), nil, 0)
		} else {
			perms := *telegram.AllPerms
			if grp.NightPerms != "" {
				json.Unmarshal([]byte(grp.NightPerms), &perms)
			}
			if _, err := g.tg.SetChatPermissions(ctx, &tgbot.SetChatPermissionsParams{ChatID: id, Permissions: perms}); err != nil {
				slog.Warn("night off", "chat", id, "err", err)
				continue
			}
			g.st.UpdateGroup(id, map[string]any{"night_active": false})
			g.tg.Notify(ctx, id, "夜间模式结束，恢复发言。", nil, 10*time.Minute)
		}
	}
}

func (g *Guard) tickSchedules(ctx context.Context, now time.Time) {
	ss, err := g.st.Schedules(0)
	if err != nil {
		return
	}
	hm, day := now.Format("15:04"), now.Format("2006-01-02")
	for _, sc := range ss {
		if sc.At != hm || sc.LastRun == day {
			continue
		}
		g.st.MarkScheduleRun(sc.ID, day) // 先标记，避免发送慢时下一轮重复发
		g.sendContent(ctx, sc.GroupID, sc.Content, 0)
	}
}

// today 统计用的日期
func (g *Guard) today() string { return time.Now().In(g.cfg.Location()).Format("2006-01-02") }

func (g *Guard) count(chatID int64, field string) { g.st.Count(chatID, g.today(), field) }
