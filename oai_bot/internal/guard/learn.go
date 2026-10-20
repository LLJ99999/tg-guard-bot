package guard

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/go-telegram/bot/models"

	"oai_bot/internal/model"
)

// ---------- 处罚记录 ----------

// punNote 处罚前登记的上下文（原因、消息），由 Telegram 操作成功后的回调取走写库
type punNote struct {
	Source, Reason, Text, Name, Operator string
	at                                   time.Time
}

// undoNote 撤销操作的上下文：谁撤销的、要不要让 AI 记住这条是正常消息
type undoNote struct {
	By    string
	Learn *bool
	at    time.Time
}

type learnState struct {
	mu    sync.Mutex
	notes map[string]punNote
	undos map[string]undoNote
	// 样本缓存
	samples []model.SpamSample
	loaded  bool
}

func (g *Guard) note(chatID, userID int64, n punNote) {
	n.at = time.Now()
	g.learn.mu.Lock()
	if g.learn.notes == nil {
		g.learn.notes = map[string]punNote{}
	}
	g.learn.notes[memberKey(chatID, userID)] = n
	g.learn.mu.Unlock()
}

func (g *Guard) noteUndo(chatID, userID int64, n undoNote) {
	n.at = time.Now()
	g.learn.mu.Lock()
	if g.learn.undos == nil {
		g.learn.undos = map[string]undoNote{}
	}
	g.learn.undos[memberKey(chatID, userID)] = n
	g.learn.mu.Unlock()
}

func (g *Guard) takeNote(key string) (punNote, bool) {
	g.learn.mu.Lock()
	defer g.learn.mu.Unlock()
	n, ok := g.learn.notes[key]
	delete(g.learn.notes, key)
	for k, v := range g.learn.notes { // 顺手清理过期的
		if time.Since(v.at) > time.Minute {
			delete(g.learn.notes, k)
		}
	}
	return n, ok && time.Since(n.at) < time.Minute
}

func (g *Guard) takeUndo(key string) (undoNote, bool) {
	g.learn.mu.Lock()
	defer g.learn.mu.Unlock()
	n, ok := g.learn.undos[key]
	delete(g.learn.undos, key)
	return n, ok && time.Since(n.at) < time.Minute
}

// onAction Telegram 操作成功后的回调：记录处罚、标记撤销
func (g *Guard) onAction(chatID, userID int64, action string, d time.Duration) {
	if chatID >= 0 || userID <= 0 {
		return
	}
	key := memberKey(chatID, userID)
	switch action {
	case "ban", "mute", "kick":
		n, ok := g.takeNote(key)
		if action == "mute" && d == 0 && !ok {
			return // 入群验证期间的限制发言，不算处罚
		}
		if !ok {
			n = punNote{Source: "other"}
		}
		if n.Name == "" {
			if u, _ := g.st.UserByID(userID); u != nil {
				n.Name = strings.TrimSpace(u.FirstName + " " + u.LastName)
				if u.Username != "" {
					n.Name += " @" + u.Username
				}
			}
		}
		p := &model.Punishment{GroupID: chatID, UserID: userID, Name: n.Name, Action: action, Minutes: int(d / time.Minute),
			Source: n.Source, Reason: n.Reason, Text: truncate(n.Text, 1000), Operator: n.Operator}
		if err := g.st.AddPunishment(p); err != nil {
			slog.Warn("save punishment", "err", err)
		}
	case "unban", "unmute":
		want := map[string][]string{"unban": {"ban"}, "unmute": {"mute"}}[action]
		p, _ := g.st.LastPunishment(chatID, userID, want...)
		u, hasUndo := g.takeUndo(key)
		if p == nil {
			return
		}
		by := "群内按钮 / 命令"
		if hasUndo && u.By != "" {
			by = u.By
		}
		g.st.UndoPunishment(p.ID, by)
		// 撤销 AI / 样本误判的处罚 = 告诉 AI 这条不是广告
		learn := p.Source == "ai" || p.Source == "learned"
		if hasUndo && u.Learn != nil {
			learn = *u.Learn
		}
		if learn && strings.TrimSpace(p.Text) != "" {
			if _, err := g.AddSample(chatID, "ham", p.Text, "unban", by); err == nil {
				g.logf(context.Background(), "已学习误封样本（正常消息）：%s", esc(truncate(p.Text, 100)))
			}
		}
	}
}

