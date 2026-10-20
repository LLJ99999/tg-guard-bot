package guard

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-telegram/bot/models"

	"oai_bot/internal/model"
)

var hmRe = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

// runExtra 笔记、自动回复、白名单、定时、夜间、防爆群、统计等管理命令；返回 false 表示不是这些命令
func (g *Guard) runExtra(ctx context.Context, m *models.Message, grp *model.Group, name, arg string, reply func(string)) bool {
	set := func(fields map[string]any, msg string) {
		if err := g.st.UpdateGroup(grp.ID, fields); err != nil {
			reply("保存失败：" + esc(err.Error()))
			return
		}
		reply(msg)
	}
	switch name {
	case "save":
		nm, rest, _ := strings.Cut(arg, " ")
		nm = strings.ToLower(strings.TrimPrefix(nm, "#"))
		c, ok := contentFrom(m, strings.TrimSpace(rest))
		if nm == "" || !ok {
			reply("用法：/save 名字 内容 ；或回复一条消息 /save 名字（图片、按钮都会保留）")
			return true
		}
		if err := g.st.SaveNote(&model.Note{GroupID: grp.ID, Name: nm, Content: c}); err != nil {
			reply(esc(err.Error()))
			return true
		}
		reply(fmt.Sprintf("已保存笔记 #%s，群友发 #%s 或 /get %s 即可查看", esc(nm), esc(nm), esc(nm)))
	case "clear":
		ok, _ := g.st.DelNote(grp.ID, strings.TrimPrefix(arg, "#"))
		reply(onOff(ok, "已删除", "没有这条笔记"))

	case "reply":
		kw, rest, _ := strings.Cut(arg, " ")
		kw = strings.ToLower(kw)
		c, ok := contentFrom(m, strings.TrimSpace(rest))
		if kw == "" || !ok {
			reply("用法：/reply 关键词 回复内容 ；或回复一条消息 /reply 关键词\n消息里包含关键词就自动回复")
			return true
		}
		if err := g.st.SaveReply(&model.AutoReply{GroupID: grp.ID, Keyword: kw, Content: c}); err != nil {
			reply(esc(err.Error()))
			return true
		}
		reply("已添加自动回复：" + esc(kw))
	case "noreply":
		ok, _ := g.st.DelReply(grp.ID, arg)
		reply(onOff(ok, "已删除", "没有这个关键词"))
	case "replies":
		rs, _ := g.st.Replies(grp.ID)
		var names []string
		for _, r := range rs {
			names = append(names, "<code>"+esc(r.Keyword)+"</code>")
		}
		reply(onOff(len(names) > 0, "<b>自动回复</b>\n"+strings.Join(names, "\n"), "还没有自动回复"))

	case "approve", "unapprove":
		uid, uname, _, err := g.target(m, arg)
		if err != nil {
			reply(esc(err.Error()))
			return true
		}
		g.st.SetApproved(grp.ID, uid, name == "approve")
		reply(fmt.Sprintf("%s %s", onOff(name == "approve", "已加入白名单（不受反垃圾限制）：", "已移出白名单："), mentionID(uid, uname)))
	case "approved":
		as, _ := g.st.ApprovedList(grp.ID)
		if len(as) == 0 {
			reply("白名单为空")
			return true
		}
		var b strings.Builder
		b.WriteString("<b>白名单</b>\n")
		for _, a := range as {
			nm := ""
			if u, _ := g.st.UserByID(a.UserID); u != nil {
				nm = strings.TrimSpace(u.FirstName + " " + u.LastName)
			}
			b.WriteString(mentionID(a.UserID, nm) + "\n")
		}
		reply(b.String())

	case "schedule":
		at, rest, _ := strings.Cut(arg, " ")
		c, ok := contentFrom(m, strings.TrimSpace(rest))
		if !hmRe.MatchString(at) || !ok {
			reply("用法：/schedule 09:00 早安 ；或回复一条消息 /schedule 09:00\n每天定时发送（时区 " + esc(g.cfg.Bot.Timezone) + "）")
			return true
		}
		sc := &model.Schedule{GroupID: grp.ID, At: at, Content: c}
		if err := g.st.AddSchedule(sc); err != nil {
			reply(esc(err.Error()))
			return true
		}
		reply(fmt.Sprintf("已添加定时消息 #%d，每天 %s 发送", sc.ID, at))
	case "unschedule":
		id, _ := strconv.Atoi(strings.TrimPrefix(arg, "#"))
		ok, _ := g.st.DelSchedule(grp.ID, uint(id))
		reply(onOff(ok, "已删除", "没有这个编号，用 /schedules 查看"))
	case "schedules":
		ss, _ := g.st.Schedules(grp.ID)
		if len(ss) == 0 {
			reply("没有定时消息")
			return true
		}
		var b strings.Builder
		b.WriteString("<b>定时消息</b>\n")
		for _, s := range ss {
			fmt.Fprintf(&b, "#%d　%s　%s\n", s.ID, s.At, esc(truncate(strings.ReplaceAll(s.Text, "\n", " "), 30)))
		}
		reply(b.String())

	case "night":
		if arg == "off" {
			set(map[string]any{"night_on": false}, "夜间模式已关闭")
			return true
		}
		var s, e string
		fmt.Sscan(arg, &s, &e)
		if arg == "on" {
			s, e = grp.NightStart, grp.NightEnd
		}
		if !hmRe.MatchString(s) || !hmRe.MatchString(e) || s == e {
			reply("用法：/night 23:30 07:00 ；/night on ；/night off\n时间段内全员禁言（管理员除外）")
			return true
		}
		set(map[string]any{"night_on": true, "night_start": s, "night_end": e}, fmt.Sprintf("夜间模式：每天 %s ~ %s 全员禁言", s, e))
	case "antiraid":
		switch arg {
		case "off":
			set(map[string]any{"raid_limit": 0, "raid_until": nil}, "防爆群已关闭")
		case "stop":
			set(map[string]any{"raid_until": nil}, "已解除防爆群状态")
		default:
			n, err := strconv.Atoi(arg)
			if err != nil || n < 3 || n > 500 {
				reply("用法：/antiraid 15（1 分钟进群超过 15 人自动拒绝新人 15 分钟）；/antiraid stop 解除；/antiraid off 关闭")
				return true
			}
			set(map[string]any{"raid_limit": n}, fmt.Sprintf("防爆群：1 分钟超过 %d 人进群即触发", n))
		}
	case "bioscan":
		f := strings.Fields(arg)
		switch {
		case len(f) == 0 || f[0] == "now":
			if !g.StartBioScan(grp.ID, 0) {
				reply("已有查重在进行中，请稍后")
				return true
			}
			reply("🔍 已开始主页链接查重，人多时需要几分钟，结果会发到日志频道（未配置则发到本群）")
		case f[0] == "off":
			set(map[string]any{"bio_scan": false}, "定时主页查重：关闭")
		case f[0] == "min" && len(f) == 2:
			n, err := strconv.Atoi(f[1])
			if err != nil || n < 2 || n > 100 {
				reply("用法：/bioscan min 3（同一链接至少 3 人才报告，2~100）")
				return true
			}
			set(map[string]any{"bio_scan_min": n}, fmt.Sprintf("同一链接至少 %d 人才报告", n))
		default:
			h, err := strconv.Atoi(f[0])
			if err != nil || h < 1 || h > 168 {
				reply("用法：\n/bioscan — 立即扫描一次\n/bioscan 24 — 每 24 小时自动扫描（1~168）\n/bioscan off — 关闭定时扫描\n/bioscan min 3 — 同一链接至少 3 人才报告")
				return true
			}
			set(map[string]any{"bio_scan": true, "bio_scan_hours": h}, fmt.Sprintf("定时主页查重：每 %d 小时", h))
		}
	case "dup":
		if arg != "on" && arg != "off" {
			reply("用法：/dup on|off（同一段文字被 3 个人或同一人发 3 次即删除并警告）")
			return true
		}
		set(map[string]any{"dup_check": arg == "on"}, "重复内容检测："+onOff(arg == "on", "开启", "关闭"))
	case "stats":
		g.sendStats(ctx, grp)
	default:
		return false
	}
	return true
}

