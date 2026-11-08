package guard

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"oai_bot/internal/model"
)

// 主页链接查重：定时读取机器人见过的成员的个人简介 / 个人频道，
// 同一个链接被多人挂出（广告号集群常见特征）时报告给管理员。只报告，不自动处罚。

const (
	bioFresh    = 12 * time.Hour         // 主页缓存有效期，期内不重复拉取
	bioInterval = 150 * time.Millisecond // getChat 间隔，约 6~7 次/秒，远低于限流
	bioMaxShow  = 8                      // 报告里最多列几组
)

var (
	bioURLRe     = regexp.MustCompile(`(?i)(?:https?://|www\.)[^\s<>"'，。、）)]+|(?:t|telegram)\.(?:me|dog)/[^\s<>"'，。、）)]+|tg://resolve\?domain=[A-Za-z0-9_]+`)
	bioMentionRe = regexp.MustCompile(`(?:^|[^A-Za-z0-9_.@])@([A-Za-z][A-Za-z0-9_]{3,31})`)
	tgHostRe     = regexp.MustCompile(`(?i)^(?:https?://)?(?:www\.)?(?:t|telegram)\.(?:me|dog)/`)
)

// normLink 把链接归一化，让「https://t.me/Abc/」「t.me/abc」「@abc」算同一个
func normLink(raw string) string {
	s := strings.TrimRight(raw, ".,;:!?")
	if strings.HasPrefix(strings.ToLower(s), "tg://resolve?domain=") {
		return "t.me/" + strings.ToLower(s[len("tg://resolve?domain="):])
	}
	if loc := tgHostRe.FindStringIndex(s); loc != nil {
		path := s[loc[1]:]
		path, _, _ = strings.Cut(path, "?")
		path = strings.TrimRight(path, "/")
		if path == "" {
			return ""
		}
		// 邀请链接（+xxx / joinchat/xxx）区分大小写，用户名不区分
		if strings.HasPrefix(path, "+") || strings.HasPrefix(strings.ToLower(path), "joinchat/") {
			return "t.me/" + path
		}
		name, _, _ := strings.Cut(path, "/") // t.me/channel/123 → t.me/channel
		return "t.me/" + strings.ToLower(name)
	}
	low := strings.ToLower(s)
	low = strings.TrimPrefix(strings.TrimPrefix(low, "https://"), "http://")
	low = strings.TrimPrefix(low, "www.")
	low, _, _ = strings.Cut(low, "#")
	low = strings.TrimRight(low, "/")
	return low
}

// bioLinks 从简介文字里提取归一化链接（去重）
func bioLinks(bio, channel string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(l string) {
		if l != "" && !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}
	for _, m := range bioURLRe.FindAllString(bio, -1) {
		add(normLink(m))
	}
	for _, m := range bioMentionRe.FindAllStringSubmatch(bio, -1) {
		add("t.me/" + strings.ToLower(m[1]))
	}
	if channel != "" {
		add("t.me/" + strings.ToLower(channel))
	}
	return out
}

type bioScanner struct {
	mu      sync.Mutex
	running bool
}

func (b *bioScanner) start() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.running {
		return false
	}
	b.running = true
	return true
}

func (b *bioScanner) done() { b.mu.Lock(); b.running = false; b.mu.Unlock() }

// StartBioScan 后台开始一次查重。notifyTo 为未配置日志频道时的结果接收者（0 表示发到群里）
func (g *Guard) StartBioScan(groupID, notifyTo int64) bool {
	if !g.bioScan.start() {
		return false
	}
	go func() {
		defer g.bioScan.done()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
		defer cancel()
		grp, err := g.st.Group(groupID, "")
		if err != nil {
			return
		}
		r, err := g.runBioScan(ctx, grp)
		if err != nil {
			slog.Error("bio scan", "group", groupID, "err", err)
			return
		}
		g.sendBioReport(ctx, grp, r, notifyTo)
	}()
	return true
}

// tickBioScan 定时器：到点的群开始扫描（同一时间只扫一个群）
func (g *Guard) tickBioScan(now time.Time) {
	gs, err := g.st.Groups()
	if err != nil {
		return
	}
	for _, grp := range gs {
		if !grp.BioScan || grp.BioScanHours <= 0 {
			continue
		}
		if grp.BioScanAt != nil && now.Sub(*grp.BioScanAt) < time.Duration(grp.BioScanHours)*time.Hour {
			continue
		}
		if g.StartBioScan(grp.ID, 0) {
			return
		}
	}
}

