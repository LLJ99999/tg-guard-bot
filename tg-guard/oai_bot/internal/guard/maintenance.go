package guard

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"oai_bot/internal/model"

	tgbot "github.com/go-telegram/bot"

	"github.com/go-telegram/bot/models"
)

// ======================= 清理已注销账号 =======================

type zombieScan struct {
	mu      sync.Mutex
	running map[int64]bool
	last    map[int64]string
}

var zombies = &zombieScan{running: map[int64]bool{}, last: map[int64]string{}}

// ZombieStatus 面板查看清理状态
func (g *Guard) ZombieStatus(groupID int64) (bool, string) {
	zombies.mu.Lock()
	defer zombies.mu.Unlock()
	return zombies.running[groupID], zombies.last[groupID]
}

// StartZombieScan 后台逐个检查机器人见过的成员，踢出已注销账号（Deleted Account）
func (g *Guard) StartZombieScan(groupID int64, notify bool) bool {
	zombies.mu.Lock()
	if zombies.running[groupID] {
		zombies.mu.Unlock()
		return false
	}
	zombies.running[groupID] = true
	zombies.mu.Unlock()
	go func() {
		ctx := context.Background()
		ids := g.st.AllActiveMemberIDs(groupID)
		kicked, left := 0, 0
		for _, id := range ids {
			cm, err := g.tg.GetChatMember(ctx, &tgbot.GetChatMemberParams{ChatID: groupID, UserID: id})
			time.Sleep(120 * time.Millisecond)
			if err != nil || cm == nil {
				continue
			}
			if !isIn(*cm) {
				g.st.UpdateMember(groupID, id, map[string]any{"has_left": true})
				left++
				continue
			}
			u := memberUser(*cm)
			if u != nil && u.Username == "" && (u.FirstName == "" || u.FirstName == "Deleted Account") && cm.Type != models.ChatMemberTypeOwner && cm.Type != models.ChatMemberTypeAdministrator {
				g.note(groupID, id, punNote{Source: "other", Reason: "已注销账号", Name: "Deleted Account"})
				if g.tg.Kick(ctx, groupID, id) == nil {
					g.st.UpdateMember(groupID, id, map[string]any{"has_left": true})
					kicked++
				}
			}
		}
		res := fmt.Sprintf("%s 检查 %d 人，踢出已注销账号 %d 个，发现已退群 %d 人", time.Now().In(g.cfg.Location()).Format("01-02 15:04"), len(ids), kicked, left)
		zombies.mu.Lock()
		zombies.running[groupID] = false
		zombies.last[groupID] = res
		zombies.mu.Unlock()
		g.logf(ctx, "🧹 清理已注销账号（群 %d）：%s", groupID, res)
		if notify {
			g.tg.Notify(ctx, groupID, "🧹 "+res, nil, 5*time.Minute)
		}
	}()
	return true
}

// ======================= /info =======================

func (g *Guard) UserInfo(groupID, uid int64) string {
	var b strings.Builder
	u, _ := g.st.UserByID(uid)
	name := ""
	if u != nil {
		name = strings.TrimSpace(u.FirstName + " " + u.LastName)
	}
	fmt.Fprintf(&b, "<b>%s</b>\nID：<code>%d</code>\n", mentionID(uid, name), uid)
	if u != nil && u.Username != "" {
		fmt.Fprintf(&b, "用户名：@%s\n", esc(u.Username))
	}
	if mem, _ := g.st.Member(groupID, uid); mem != nil {
		fmt.Fprintf(&b, "入群：%s　发言 %d 条　警告 %d\n", mem.JoinedAt.In(g.cfg.Location()).Format("2006-01-02 15:04"), mem.MsgCount, mem.Warns)
		if mem.Left {
			b.WriteString("状态：已退群\n")
		}
	}
	p := g.st.PointsOf(groupID, uid)
	if p.Points > 0 || p.Checkins > 0 {
		fmt.Fprintf(&b, "积分 %d　签到 %d 次\n", p.Points, p.Checkins)
	}
	if n := g.st.InviteCount(groupID, uid); n > 0 {
		fmt.Fprintf(&b, "邀请 %d 人\n", n)
	}
	if inv := g.st.InviteOf(groupID, uid); inv != nil {
		fmt.Fprintf(&b, "由 %s 邀请进群\n", mentionID(inv.InviterID, ""))
	}
	var pn int64
	g.st.DB().Model(&model.Punishment{}).Where("group_id = ? AND user_id = ?", groupID, uid).Count(&pn)
	if pn > 0 {
		fmt.Fprintf(&b, "处罚记录 %d 条\n", pn)
	}
	if g.st.GBanned(uid) {
		b.WriteString("⚠️ 全局封禁\n")
	}
	if g.st.Approved(groupID, uid) {
		b.WriteString("免检白名单\n")
	}
	return b.String()
}

// ======================= 数据库备份 / 换邀请链接 =======================

func (g *Guard) backupDir() string { return filepath.Join(filepath.Dir(g.cfg.DB.Path), "backup") }

// tickBackup 每天 04:00 备份数据库，保留 7 份
func (g *Guard) tickBackup(now time.Time) {
	if !dueHM(now, "04:00") {
		return
	}
	dir := g.backupDir()
	name := filepath.Join(dir, "oai_bot-"+now.Format("20060102")+".db")
	if _, err := os.Stat(name); err == nil {
		return
	}
	os.MkdirAll(dir, 0o755)
	if err := g.st.Backup(name); err != nil {
		slog.Warn("backup", "err", err)
		return
	}
	files, _ := filepath.Glob(filepath.Join(dir, "oai_bot-*.db"))
	sort.Strings(files)
	for len(files) > 7 {
		os.Remove(files[0])
		files = files[1:]
	}
}

// tickLinkRotate 定期更换群主邀请链接（旧链接失效，防止被搬运到广告群）
func (g *Guard) tickLinkRotate(ctx context.Context) {
	gs, _ := g.st.Groups()
	for _, grp := range gs {
		if grp.BotLeft || grp.LinkRotate <= 0 {
			continue
		}
		if grp.LinkRotateAt != nil && time.Since(*grp.LinkRotateAt) < time.Duration(grp.LinkRotate)*time.Hour {
			continue
		}
		g.RotateLink(ctx, grp.ID)
	}
}

// RotateLink 立即更换主邀请链接
func (g *Guard) RotateLink(ctx context.Context, groupID int64) (string, error) {
	now := time.Now()
	g.st.UpdateGroup(groupID, map[string]any{"link_rotate_at": now})
	link, err := g.tg.ExportChatInviteLink(ctx, &tgbot.ExportChatInviteLinkParams{ChatID: groupID})
	if err != nil {
		g.logf(ctx, "更换邀请链接失败（群 %d）：%s", groupID, esc(err.Error()))
		return "", err
	}
	g.logf(ctx, "🔄 群 %d 的邀请链接已更换：%s", groupID, esc(link))
	return link, nil
}
