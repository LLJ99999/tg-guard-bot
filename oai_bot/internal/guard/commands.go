package guard

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"oai_bot/internal/model"
)

// adminMenu 管理员在输入框「/」菜单里看到的命令：只放最常用的几个。
// 其余设置（验证、锁定、防刷屏、屏蔽词、欢迎语、笔记、定时消息……）都在按钮面板 / AstrBot 后台面板里改，
// 旧命令仍然可用（hiddenAdminCommands），只是不再出现在菜单里刷屏。
var adminMenu = [][2]string{
	{"settings", "按钮设置面板"},
	{"ban", "封禁（回复消息），可加时长 /ban 1d"},
	{"mute", "禁言（回复消息），可加时长 /mute 2h"},
	{"kick", "踢出（回复消息）"},
	{"warn", "警告（回复消息）"},
	{"unban", "解封 / 解除禁言"},
	{"del", "删除回复的消息"},
}

// memberMenu 普通成员菜单
var memberMenu = [][2]string{
	{"rules", "查看群规"},
	{"report", "回复消息举报给管理员"},
}

// hiddenAdminCommands 不显示在菜单里、但仍然可用的管理命令（兼容老用法）
var hiddenAdminCommands = []string{
	"panel", "captcha", "tban", "unmute", "unwarn", "warns", "purge",
	"verify", "vtimeout", "setwelcome", "setrules", "flood", "warnlimit", "lock", "unlock",
	"filter", "unfilter", "filters", "cas", "ai", "newbie",
	"approve", "unapprove", "approved",
	"save", "clear", "reply", "noreply", "replies", "schedule", "unschedule", "schedules",
	"night", "antiraid", "dup", "stats", "gban", "ungban",
	"pin", "unpin", "warntime",
	"bioscan", "spam", "notspam", "ad", "ads", "adpreview", "delad", "adon", "adoff", "adsend",
	"newfed", "joinfed", "leavefed", "fedinfo", "fedadmin", "fedunadmin", "fban", "unfban",
}

// publicCommands 所有人可用
var publicCommands = map[string]bool{"rules": true, "id": true, "help": true, "start": true, "get": true, "notes": true, "report": true}

const helpText = `<b>oai_bot 群管</b>

<b>常用命令</b>（回复对方消息使用，或在后面写 @用户名 / 数字 ID）
/ban — 封禁，<code>/ban 1d</code> 临时封禁
/mute — 禁言，<code>/mute 2h</code> 限时禁言
/kick — 踢出　/warn — 警告
/unban — 解封 / 解除禁言
/del — 删除回复的消息

<b>其它设置不用记命令</b>
群里发 /settings 弹出按钮面板（验证、锁定、防刷屏、AI 审核、夜间模式……）。
欢迎语、群规、屏蔽词、笔记、自动回复、定时消息在 AstrBot 后台「TG 群管面板」里编辑。

<b>主页链接查重</b>（只报告，不自动处罚）
/bioscan 立即扫描，<code>/bioscan 24</code> 每 24 小时，<code>/bioscan off</code> 关闭，<code>/bioscan min 3</code> 至少 3 人同链接才报告

<b>定时广告</b>
先发好广告（可带图和格式），回复它：<code>/ad 2h 名字</code>，第二行起写按钮「文字 - 链接」
/ads 列表　/adpreview 私聊预览　/adoff /adon /adsend /delad 后面跟 ID

<b>AI 学习</b>
回复广告 /spam 删除+封禁+记住　回复误判消息 /notspam 记住是正常消息

<b>联邦封禁</b>（多个群共用封禁名单）
群主：/newfed 名称 创建，/joinfed 联邦ID 加入，/leavefed 退出
联邦管理员：/fban 联邦封禁，/unfban 解除，/fedinfo 查看

群友可用：/rules 群规　/report 举报　#名字 查看笔记`

// parseCommand 解析 /cmd@bot args；发给其它机器人的命令返回空
func parseCommand(text, botName string) (name, arg string) {
	if !strings.HasPrefix(text, "/") {
		return "", ""
	}
	head, rest, _ := strings.Cut(text[1:], " ")
	head, at, hasAt := strings.Cut(head, "@")
	if hasAt && !strings.EqualFold(at, botName) {
		return "", ""
	}
	return strings.ToLower(head), strings.TrimSpace(rest)
}

var adminOnly = map[string]bool{}

func init() {
	for _, c := range adminMenu {
		adminOnly[c[0]] = true
	}
	for _, c := range hiddenAdminCommands {
		adminOnly[c] = true
	}
}