// UndoPunishment 面板撤销一条处罚
func (g *Guard) UndoPunishment(ctx context.Context, groupID int64, id uint, by string, learn, approve bool) error {
	p, err := g.st.Punishment(groupID, id)
	if err != nil || p == nil {
		return errors.New("记录不存在")
	}
	if p.Undone {
		return errors.New("这条已经撤销过了")
	}
	g.noteUndo(groupID, p.UserID, undoNote{By: by, Learn: &learn})
	switch p.Action {
	case "ban":
		err = g.tg.Unban(ctx, groupID, p.UserID)
	case "mute":
		err = g.tg.Unmute(ctx, groupID, p.UserID)
	case "kick": // 踢出本身就能重新进群，只标记撤销
		g.st.UndoPunishment(p.ID, by)
		if learn && strings.TrimSpace(p.Text) != "" {
			g.AddSample(groupID, "ham", p.Text, "unban", by)
		}
	}
	if err != nil {
		return fmt.Errorf("Telegram 操作失败：%w", err)
	}
	if cur, _ := g.st.Punishment(groupID, id); cur != nil && !cur.Undone { // 回调没对上（比如对方已经不在封禁状态），兜底标记
		g.st.UndoPunishment(id, by)
	}
	if approve {
		g.st.SetApproved(groupID, p.UserID, true)
	}
	g.st.UpdateMember(groupID, p.UserID, map[string]any{"warns": 0})
	return nil
}

// MemberAction 面板对成员执行操作
func (g *Guard) MemberAction(ctx context.Context, groupID, userID int64, action string, minutes int) (string, error) {
	protected := func() bool {
		return userID == g.tg.Me().ID || g.cfg.IsOwner(userID) || g.admins.isAdmin(ctx, groupID, userID)
	}
	d := time.Duration(minutes) * time.Minute
	n := punNote{Source: "admin", Operator: "面板"}
	var err error
	var text string
	switch action {
	case "ban":
		if protected() {
			return "", errors.New("不能对管理员执行这个操作")
		}
		g.note(groupID, userID, n)
		err, text = g.tg.Ban(ctx, groupID, userID, d, false), "已封禁"
	case "mute":
		if protected() {
			return "", errors.New("不能对管理员执行这个操作")
		}
		if d == 0 {
			d = 24 * time.Hour
		}
		g.note(groupID, userID, n)
		err, text = g.tg.Mute(ctx, groupID, userID, d), "已禁言"
	case "kick":
		if protected() {
			return "", errors.New("不能对管理员执行这个操作")
		}
		g.note(groupID, userID, n)
		err, text = g.tg.Kick(ctx, groupID, userID), "已踢出"
	case "unban":
		g.noteUndo(groupID, userID, undoNote{By: "面板"})
		err, text = g.tg.Unban(ctx, groupID, userID), "已解封"
	case "unmute":
		g.noteUndo(groupID, userID, undoNote{By: "面板"})
		err, text = g.tg.Unmute(ctx, groupID, userID), "已解除禁言"
	case "clearwarn":
		err, text = g.st.UpdateMember(groupID, userID, map[string]any{"warns": 0}), "已清空警告"
	case "approve":
		err, text = g.st.SetApproved(groupID, userID, true), "已加入免检白名单"
	case "unapprove":
		err, text = g.st.SetApproved(groupID, userID, false), "已移出免检白名单"
	default:
		return "", errors.New("未知操作")
	}
	if err != nil {
		return "", err
	}
	return text, nil
}

// ---------- 学习样本 ----------