func (g *Guard) sendStats(ctx context.Context, grp *model.Group) {
	loc := g.cfg.Location()
	var days []string
	for i := 6; i >= 0; i-- {
		days = append(days, time.Now().In(loc).AddDate(0, 0, -i).Format("2006-01-02"))
	}
	ss, _ := g.st.Stats(grp.ID, days)
	by := map[string]model.Stat{}
	var tot model.Stat
	for _, s := range ss {
		by[s.Day] = s
		tot.Messages += s.Messages
		tot.Joins += s.Joins
		tot.Verified += s.Verified
		tot.Deleted += s.Deleted
		tot.Banned += s.Banned
		tot.Muted += s.Muted
	}
	var b strings.Builder
	fmt.Fprintf(&b, "<b>%s</b> 近 7 天\n<code>日期   消息 进群 验证 删除 封禁 禁言\n", esc(grp.Title))
	for _, d := range days {
		s := by[d]
		fmt.Fprintf(&b, "%s %5d %4d %4d %4d %4d %4d\n", d[5:], s.Messages, s.Joins, s.Verified, s.Deleted, s.Banned, s.Muted)
	}
	fmt.Fprintf(&b, "合计  %5d %4d %4d %4d %4d %4d</code>", tot.Messages, tot.Joins, tot.Verified, tot.Deleted, tot.Banned, tot.Muted)
	g.tg.Notify(ctx, grp.ID, b.String(), nil, 3*time.Minute)
}