// addressedToMe 命令是否明确 @ 了本机器人（/help@oai_bot）
func addressedToMe(text, botName string) bool {
	head, _, _ := strings.Cut(strings.TrimPrefix(text, "/"), " ")
	_, at, ok := strings.Cut(head, "@")
	return ok && strings.EqualFold(at, botName)
}

// runCommand 执行群命令，返回 true 表示已处理（不再走反垃圾检查）
func (g *Guard) runCommand(ctx context.Context, m *models.Message, name, arg string) bool {
	// /help /start 这类通用命令 AstrBot 也有：群里只有明确 @ 本机器人时才回应，避免两个机器人一起回、还把对方的命令删掉
	if (name == "help" || name == "start") && !addressedToMe(m.Text, g.tg.Me().Username) {
		return false
	}
	// /del 不回复任何消息时多半是给 AstrBot 的（删除会话），放行
	if name == "del" && m.ReplyToMessage == nil {
		return false
	}
	if publicCommands[name] {
		g.publicCommand(ctx, m, name)
		return true
	}
	if !adminOnly[name] {
		return false // 不认识的命令（可能是给 AstrBot 的），按普通消息处理
	}
	isAnon := m.SenderChat != nil && m.SenderChat.ID == m.Chat.ID
	if m.From == nil || !(isAnon || g.cfg.IsOwner(m.From.ID) || g.admins.isAdmin(ctx, m.Chat.ID, m.From.ID)) {
		g.tg.Delete(ctx, m.Chat.ID, m.ID)
		return true
	}
	grp, err := g.st.Group(m.Chat.ID, m.Chat.Title)
	if err != nil {
		return true
	}
	reply := func(text string) { g.tg.Notify(ctx, m.Chat.ID, text, nil, time.Minute) }
	done := func() { g.tg.Delete(ctx, m.Chat.ID, m.ID) }
	set := func(fields map[string]any, msg string) {
		if err := g.st.UpdateGroup(grp.ID, fields); err != nil {
			reply("保存失败：" + esc(err.Error()))
			return
		}
		reply(msg)
	}
	defer done()

	if g.fedCommand(ctx, grp, m, name, arg, reply) {
		return true
	}
	switch name {
	case "settings", "panel":
		// 直接在群里弹出按钮面板（只有有封禁权限的管理员能点），5 分钟后自动删除
		g.tg.Notify(ctx, m.Chat.ID, settingsText(grp, g.captcha != nil, g.ai.enabled()), g.panelMarkupFor(grp, true), 5*time.Minute)

	case "ban", "tban", "unban", "kick", "mute", "unmute", "warn", "unwarn", "warns", "gban", "ungban":
		g.moderate(ctx, grp, m, name, arg, reply)

	case "del":
		g.tg.Delete(ctx, m.Chat.ID, m.ReplyToMessage.ID)
	case "pin", "unpin":
		if m.ReplyToMessage == nil && name == "pin" {
			reply("请回复要置顶的消息；/pin loud 会通知全员")
			return true
		}
		var err error
		if name == "pin" {
			_, err = g.tg.PinChatMessage(ctx, &tgbot.PinChatMessageParams{ChatID: m.Chat.ID, MessageID: m.ReplyToMessage.ID, DisableNotification: arg != "loud"})
		} else if m.ReplyToMessage != nil {
			_, err = g.tg.UnpinChatMessage(ctx, &tgbot.UnpinChatMessageParams{ChatID: m.Chat.ID, MessageID: m.ReplyToMessage.ID})
		} else {
			_, err = g.tg.UnpinChatMessage(ctx, &tgbot.UnpinChatMessageParams{ChatID: m.Chat.ID})
		}
		if err != nil {
			reply("操作失败（机器人需要置顶权限）：" + esc(err.Error()))
		}
	case "warntime":
		n, err := strconv.Atoi(arg)
		if err != nil || n < 0 || n > 8760 {
			reply("用法：/warntime 小时数（0 表示警告永久有效）")
			return true
		}
		set(map[string]any{"warn_expire": n}, onOff(n == 0, "警告永久有效", fmt.Sprintf("警告 %d 小时后失效", n)))
	case "purge":
		if m.ReplyToMessage == nil {
			reply("请回复起始消息")
			return true
		}
		from := m.ReplyToMessage.ID
		if m.ID-from > 1000 {
			from = m.ID - 1000
		}
		ids := make([]int, 0, m.ID-from)
		for id := from; id < m.ID; id++ {
			ids = append(ids, id)
		}
		g.tg.Delete(ctx, m.Chat.ID, ids...)
		reply(fmt.Sprintf("已清理 %d 条消息", len(ids)))

	case "verify":
		switch arg {
		case model.VerifyOff, model.VerifyJoinRequest, model.VerifyRestrict:
			tip := ""
			if arg == model.VerifyJoinRequest {
				tip = "\n请在群设置 → 邀请链接 里开启「需要管理员批准」，或在群类型里开启「入群需审核」。"
			}
			set(map[string]any{"verify_mode": arg}, "入群验证："+verifyName(arg)+tip)
		default:
			reply("用法：/verify off|join|restrict")
		}
	case "captcha":
		if !slices.Contains(model.CaptchaTypes, arg) {
			reply("用法：/captcha image|math|emoji|button|web\nimage 图片验证码（默认）｜math 算术题｜emoji 点表情｜button 点按钮｜web 网页验证")
			return true
		}
		if arg == model.CaptchaWeb && g.captcha == nil {
			reply("网页验证需要配置 captcha 和 web.public_url，其它几种不需要")
			return true
		}
		set(map[string]any{"captcha_type": arg}, "验证码类型："+captchaName(arg))
	case "vtimeout":
		n, err := strconv.Atoi(arg)
		if err != nil || n < 30 || n > 86400 {
			reply("用法：/vtimeout 300（30~86400 秒）")
			return true
		}
		set(map[string]any{"verify_timeout": n}, fmt.Sprintf("验证超时：%s", humanDur(time.Duration(n)*time.Second)))
	case "setwelcome":
		if arg == "" {
			reply("用法：/setwelcome 欢迎 {name} 加入 {group}！ ；/setwelcome off 关闭")
			return true
		}
		if arg == "off" {
			arg = ""
		}
		set(map[string]any{"welcome": arg}, "欢迎语已更新")
	case "setrules":
		set(map[string]any{"rules": arg}, "群规已更新")
	case "flood":
		if arg == "off" {
			set(map[string]any{"flood_count": 0}, "防刷屏已关闭")
			return true
		}
		var c, s int
		if _, err := fmt.Sscan(arg, &c, &s); err != nil || c < 2 || s < 1 || s > 600 {
			reply("用法：/flood 6 10（10 秒内超过 6 条算刷屏）；/flood off")
			return true
		}
		set(map[string]any{"flood_count": c, "flood_second": s}, fmt.Sprintf("防刷屏：%d 秒内超过 %d 条禁言 10 分钟", s, c))
	case "warnlimit":
		var n int
		var act string
		fmt.Sscan(arg, &n, &act)
		if n < 1 || n > 20 || (act != model.ActionMute && act != model.ActionKick && act != model.ActionBan) {
			reply("用法：/warnlimit 3 mute|kick|ban")
			return true
		}
		set(map[string]any{"warn_limit": n, "warn_action": act}, fmt.Sprintf("警告 %d 次后%s", n, actionName(act)))
	case "lock", "unlock":
		col, ok := lockColumns[arg]
		if !ok {
			reply("可用：link 链接｜invite 邀请链接｜forward 转发｜button 按钮消息｜viabot inline 机器人｜story 动态｜sticker 贴纸｜gif 动图｜media 图片视频文件｜voice 语音｜contact 名片位置｜poll 投票｜game 骰子游戏｜foreign 外文")
			return true
		}
		on := name == "lock"
		set(map[string]any{col: on}, fmt.Sprintf("%s：%s", lockNames[arg], onOff(on, "已锁定", "已解锁")))
	case "filter":
		if arg == "" {
			reply("用法：/filter 关键词 [delete|warn|mute|ban]；/filter re:正则 [处理方式]\n默认 warn（删除并警告）")
			return true
		}
		// 末尾可带处理方式：/filter 关键词 ban|mute|delete
		f := &model.Filter{GroupID: grp.ID, Pattern: arg, Action: "warn"}
		if i := strings.LastIndex(arg, " "); i > 0 {
			if a := arg[i+1:]; a == "ban" || a == "mute" || a == "delete" || a == "warn" {
				f.Pattern, f.Action, arg = strings.TrimSpace(arg[:i]), a, strings.TrimSpace(arg[:i])
			}
		}
		if p, ok := strings.CutPrefix(arg, "re:"); ok {
			if _, err := regexp.Compile(p); err != nil {
				reply("正则有误：" + esc(err.Error()))
				return true
			}
			f.Pattern, f.Regex = p, true
		}
		if err := g.st.AddFilter(f); err != nil {
			reply(esc(err.Error()))
			return true
		}
		reply("已添加屏蔽：" + esc(f.Pattern))
	case "unfilter":
		ok, _ := g.st.DelFilter(grp.ID, strings.TrimPrefix(arg, "re:"))
		reply(onOff(ok, "已删除", "没有这条屏蔽词"))
	case "filters":
		fs, _ := g.st.Filters(grp.ID)
		if len(fs) == 0 {
			reply("还没有屏蔽词")
			return true
		}
		var b strings.Builder
		b.WriteString("<b>屏蔽词</b>\n")
		for _, f := range fs {
			if f.Regex {
				b.WriteString("re:")
			}
			b.WriteString("<code>" + esc(f.Pattern) + "</code>\n")
		}
		g.tg.Notify(ctx, m.Chat.ID, b.String(), nil, 2*time.Minute)
	case "cas", "ai":
		if arg != "on" && arg != "off" {
			reply(fmt.Sprintf("用法：/%s on|off", name))
			return true
		}
		if name == "ai" && arg == "on" && !g.ai.enabled() {
			reply("机器人未配置 ai（base_url / api_key / model）")
			return true
		}
		set(map[string]any{name: arg == "on"}, strings.ToUpper(name)+"："+onOff(arg == "on", "开启", "关闭"))
	default:
		if !g.adCommand(ctx, m, grp, name, arg, reply) && !g.learnCommand(ctx, m, grp, name, arg, reply) {
			g.runExtra(ctx, m, grp, name, arg, reply)
		}
	case "newbie":
		n, err := strconv.Atoi(arg)
		if err != nil || n < 0 || n > 100 {
			reply("用法：/newbie 5（新人前 5 条消息严格检查，0 关闭）")
			return true
		}
		set(map[string]any{"newbie_msgs": n}, fmt.Sprintf("新人严格检查：前 %d 条消息", n))
	}
	return true
}

