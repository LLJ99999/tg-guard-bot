package guard

import (
	"context"
	"sync"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"oai_bot/internal/telegram"
)

const adminTTL = 10 * time.Minute

// groupAnonymousBot 匿名管理员发言时 From 是这个账号，SenderChat 是群本身
const groupAnonymousBot = 1087968824

// adminCache 缓存各群管理员列表。管理员变动时（chat_member 更新）主动失效
type adminCache struct {
	tg *telegram.Client
	mu sync.Mutex
	m  map[int64]adminEntry
	// extra 管理员白名单（面板配置），视同管理员：可用管理命令、免检
	extra func(chatID, userID int64) bool
}

func (a *adminCache) isExtra(chatID, userID int64) bool {
	return a.extra != nil && userID != 0 && a.extra(chatID, userID)
}

type adminEntry struct {
	ids    map[int64]models.ChatMember
	linked int64 // 群关联的频道 ID
	exp    time.Time
}

func newAdminCache(tg *telegram.Client) *adminCache {
	return &adminCache{tg: tg, m: map[int64]adminEntry{}}
}

func (a *adminCache) invalidate(chatID int64) {
	a.mu.Lock()
	delete(a.m, chatID)
	a.mu.Unlock()
}

func (a *adminCache) get(ctx context.Context, chatID int64) map[int64]models.ChatMember {
	a.mu.Lock()
	e, ok := a.m[chatID]
	a.mu.Unlock()
	if ok && time.Now().Before(e.exp) {
		return e.ids
	}
	list, err := telegram.Retry(ctx, func() ([]models.ChatMember, error) {
		return a.tg.GetChatAdministrators(ctx, &tgbot.GetChatAdministratorsParams{ChatID: chatID})
	})
	if err != nil {
		if ok {
			return e.ids // 拉取失败时沿用旧数据
		}
		return nil
	}
	ids := make(map[int64]models.ChatMember, len(list))
	for _, m := range list {
		ids[memberUserID(m)] = m
	}
	var linked int64
	if info, err := a.tg.GetChat(ctx, &tgbot.GetChatParams{ChatID: chatID}); err == nil {
		linked = info.LinkedChatID
	}
	a.mu.Lock()
	a.m[chatID] = adminEntry{ids: ids, linked: linked, exp: time.Now().Add(adminTTL)}
	a.mu.Unlock()
	return ids
}

// linkedChannel 群关联的频道（频道主以频道身份在评论区发言是正常行为）
func (a *adminCache) linkedChannel(ctx context.Context, chatID int64) int64 {
	a.get(ctx, chatID)
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.m[chatID].linked
}

func (a *adminCache) isAdmin(ctx context.Context, chatID, userID int64) bool {
	if a.isExtra(chatID, userID) {
		return true
	}
	_, ok := a.get(ctx, chatID)[userID]
	return ok
}

// canRestrict 是否有封禁权限（群主总是有）
func (a *adminCache) canRestrict(ctx context.Context, chatID, userID int64) bool {
	if a.isExtra(chatID, userID) {
		return true
	}
	m, ok := a.get(ctx, chatID)[userID]
	if !ok {
		return false
	}
	return m.Type == models.ChatMemberTypeOwner || (m.Administrator != nil && m.Administrator.CanRestrictMembers)
}

func memberUserID(m models.ChatMember) int64 {
	switch {
	case m.Owner != nil && m.Owner.User != nil:
		return m.Owner.User.ID
	case m.Administrator != nil:
		return m.Administrator.User.ID
	case m.Member != nil && m.Member.User != nil:
		return m.Member.User.ID
	case m.Restricted != nil && m.Restricted.User != nil:
		return m.Restricted.User.ID
	case m.Left != nil && m.Left.User != nil:
		return m.Left.User.ID
	case m.Banned != nil && m.Banned.User != nil:
		return m.Banned.User.ID
	}
	return 0
}

func memberUser(m models.ChatMember) *models.User {
	switch {
	case m.Owner != nil:
		return m.Owner.User
	case m.Administrator != nil:
		u := m.Administrator.User
		return &u
	case m.Member != nil:
		return m.Member.User
	case m.Restricted != nil:
		return m.Restricted.User
	case m.Left != nil:
		return m.Left.User
	case m.Banned != nil:
		return m.Banned.User
	}
	return nil
}