// fetchBio 拉取单个用户主页，带缓存
func (g *Guard) fetchBio(ctx context.Context, uid int64) (*model.UserBio, bool) {
	if b, _ := g.st.UserBio(uid); b != nil && time.Since(b.CheckedAt) < bioFresh {
		return b, false
	}
	b := &model.UserBio{UserID: uid, CheckedAt: time.Now()}
	var info *models.ChatFullInfo
	var err error
	for try := 0; try < 3; try++ {
		info, err = g.tg.GetChat(ctx, &tgbot.GetChatParams{ChatID: uid})
		if err == nil || !strings.Contains(err.Error(), "Too Many Requests") {
			break
		}
		select { // 被限流就多等一会
		case <-ctx.Done():
			return b, true
		case <-time.After(time.Duration(5*(try+1)) * time.Second):
		}
	}
	if err != nil {
		b.Err = err.Error()
	} else {
		b.Bio = info.Bio
		if info.PersonalChat != nil {
			b.Channel = info.PersonalChat.Username
		}
		if info.BusinessIntro != nil {
			b.Bio += "\n" + info.BusinessIntro.Title + " " + info.BusinessIntro.Message
		}
		b.Links = strings.Join(bioLinks(b.Bio, b.Channel), " ")
	}
	g.st.SaveUserBio(b)
	return b, true
}

func (g *Guard) runBioScan(ctx context.Context, grp *model.Group) (*model.BioReport, error) {
	now := time.Now()
	g.st.UpdateGroup(grp.ID, map[string]any{"bio_scan_at": now})
	ms, err := g.st.ActiveMembers(grp.ID)
	if err != nil {
		return nil, err
	}
	r := &model.BioReport{GroupID: grp.ID}
	byLink := map[string][]int64{}
	for _, m := range ms {
		if ctx.Err() != nil {
			break
		}
		b, fetched := g.fetchBio(ctx, m.UserID)
		if fetched {
			time.Sleep(bioInterval)
		}
		r.Scanned++
		if b.Err != "" {
			r.Failed++
			continue
		}
		for _, l := range strings.Fields(b.Links) {
			byLink[l] = append(byLink[l], m.UserID)
		}
	}
	minN := grp.BioScanMin
	if minN < 2 {
		minN = 2
	}
	var cs []model.BioCluster
	for link, uids := range byLink {
		if len(uids) < minN {
			continue
		}
		c := model.BioCluster{Link: link}
		for _, uid := range uids {
			// 管理员、白名单不算；顺便确认还在群里
			if g.admins.isAdmin(ctx, grp.ID, uid) || g.st.Approved(grp.ID, uid) {
				continue
			}
			if cm, err := g.tg.GetChatMember(ctx, &tgbot.GetChatMemberParams{ChatID: grp.ID, UserID: uid}); err == nil && !isIn(*cm) {
				g.st.UpdateMember(grp.ID, uid, map[string]any{"has_left": true})
				continue
			}
			ref := model.BioUserRef{ID: uid}
			if u, _ := g.st.UserByID(uid); u != nil {
				ref.Name = strings.TrimSpace(u.FirstName + " " + u.LastName)
				ref.Username = u.Username
			}
			c.Users = append(c.Users, ref)
		}
		if len(c.Users) >= minN {
			cs = append(cs, c)
		}
	}
	sort.Slice(cs, func(i, j int) bool {
		if len(cs[i].Users) != len(cs[j].Users) {
			return len(cs[i].Users) > len(cs[j].Users)
		}
		return cs[i].Link < cs[j].Link
	})
	raw, _ := json.Marshal(cs)
	r.Clusters = string(raw)
	if err := g.st.SaveBioReport(r); err != nil {
		return nil, err
	}
	return r, nil
}