func (g *Guard) publicCommand(ctx context.Context, m *models.Message, name string) {
	switch name {
	case "id":
		text := fmt.Sprintf("群 ID：<code>%d</code>", m.Chat.ID)
		if m.From != nil {
			text += fmt.Sprintf("\n你的 ID：<code>%d</code>", m.From.ID)
		}
		if r := m.ReplyToMessage; r != nil && r.From != nil {
			text += fmt.Sprintf("\n对方 ID：<code>%d</code>", r.From.ID)
		}
		g.tg.Notify(ctx, m.Chat.ID, text, nil, time.Minute)
	case "rules":
		grp, err := g.st.Group(m.Chat.ID, m.Chat.Title)
		if err != nil {
			return
		}
		if grp.Rules == "" {
			g.tg.Notify(ctx, m.Chat.ID, "本群还没有设置群规", nil, 30*time.Second)
		} else {
			g.tg.Notify(ctx, m.Chat.ID, "<b>群规</b>\n"+esc(grp.Rules), nil, 2*time.Minute)
		}
	case "help", "start":
		g.tg.Notify(ctx, m.Chat.ID, helpText, nil, 2*time.Minute)
	case "get":
		_, arg := parseCommand(m.Text, g.tg.Me().Username)
		if n, _ := g.st.Note(m.Chat.ID, strings.TrimPrefix(arg, "#")); n != nil {
			g.sendContent(ctx, m.Chat.ID, n.Content, 0)
		}
	case "notes":
		ns, _ := g.st.Notes(m.Chat.ID)
		if len(ns) == 0 {
			g.tg.Notify(ctx, m.Chat.ID, "本群还没有笔记", nil, 30*time.Second)
			break
		}
		var names []string
		for _, n := range ns {
			names = append(names, "<code>#"+esc(n.Name)+"</code>")
		}
		g.tg.Notify(ctx, m.Chat.ID, "<b>笔记</b>（发送 #名字 查看）\n"+strings.Join(names, "　"), nil, 2*time.Minute)
	case "report":
		g.report(ctx, m)
	}
	g.tg.Delete(ctx, m.Chat.ID, m.ID)
}