// normText 归一化：小写，只留字母数字和汉字（去掉空格、符号、表情这类规避手段）
func normText(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r >= 0xFF01 && r <= 0xFF5E { // 全角转半角
			r -= 0xFEE0
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func bigrams(s string) map[string]bool {
	r := []rune(s)
	out := map[string]bool{}
	for i := 0; i+1 < len(r); i++ {
		out[string(r[i:i+2])] = true
	}
	return out
}

// similarity 重合度：短的那段有多少落在长的那段里（广告常常是样本前后加几句话）
func similarity(a, b string) float64 {
	if a == "" || b == "" {
		return 0
	}
	if strings.Contains(a, b) || strings.Contains(b, a) {
		if min(len([]rune(a)), len([]rune(b))) >= 6 {
			return 1
		}
	}
	// 最长公共片段：广告核心那句一样、前后各加了不同的话
	lcsScore := 0.0
	if l := lcs([]rune(a), []rune(b)); l >= 6 {
		if f := float64(l) / float64(min(len([]rune(a)), len([]rune(b)))); f >= 0.6 {
			lcsScore = min(1, f+0.2)
		}
	}
	A, B := bigrams(a), bigrams(b)
	small, big := A, B
	if len(B) < len(A) {
		small, big = B, A
	}
	if len(small) < 5 {
		return lcsScore
	}
	hit := 0
	for k := range small {
		if big[k] {
			hit++
		}
	}
	// 再乘一个长度比例，避免一句很长的正常话里碰巧带了样本的一半
	ratio := float64(len(small)) / float64(len(big))
	score := float64(hit) / float64(len(small))
	if ratio < 0.3 {
		score *= 0.5 + ratio
	}
	return max(score, lcsScore)
}

// lcs 最长公共子串长度
func lcs(a, b []rune) int {
	if len(a) > 400 {
		a = a[:400]
	}
	if len(b) > 400 {
		b = b[:400]
	}
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	best := 0
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			if a[i-1] == b[j-1] {
				cur[j] = prev[j-1] + 1
				best = max(best, cur[j])
			} else {
				cur[j] = 0
			}
		}
		prev, cur = cur, prev
	}
	return best
}

func (g *Guard) loadSamples() []model.SpamSample {
	g.learn.mu.Lock()
	defer g.learn.mu.Unlock()
	if !g.learn.loaded {
		xs, err := g.st.Samples("")
		if err == nil {
			g.learn.samples, g.learn.loaded = xs, true
		}
	}
	return g.learn.samples
}

func (g *Guard) resetSamples() {
	g.learn.mu.Lock()
	g.learn.loaded = false
	g.learn.mu.Unlock()
}

// AddSample 添加学习样本（重复的不再添加）
func (g *Guard) AddSample(groupID int64, kind, text, source, by string) (*model.SpamSample, error) {
	text = strings.TrimSpace(text)
	if kind != "spam" && kind != "ham" {
		return nil, errors.New("类型只能是 spam 或 ham")
	}
	norm := normText(text)
	if len([]rune(norm)) < 6 {
		return nil, errors.New("内容太短（去掉符号后至少 6 个字），学不了")
	}
	for _, x := range g.loadSamples() {
		if x.Kind == kind && x.Norm == norm {
			return &x, nil
		}
	}
	// 反向样本冲突时以最新的为准
	for _, x := range g.loadSamples() {
		if x.Kind != kind && similarity(x.Norm, norm) >= 0.95 {
			g.st.DelSample(x.ID)
		}
	}
	x := &model.SpamSample{GroupID: groupID, Kind: kind, Text: truncate(text, 1000), Norm: norm, Source: source, By: by}
	err := g.st.AddSample(x)
	g.resetSamples()
	return x, err
}

func (g *Guard) DelSample(id uint) (bool, error) {
	ok, err := g.st.DelSample(id)
	g.resetSamples()
	return ok, err
}

// matchSample 找最像的样本
func (g *Guard) matchSample(text, kind string) (*model.SpamSample, float64) {
	norm := normText(text)
	if len([]rune(norm)) < 6 {
		return nil, 0
	}
	var best *model.SpamSample
	bestScore := 0.0
	xs := g.loadSamples()
	for i := range xs {
		if xs[i].Kind != kind {
			continue
		}
		if s := similarity(xs[i].Norm, norm); s > bestScore {
			best, bestScore = &xs[i], s
		}
	}
	return best, bestScore
}