func (g *Guard) sendBioReport(ctx context.Context, grp *model.Group, r *model.BioReport, notifyTo int64) {
	var cs []model.BioCluster
	json.Unmarshal([]byte(r.Clusters), &cs)
	var b strings.Builder
	fmt.Fprintf(&b, "🔍 <b>主页链接查重</b>\n群：%s\n扫描 %d 人", esc(grp.Title), r.Scanned)
	if r.Failed > 0 {
		fmt.Fprintf(&b, "（%d 人读取失败）", r.Failed)
	}
	if len(cs) == 0 {
		fmt.Fprintf(&b, "\n未发现 %d 人以上共用同一链接。", max(grp.BioScanMin, 2))
		g.st.AddLog(stripHTML(b.String()))
		if g.cfg.Bot.LogChatID != 0 || notifyTo != 0 {
			g.tg.Notify(ctx, g.reportTarget(grp.ID, notifyTo), b.String(), nil, 0)
		}
		return
	}
	fmt.Fprintf(&b, "，发现 %d 组共用链接：\n", len(cs))
	var rows [][]models.InlineKeyboardButton
	for i, c := range cs {
		if i >= bioMaxShow {
			fmt.Fprintf(&b, "\n……还有 %d 组，见 AstrBot 面板", len(cs)-bioMaxShow)
			break
		}
		fmt.Fprintf(&b, "\n<b>%d. %s</b>（%d 人）\n", i+1, esc(c.Link), len(c.Users))
		for j, u := range c.Users {
			if j >= 15 {
				fmt.Fprintf(&b, "  …等 %d 人\n", len(c.Users))
				break
			}
			fmt.Fprintf(&b, "  %s <code>%d</code>\n", mentionID(u.ID, u.Name), u.ID)
		}
		rows = append(rows, []models.InlineKeyboardButton{
			{Text: fmt.Sprintf("🚫 封禁第 %d 组", i+1), CallbackData: fmt.Sprintf("bsb:%d:%d", r.ID, i)},
		})
	}
	text := b.String()
	if len([]rune(text)) > 4000 {
		text = string([]rune(text)[:3990]) + "…"
	}
	g.st.AddLog(stripHTML(text))
	g.tg.Notify(ctx, g.reportTarget(grp.ID, notifyTo), text, &models.InlineKeyboardMarkup{InlineKeyboard: rows}, 0)
}

// reportTarget 报告发到哪：日志频道 > 指定的人 > 群本身
func (g *Guard) reportTarget(groupID, notifyTo int64) int64 {
	if g.cfg.Bot.LogChatID != 0 {
		return g.cfg.Bot.LogChatID
	}
	if notifyTo != 0 {
		return notifyTo
	}
	return groupID
}

// BanBioCluster 封禁报告里的某一组（按钮和面板共用）
func (g *Guard) BanBioCluster(ctx context.Context, groupID int64, reportID uint, idx int, operator int64) (int, error) {
	r, err := g.st.BioReport(reportID)
	if err != nil || r == nil || (groupID != 0 && r.GroupID != groupID) {
		return 0, fmt.Errorf("报告不存在")
	}
	if operator != 0 && !g.cfg.IsOwner(operator) && !g.admins.canRestrict(ctx, r.GroupID, operator) {
		return 0, fmt.Errorf("仅限该群有封禁权限的管理员")
	}
	var cs []model.BioCluster
	json.Unmarshal([]byte(r.Clusters), &cs)
	if idx < 0 || idx >= len(cs) {
		return 0, fmt.Errorf("分组不存在")
	}
	n := 0
	for _, u := range cs[idx].Users {
		if g.admins.isAdmin(ctx, r.GroupID, u.ID) || g.st.Approved(r.GroupID, u.ID) {
			continue
		}
		if err := g.tg.Ban(ctx, r.GroupID, u.ID, 0, true); err == nil {
			n++
		}
	}
	g.logf(ctx, "🚫 按主页链接查重封禁 %d 人\n群：%d\n链接：%s", n, r.GroupID, esc(cs[idx].Link))
	return n, nil
}

// LatestBioReport 面板用
func (g *Guard) LatestBioReport(groupID int64) (*model.BioReport, bool) {
	r, _ := g.st.LatestBioReport(groupID)
	g.bioScan.mu.Lock()
	running := g.bioScan.running
	g.bioScan.mu.Unlock()
	return r, running
}

func bioScanName(grp *model.Group) string {
	if !grp.BioScan {
		return "关"
	}
	return fmt.Sprintf("每 %d 小时", grp.BioScanHours)
}