// moderate 针对某个用户的管理操作
func (g *Guard) moderate(ctx context.Context, grp *model.Group, m *models.Message, name, arg string, reply func(string)) {
	uid, uname, rest, err := g.target(m, arg)
	if err != nil {
		reply(esc(err.Error()))
		return
	}
	who := mentionID(uid, uname)
	if name != "warns" && name != "unban" && name != "unmute" && name != "unwarn" && name != "ungban" {
		if uid == g.tg.Me().ID || g.admins.isAdmin(ctx, m.Chat.ID, uid) || g.cfg.IsOwner(uid) {
			reply("不能对管理员执行这个操作")
			return
		}
	}
	if name == "gban" || name == "ungban" {
		if m.From == nil || !g.cfg.IsOwner(m.From.ID) {
			reply("只有机器人主人可以全局封禁")
			return
		}
	}
	var d time.Duration
	if name == "ban" { // /ban 1d 等同 /tban 1d
		first, tail, _ := strings.Cut(rest, " ")
		if dd, ok := parseDur(first); ok {
			d, rest = dd, strings.TrimSpace(tail)
		}
	}
	if name == "tban" || name == "mute" {
		first, tail, _ := strings.Cut(rest, " ")
		if dd, ok := parseDur(first); ok {
			d, rest = dd, strings.TrimSpace(tail)
		} else if name == "tban" {
			reply("用法：/tban 1d（支持 s m h d）")
			return
		}
	}
	reason := ""
	if rest != "" {
		reason = "\n原因：" + esc(rest)
	}
	if name == "ban" || name == "tban" || name == "kick" || name == "mute" || name == "gban" {
		pt := ""
		if m.ReplyToMessage != nil {
			pt = msgText(m.ReplyToMessage)
		}
		g.note(m.Chat.ID, uid, punNote{Source: "admin", Reason: rest, Text: pt, Name: uname, Operator: displayName(m.From)})
	}
	if name == "unban" || name == "unmute" {
		g.noteUndo(m.Chat.ID, uid, undoNote{By: displayName(m.From)})
	}
	var e error
	var text string
	switch name {
	case "ban", "tban":
		e = g.tg.Ban(ctx, m.Chat.ID, uid, d, true)
		text = fmt.Sprintf("已封禁 %s%s%s", who, durText(d), reason)
	case "unban":
		// 一个命令同时处理封禁和禁言：在群里的人解除禁言，不在群里的人解除封禁
		if mem, err := g.tg.GetChatMember(ctx, &tgbot.GetChatMemberParams{ChatID: m.Chat.ID, UserID: uid}); err == nil && mem.Type == models.ChatMemberTypeRestricted {
			e = g.tg.Unmute(ctx, m.Chat.ID, uid)
			text = "已解除禁言 " + who
		} else {
			e = g.tg.Unban(ctx, m.Chat.ID, uid)
			text = "已解除封禁 " + who
		}
	case "kick":
		e = g.tg.Kick(ctx, m.Chat.ID, uid)
		text = fmt.Sprintf("已踢出 %s%s", who, reason)
	case "mute":
		e = g.tg.Mute(ctx, m.Chat.ID, uid, d)
		text = fmt.Sprintf("已禁言 %s%s%s", who, durText(d), reason)
	case "unmute":
		e = g.tg.Unmute(ctx, m.Chat.ID, uid)
		text = "已解除禁言 " + who
	case "warn":
		if m.ReplyToMessage != nil {
			g.tg.Delete(ctx, m.Chat.ID, m.ReplyToMessage.ID)
		}
		g.warn(ctx, grp, m.Chat.ID, uid, who, strings.TrimPrefix(rest, " "))
		return
	case "unwarn":
		n, _ := g.st.AddWarn(m.Chat.ID, uid, -1)
		text = fmt.Sprintf("已撤销一次警告，%s 当前 %d/%d", who, n, grp.WarnLimit)
	case "warns":
		mem, _ := g.st.Member(m.Chat.ID, uid)
		n := 0
		if mem != nil {
			n = mem.Warns
		}
		text = fmt.Sprintf("%s 当前警告 %d/%d", who, n, grp.WarnLimit)
	case "gban":
		e = g.st.SetGBan(uid, rest, true)
		if e == nil {
			g.tg.Ban(ctx, m.Chat.ID, uid, 0, true)
		}
		text = fmt.Sprintf("已全局封禁 %s（所有群生效，进群 / 发言即封）%s", who, reason)
	case "ungban":
		e = g.st.SetGBan(uid, "", false)
		text = "已解除全局封禁 " + who
	}
	if e != nil {
		reply("操作失败：" + esc(e.Error()) + "\n请确认机器人有相应管理员权限")
		return
	}
	g.tg.Notify(ctx, m.Chat.ID, text, nil, 2*time.Minute)
	op := "匿名管理员"
	if m.From != nil && m.SenderChat == nil {
		op = mention(m.From)
	}
	g.logf(ctx, "%s\n群：%s\n操作人：%s", text, esc(grp.Title), op)
}

