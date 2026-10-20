package guard

import (
	"context"
	"fmt"
	"sync"
	"time"

	tgbot "github.com/go-telegram/bot"

	"oai_bot/internal/model"
)

const (
	raidWindow   = time.Minute
	raidDuration = 15 * time.Minute
)

// raidTracker 统计每群最近 1 分钟进群 / 申请人数
type raidTracker struct {
	mu sync.Mutex
	m  map[int64][]time.Time
}

func newRaidTracker() *raidTracker { return &raidTracker{m: map[int64][]time.Time{}} }

func (r *raidTracker) hit(chatID int64) int {
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	ts := r.m[chatID]
	keep := ts[:0]
	for _, t := range ts {
		if now.Sub(t) <= raidWindow {
			keep = append(keep, t)
		}
	}
	keep = append(keep, now)
	r.m[chatID] = keep
	return len(keep)
}

func raidActive(grp *model.Group) bool {
	return grp.RaidUntil != nil && time.Now().Before(*grp.RaidUntil)
}

// checkRaid 记录一次进群；返回 true 表示处于防爆群状态，应拒绝此人
func (g *Guard) checkRaid(ctx context.Context, grp *model.Group) bool {
	if raidActive(grp) {
		return true
	}
	if grp.RaidLimit <= 0 {
		return false
	}
	if n := g.raid.hit(grp.ID); n > grp.RaidLimit {
		until := time.Now().Add(raidDuration)
		grp.RaidUntil = &until
		g.st.UpdateGroup(grp.ID, map[string]any{"raid_until": until})
		g.tg.Notify(ctx, grp.ID, fmt.Sprintf("检测到 1 分钟内 %d 人进群，已开启防爆群模式 15 分钟：期间新的入群一律拒绝。管理员可发送 /antiraid stop 提前解除。", n), nil, 0)
		g.logf(ctx, "防爆群触发：%s，1 分钟 %d 人", esc(grp.Title), n)
		return true
	}
	return false
}

// rejectRaider 防爆群期间拒绝：入群申请直接拒绝，已进群的踢出
func (g *Guard) rejectRaider(ctx context.Context, chatID, userID int64, viaRequest bool) {
	if viaRequest {
		g.tg.DeclineChatJoinRequest(ctx, &tgbot.DeclineChatJoinRequestParams{ChatID: chatID, UserID: userID})
		return
	}
	g.tg.Kick(ctx, chatID, userID)
}