// report 群友举报：回复可疑消息发 /report，通知管理员并附处理按钮
func (g *Guard) report(ctx context.Context, m *models.Message) {
	r := m.ReplyToMessage
	if r == nil || r.From == nil {
		g.tg.Notify(ctx, m.Chat.ID, "请回复要举报的消息再发 /report", nil, 20*time.Second)
		return
	}
	if g.admins.isAdmin(ctx, m.Chat.ID, r.From.ID) {
		return
	}
	// 用零宽字符隐藏 @ 管理员，管理员会收到提醒但不刷屏
	var hidden strings.Builder
	for id, cm := range g.admins.get(ctx, m.Chat.ID) {
		if u := memberUser(cm); u != nil && !u.IsBot {
			fmt.Fprintf(&hidden, `<a href="tg://user?id=%d">&#8203;</a>`, id)
		}
	}
	rep, isNew, err := g.st.AddReport(&model.Report{GroupID: m.Chat.ID, ReporterID: m.From.ID, Reporter: displayName(m.From),
		TargetID: r.From.ID, Target: displayName(r.From), MsgID: r.ID, Text: truncate(msgText(r), 500)})
	if err != nil {
		return
	}
	if !isNew {
		g.tg.Notify(ctx, m.Chat.ID, fmt.Sprintf("这条消息已经有人举报了（共 %d 次），管理员会尽快处理", rep.Count), nil, 20*time.Second)
		return
	}
	text := fmt.Sprintf("举报 #%d：%s 举报了 %s 的消息，已通知管理员%s", rep.ID, mention(m.From), mention(r.From), hidden.String())
	btn := func(t, act string) models.InlineKeyboardButton {
		return models.InlineKeyboardButton{Text: t, CallbackData: fmt.Sprintf("rp%s:%d:%d", act, m.Chat.ID, rep.ID)}
	}
	if msg := g.tg.Notify(ctx, m.Chat.ID, text, &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{
		{btn("删除消息", "del"), btn("禁言 24 小时", "mute"), btn("封禁", "ban")},
		{btn("忽略", "ign")},
	}}, 6*time.Hour); msg != nil {
		g.st.SetReportNotify(rep.ID, msg.ID)
	}
	g.logf(ctx, "举报 #%d：%s 举报 %s\n群：%s\n内容：%s", rep.ID, mention(m.From), mention(r.From), esc(m.Chat.Title), esc(truncate(msgText(r), 200)))
}

// respond 通过检查的消息：#笔记、关键词自动回复
func (g *Guard) respond(ctx context.Context, m *models.Message) {
	text := msgText(m)
	if text == "" || (m.From != nil && m.From.IsBot) {
		return
	}
	if strings.HasPrefix(text, "#") {
		nm, _, _ := strings.Cut(text[1:], " ")
		if n, _ := g.st.Note(m.Chat.ID, nm); n != nil {
			g.sendContent(ctx, m.Chat.ID, n.Content, m.ID)
			return
		}
	}
	rs, _ := g.st.Replies(m.Chat.ID)
	low := strings.ToLower(text)
	for _, r := range rs {
		if strings.Contains(low, r.Keyword) && g.replyCD.allow(fmt.Sprintf("%d:%s", m.Chat.ID, r.Keyword)) {
			g.sendContent(ctx, m.Chat.ID, r.Content, m.ID)
			return
		}
	}
}

// cooldown 简单的按 key 冷却，防止自动回复被刷
type cooldown struct {
	mu   sync.Mutex
	last map[string]time.Time
	d    time.Duration
}

func (c *cooldown) allow(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	if c.last == nil {
		c.last = map[string]time.Time{}
	}
	if t, ok := c.last[key]; ok && now.Sub(t) < c.d {
		return false
	}
	c.last[key] = now
	if len(c.last) > 5000 {
		for k, t := range c.last {
			if now.Sub(t) >= c.d {
				delete(c.last, k)
			}
		}
	}
	return true
}