// target 解析操作对象：回复的消息 > @用户名 > 数字 ID。返回剩余参数
func (g *Guard) target(m *models.Message, arg string) (int64, string, string, error) {
	if r := m.ReplyToMessage; r != nil && r.From != nil && r.SenderChat == nil && !r.IsAutomaticForward {
		return r.From.ID, displayName(r.From), arg, nil
	}
	first, rest, _ := strings.Cut(arg, " ")
	rest = strings.TrimSpace(rest)
	if first == "" {
		return 0, "", "", errors.New("请回复对方的消息，或写 @用户名 / 数字 ID")
	}
	if id, err := strconv.ParseInt(first, 10, 64); err == nil {
		name := ""
		if u, _ := g.st.UserByID(id); u != nil {
			name = strings.TrimSpace(u.FirstName + " " + u.LastName)
		}
		return id, name, rest, nil
	}
	if strings.HasPrefix(first, "@") {
		u, _ := g.st.UserByUsername(first)
		if u == nil {
			return 0, "", "", fmt.Errorf("没见过 %s，请改为回复对方消息或使用数字 ID", first)
		}
		return u.ID, strings.TrimSpace(u.FirstName + " " + u.LastName), rest, nil
	}
	return 0, "", "", errors.New("请回复对方的消息，或写 @用户名 / 数字 ID")
}

