package guard

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/go-telegram/bot/models"

	"oai_bot/internal/model"
)

// 入群兜底 + 主页预取（v1.5.5）
// 1) 部分群收不到 chat_member 更新，用「xx 加入了群」服务消息兜底登记新人；
// 2) 成员第一次被看到（发言 / 进群）就后台抓一次主页，并即时检查是否和群里其他人共用同一链接。

type bioPrefetch struct {
	mu   sync.Mutex
	seen map[string]time.Time
	ch   chan [2]int64
}

var prefetch = &bioPrefetch{seen: map[string]time.Time{}, ch: make(chan [2]int64, 500)}

// markJoinedFallback 服务消息里的新人：若 chat_member 没处理过（60 秒内没登记），按新人登记
func (g *Guard) markJoinedFallback(ctx context.Context, m *models.Message) {
	for i := range m.NewChatMembers {
		u := m.NewChatMembers[i]
		if u.IsBot {
			continue
		}
		g.st.SaveUser(u.ID, u.Username, u.FirstName, u.LastName)
		mem, err := g.st.Member(m.Chat.ID, u.ID)
		if err != nil {
			continue
		}
		if mem.Newcomer && time.Since(mem.JoinedAt) < time.Minute {
			continue // chat_member 已经处理
		}
		g.st.UpdateMember(m.Chat.ID, u.ID, map[string]any{"joined_at": time.Now(), "msg_count": 0, "newcomer": true, "has_left": false})
		g.count(m.Chat.ID, "joins")
		slog.Info("join via service message", "chat", m.Chat.ID, "user", u.ID)
		g.queueBio(m.Chat.ID, u.ID)
	}
}

// queueBio 每个群每人 12 小时最多排队一次
func (g *Guard) queueBio(chatID, uid int64) {
	k := fmt.Sprintf("%d:%d", chatID, uid)
	prefetch.mu.Lock()
	if t, ok := prefetch.seen[k]; ok && time.Since(t) < bioFresh {
		prefetch.mu.Unlock()
		return
	}
	prefetch.seen[k] = time.Now()
	if len(prefetch.seen) > 20000 {
		prefetch.seen = map[string]time.Time{}
	}
	prefetch.mu.Unlock()
	select {
	case prefetch.ch <- [2]int64{chatID, uid}:
	default:
	}
}

// RunBioPrefetch 后台单线程抓主页，避免触发限流
func (g *Guard) RunBioPrefetch(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case it := <-prefetch.ch:
			g.bioCheckOne(ctx, it[0], it[1])
			time.Sleep(time.Second)
		}
	}
}

func (g *Guard) bioCheckOne(ctx context.Context, chatID, uid int64) {
	b, _ := g.fetchBio(ctx, uid)
	if b == nil || b.Err != "" || strings.TrimSpace(b.Links) == "" {
		return
	}
	grp, err := g.st.Group(chatID, "")
	if err != nil || !grp.BioScan {
		return
	}
	minN := grp.BioScanMin
	if minN < 2 {
		minN = 2
	}
	ms, err := g.st.ActiveMembers(chatID)
	if err != nil {
		return
	}
	for _, link := range strings.Fields(b.Links) {
		var users []int64
		for _, m := range ms {
			if m.UserID == uid {
				continue
			}
			ob, _ := g.st.UserBio(m.UserID)
			if ob == nil || ob.Err != "" {
				continue
			}
			for _, l := range strings.Fields(ob.Links) {
				if l == link {
					users = append(users, m.UserID)
					break
				}
			}
		}
		if len(users)+1 < minN || g.admins.isAdmin(ctx, chatID, uid) || g.st.Approved(chatID, uid) {
			continue
		}
		var sb strings.Builder
		for _, id := range users {
			sb.WriteString(" ")
			sb.WriteString(mentionID(id, fmt.Sprint(id)))
		}
		g.logf(ctx, "🔍 主页同链接提醒\n群：%s\n%s 的主页链接 %s\n与 %d 位成员相同：%s\n（只提醒不处罚，可用 /bioscan 查看全部）",
			esc(grp.Title), mentionID(uid, fmt.Sprint(uid)), esc(link), len(users), sb.String())
	}
}

var _ = model.ActionBan
