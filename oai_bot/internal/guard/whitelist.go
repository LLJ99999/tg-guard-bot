package guard

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"oai_bot/internal/model"
)

// ---------- 管理员白名单 ----------

type wlEntry struct {
	ids map[int64]bool
	exp time.Time
}

type whitelist struct {
	mu sync.Mutex
	m  map[int64]wlEntry
}

func (g *Guard) inWhitelist(chatID, userID int64) bool {
	g.wl.mu.Lock()
	e, ok := g.wl.m[chatID]
	g.wl.mu.Unlock()
	if !ok || time.Now().After(e.exp) {
		e = wlEntry{ids: map[int64]bool{}, exp: time.Now().Add(time.Minute)}
		if grp, err := g.st.Group(chatID, ""); err == nil && grp != nil {
			for _, id := range ParseIDList(grp.BotAdmins) {
				e.ids[id] = true
			}
		}
		g.wl.mu.Lock()
		if g.wl.m == nil {
			g.wl.m = map[int64]wlEntry{}
		}
		g.wl.m[chatID] = e
		g.wl.mu.Unlock()
	}
	return e.ids[userID]
}

// InvalidateWhitelist 面板改了白名单后立即生效
func (g *Guard) InvalidateWhitelist(chatID int64) {
	g.wl.mu.Lock()
	delete(g.wl.m, chatID)
	g.wl.mu.Unlock()
}

// ParseIDList 解析空格 / 逗号 / 换行分隔的数字 ID
func ParseIDList(s string) []int64 {
	var out []int64
	seen := map[int64]bool{}
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '，' || r == ' ' || r == '\n' || r == '\t' || r == ';' }) {
		if id, err := strconv.ParseInt(f, 10, 64); err == nil && id > 0 && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// NormalizeWhitelist 校验面板提交的白名单：支持数字 ID 和机器人见过的 @用户名
func (g *Guard) NormalizeWhitelist(s string) (string, error) {
	var ids []string
	seen := map[string]bool{}
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '，' || r == ' ' || r == '\n' || r == '\t' || r == ';' }) {
		if strings.HasPrefix(f, "@") {
			u, _ := g.st.UserByUsername(strings.TrimPrefix(f, "@"))
			if u == nil {
				return "", fmt.Errorf("不认识 %s：让他先在群里说句话，或直接填数字 ID", f)
			}
			f = strconv.FormatInt(u.ID, 10)
		}
		if id, err := strconv.ParseInt(f, 10, 64); err != nil || id <= 0 {
			return "", fmt.Errorf("%s 不是有效的用户 ID", f)
		}
		if !seen[f] {
			seen[f] = true
			ids = append(ids, f)
		}
	}
	if len(ids) > 50 {
		return "", fmt.Errorf("白名单最多 50 人")
	}
	return strings.Join(ids, " "), nil
}

// ---------- 广告预览 ----------

// previewTargets 广告预览发给谁：机器人主人 + 本群管理员白名单
func (g *Guard) previewTargets(grp *model.Group) []int64 {
	seen := map[int64]bool{}
	var out []int64
	add := func(id int64) {
		if id > 0 && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, id := range g.cfg.Bot.OwnerIDs {
		add(id)
	}
	if grp != nil {
		for _, id := range ParseIDList(grp.BotAdmins) {
			add(id)
		}
	}
	return out
}

// PreviewAd 把广告私聊发给管理员看效果，返回成功 / 失败的人数说明
func (g *Guard) PreviewAd(ctx context.Context, a *model.Ad, to []int64) (int, []string) {
	if len(to) == 0 {
		grp, _ := g.st.Group(a.GroupID, "")
		to = g.previewTargets(grp)
	}
	title := a.GroupID
	ok := 0
	var fails []string
	for _, uid := range to {
		head := fmt.Sprintf("👀 <b>广告预览</b>「%s」\n群：<code>%d</code>　时间：%s\n下面是群友会看到的样子：", esc(a.Name), title, adWhenText(a))
		if _, err := g.tg.SendMessage(ctx, &tgbot.SendMessageParams{ChatID: uid, Text: head, ParseMode: models.ParseModeHTML}); err != nil {
			fails = append(fails, fmt.Sprintf("%d（没私聊过机器人或已屏蔽）", uid))
			continue
		}
		if _, err := g.sendAdTo(ctx, a, uid); err != nil {
			fails = append(fails, fmt.Sprintf("%d（%s）", uid, err.Error()))
			continue
		}
		ok++
	}
	return ok, fails
}