var durRe = regexp.MustCompile(`^(\d+)([smhd])$`)

// parseDur 解析 30s 10m 2h 1d
func parseDur(s string) (time.Duration, bool) {
	mm := durRe.FindStringSubmatch(strings.ToLower(s))
	if mm == nil {
		return 0, false
	}
	n, _ := strconv.Atoi(mm[1])
	unit := map[string]time.Duration{"s": time.Second, "m": time.Minute, "h": time.Hour, "d": 24 * time.Hour}[mm[2]]
	return time.Duration(n) * unit, n > 0
}

func durText(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	return "，时长 " + humanDur(d)
}

var lockColumns = map[string]string{
	"link": "lock_link", "forward": "lock_forward", "button": "lock_button", "viabot": "lock_via_bot", "story": "lock_story",
	"sticker": "lock_sticker", "gif": "lock_gif", "media": "lock_media", "voice": "lock_voice",
	"invite": "lock_invite", "contact": "lock_contact", "poll": "lock_poll", "game": "lock_game", "foreign": "lock_foreign",
}

var lockNames = map[string]string{
	"link": "链接", "forward": "转发", "button": "按钮消息", "viabot": "inline 机器人", "story": "动态",
	"sticker": "贴纸", "gif": "动图", "media": "图片视频文件", "voice": "语音",
	"invite": "邀请链接", "contact": "名片和位置", "poll": "投票", "game": "骰子和游戏", "foreign": "外文消息",
}

func onOff(b bool, yes, no string) string {
	if b {
		return yes
	}
	return no
}

func verifyName(mode string) string {
	switch mode {
	case model.VerifyJoinRequest:
		return "入群申请 + 人机验证"
	case model.VerifyRestrict:
		return "进群禁言 + 人机验证"
	}
	return "关闭"
}

func actionName(a string) string {
	switch a {
	case model.ActionBan:
		return "封禁"
	case model.ActionKick:
		return "踢出"
	}
	return "禁言 24 小时"
}

func settingsText(g *model.Group, captchaOK, aiOK bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<b>%s</b>\n", esc(g.Title))
	b.WriteString("点下方按钮切换，立即生效。\n")
	if g.RaidUntil != nil && time.Now().Before(*g.RaidUntil) {
		b.WriteString("\n防爆群生效中：暂时拒绝所有新人")
	}
	if g.VerifyMode != model.VerifyOff && g.CaptchaType == model.CaptchaWeb && !captchaOK {
		b.WriteString("\n网页验证未配置（captcha / web.public_url），入群验证不会生效，请换成图片验证码等内置方式")
	}
	if g.AI && !aiOK {
		b.WriteString("\n未配置 ai，AI 审核不会生效")
	}
	b.WriteString("\n<i>欢迎语、群规、屏蔽词、笔记、定时消息请在 AstrBot 后台面板编辑。</i>")
	return b.String()
}