// learnedCheck 先用已学习的样本判断：命中广告样本直接处罚，命中误封样本跳过 AI
// 返回 skipAI=true 表示这条和管理员确认过的正常消息很像，不用再问 AI
func (g *Guard) learnedCheck(grp *model.Group, m *models.Message, strict bool) (v *verdict, skipAI bool) {
	text := msgText(m)
	if h, s := g.matchSample(text, "ham"); h != nil && s >= 0.9 {
		return nil, true
	}
	x, s := g.matchSample(text, "spam")
	need := 0.8
	if !strict { // 老成员要求几乎一样才处理
		need = 0.95
	}
	if x == nil || s < need {
		return nil, false
	}
	g.st.HitSample(x.ID)
	action := grp.AIAction
	if action == "" {
		action = model.ActionBan
	}
	return &verdict{reason: fmt.Sprintf("命中已学习的广告样本 #%d（相似 %.0f%%）", x.ID, s*100), action: action, source: "learned"}, false
}

// aiExamples 把最近的样本作为参考例子拼进提示词，让 AI 学到本群的尺度
func (g *Guard) aiExamples() string {
	var spam, ham []string
	for _, x := range g.loadSamples() { // 已按 id 倒序
		t := strings.ReplaceAll(truncate(x.Text, 120), "\n", " ")
		if x.Kind == "spam" && len(spam) < 10 {
			spam = append(spam, "[广告] "+t)
		} else if x.Kind == "ham" && len(ham) < 6 {
			ham = append(ham, "[正常] "+t)
		}
	}
	if len(spam)+len(ham) == 0 {
		return ""
	}
	return "\n\n下面是本群管理员确认过的例子，按这个尺度判断（[正常] 是之前被误封、管理员确认没问题的消息，类似的要判 false）：\n" +
		strings.Join(append(spam, ham...), "\n")
}

// learnFromMessage 群里 /spam、举报处理时学习
func (g *Guard) learnSpam(ctx context.Context, groupID int64, text, source, by string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	if x, err := g.AddSample(groupID, "spam", text, source, by); err == nil && x != nil {
		g.logf(ctx, "已学习广告样本 #%d：%s", x.ID, esc(truncate(text, 100)))
	}
}

// learnCommand /spam /notspam
func (g *Guard) learnCommand(ctx context.Context, m *models.Message, grp *model.Group, name, arg string, reply func(string)) bool {
	by := displayName(m.From)
	switch name {
	case "spam":
		r := m.ReplyToMessage
		if r == nil {
			reply("用法：回复一条广告发 /spam，会删除、封禁发送者，并让机器人记住这类广告。")
			return true
		}
		text := msgText(r)
		g.tg.Delete(ctx, m.Chat.ID, r.ID)
		uid := senderID(r)
		banned := ""
		if r.From != nil && r.SenderChat == nil && uid != g.tg.Me().ID && !g.cfg.IsOwner(uid) && !g.admins.isAdmin(ctx, m.Chat.ID, uid) {
			g.note(m.Chat.ID, uid, punNote{Source: "admin", Reason: "管理员标记为广告", Text: text, Name: displayName(r.From), Operator: by})
			if err := g.tg.Ban(ctx, m.Chat.ID, uid, 0, true); err == nil {
				g.count(m.Chat.ID, "banned")
				banned = "，已封禁 " + mentionID(uid, displayName(r.From))
			}
		}
		x, err := g.AddSample(m.Chat.ID, "spam", text, "command", by)
		if err != nil {
			reply("已删除" + banned + "\n（没学习：" + esc(err.Error()) + "）")
			return true
		}
		reply(fmt.Sprintf("✅ 已删除%s\n已记住这条广告（样本 #%d），以后类似的会直接处理", banned, x.ID))
	case "notspam":
		text := strings.TrimSpace(arg)
		if m.ReplyToMessage != nil {
			text = msgText(m.ReplyToMessage)
		}
		if text == "" {
			reply("用法：回复一条被误判的消息发 /notspam，或 /notspam 文字")
			return true
		}
		x, err := g.AddSample(m.Chat.ID, "ham", text, "command", by)
		if err != nil {
			reply("⚠️ " + esc(err.Error()))
			return true
		}
		reply(fmt.Sprintf("✅ 已记住这是正常消息（样本 #%d），AI 会参考它，类似的不再拦", x.ID))
	default:
		return false
	}
	return true
}
