package guard

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"oai_bot/internal/model"
)

// Telegram 里的按钮面板（私聊和群里 /settings 共用）。
// 回调数据：s:<群ID>:<页面>[:<动作>]，页面之间点按钮切换（编辑同一条消息），
// 需要输入文字的设置点「✏️」后机器人在私聊里等你发下一条消息（支持格式、动态表情、图片）。

// ---------- 权限 ----------

// canPanel 能不能在 Telegram 里管理这个群：机器人主人总是可以；
// 群设置为「只限名单」时只看名单，否则有封禁权限的管理员（含管理员白名单）都可以
func (g *Guard) canPanel(ctx context.Context, grp *model.Group, uid int64) bool {
	if g.cfg.IsOwner(uid) {
		return true
	}
	if grp.PanelMode == "list" {
		return slices.Contains(ParseIDList(grp.PanelAdmins), uid)
	}
	return g.admins.canRestrict(ctx, grp.ID, uid)
}

// canPanelAccess 能不能改「谁能管理」：机器人主人或群主
func (g *Guard) canPanelAccess(ctx context.Context, grp *model.Group, uid int64) bool {
	return g.cfg.IsOwner(uid) || g.isCreator(ctx, grp.ID, uid)
}

// panelGroups 列出该用户能管理的群
func (g *Guard) panelGroups(ctx context.Context, userID int64) []model.Group {
	gs, _ := g.st.Groups()
	var out []model.Group
	for i := range gs {
		if !gs[i].BotLeft && g.canPanel(ctx, &gs[i], userID) {
			out = append(out, gs[i])
		}
	}
	return out
}

func (g *Guard) sendGroupPicker(ctx context.Context, chatID, userID int64) {
	gs := g.panelGroups(ctx, userID)
	if len(gs) == 0 {
		g.tg.Notify(ctx, chatID, "没有找到你能管理的群。\n请先把我拉进群并设为管理员；如果群主设置了「只限指定管理员」，需要群主把你加进名单。", nil, 0)
		return
	}
	g.setPanelMenu(ctx, userID)
	var rows [][]models.InlineKeyboardButton
	if u := g.miniAppURL(0); u != "" {
		rows = append(rows, []models.InlineKeyboardButton{{Text: "📱 打开完整管理面板", WebApp: &models.WebAppInfo{URL: u}}})
	}
	for _, grp := range gs {
		rows = append(rows, []models.InlineKeyboardButton{{Text: "⚙️ " + truncate(grp.Title, 30), CallbackData: fmt.Sprintf("s:%d:home", grp.ID)}})
	}
	g.tg.Notify(ctx, chatID, "<b>选择要管理的群</b>", &models.InlineKeyboardMarkup{InlineKeyboard: rows}, 0)
}

// sendPanel 私聊发出某个群的面板首页
func (g *Guard) sendPanel(ctx context.Context, chatID, groupID int64) {
	g.sendPanelPage(ctx, chatID, groupID, "home", chatID)
}

func (g *Guard) sendPanelPage(ctx context.Context, chatID, groupID int64, page string, uid int64) {
	grp, err := g.st.Group(groupID, "")
	if err != nil {
		return
	}
	text, mk := g.panelView(ctx, grp, page, uid, false)
	g.tg.Notify(ctx, chatID, text, mk, 0)
}

// panelMarkupFor 群里 /settings 弹出的面板
func (g *Guard) panelMarkupFor(grp *model.Group, inGroup bool) *models.InlineKeyboardMarkup {
	_, mk := g.panelView(context.Background(), grp, "home", 0, inGroup)
	return mk
}

// ---------- 小工具 ----------

type pbtn = models.InlineKeyboardButton

func (g *Guard) pb(grp *model.Group, text, data string) pbtn {
	return pbtn{Text: text, CallbackData: fmt.Sprintf("s:%d:%s", grp.ID, data)}
}

func tick(on bool) string { return onOff(on, "✅ ", "⬜ ") }

func groupMap(grp *model.Group) map[string]any {
	b, _ := json.Marshal(grp)
	m := map[string]any{}
	json.Unmarshal(b, &m)
	return m
}

// 可以在按钮面板里直接开关的列（防止回调伪造改别的列）
var panelToggles = map[string]bool{
	"cas": true, "profile_check": true, "dup_check": true, "ai": true, "ai_all": true, "ai_image": true, "night_on": true,
	"clean_welcome": true, "points_on": true, "invite_on": true, "invite_notify": true, "usdt_on": true, "bio_scan": true,
	"lock_link": true, "lock_forward": true, "lock_button": true, "lock_via_bot": true, "lock_story": true, "lock_sticker": true,
	"lock_gif": true, "lock_media": true, "lock_voice": true, "lock_invite": true, "lock_contact": true, "lock_poll": true,
	"lock_game": true, "lock_foreign": true,
}

// 循环切换的选项：按一下换下一个值
var panelCycles = map[string]struct {
	col  string
	vals []any
}{
	"vm": {"verify_mode", []any{model.VerifyOff, model.VerifyJoinRequest, model.VerifyRestrict}},
	"vt": {"verify_timeout", []any{120, 300, 600, 1800}},
	"rd": {"raid_limit", []any{0, 10, 15, 30, 50}},
	"nb": {"newbie_msgs", []any{0, 3, 5, 10}},
	"nh": {"newbie_hours", []any{0, 1, 6, 24, 72}},
	"ax": {"ai_action", []any{model.ActionBan, model.ActionMute, "warn"}},
	"wl": {"warn_limit", []any{2, 3, 5}},
	"wa": {"warn_action", []any{model.ActionMute, model.ActionKick, model.ActionBan}},
	"we": {"warn_expire", []any{0, 24, 72, 168, 720}},
	"ra": {"report_auto", []any{0, 3, 5, 10}},
	"vk": {"vote_kick", []any{0, 3, 5, 10}},
	"wt": {"welcome_ttl", []any{0, 60, 120, 300, 3600}},
	"cp": {"checkin_points", []any{5, 10, 20, 50}},
	"sb": {"streak_bonus", []any{0, 1, 2, 5}},
	"mp": {"msg_points", []any{0, 1, 2}},
	"mc": {"msg_points_cap", []any{20, 50, 100, 200}},
	"rk": {"rank_post", []any{"", "daily", "weekly"}},
	"ip": {"invite_points", []any{0, 5, 10, 20, 50}},
	"lr": {"link_rotate", []any{0, 24, 72, 168}},
	"pm": {"panel_mode", []any{"", "list"}},
}

func nextVal(cur any, vals []any) any {
	cs := fmt.Sprint(cur)
	if cur == nil {
		cs = ""
	}
	for i, v := range vals {
		if fmt.Sprint(v) == cs || (cs == "<nil>" && fmt.Sprint(v) == "") {
			return vals[(i+1)%len(vals)]
		}
	}
	return vals[0]
}

func zeroOff(n int, f string) string {
	if n == 0 {
		return "关"
	}
	return fmt.Sprintf(f, n)
}

// 需要输入文字的设置：字段 → 提示
var panelEdits = map[string]string{
	"welcome":  "请发送新的<b>欢迎语</b>。\n支持粗体、链接、动态表情，可以直接发一张图片 / 动图 / 视频配文字。\n占位符：{name} 新人 {group} 群名 {username} {id}\n末尾每行「按钮文字 - https://链接」生成按钮。",
	"rules":    "请发送新的<b>群规</b>（支持格式和动态表情，末尾可以写按钮）。",
	"invtext":  "请发送成员领邀请链接时看到的<b>文案</b>（支持格式和动态表情，末尾可以写按钮）。\n占位符：{link} 链接 {count} 已邀请人数 {group} 群名 {name} 成员\n发 <code>默认</code> 恢复默认文案。",
	"night":    "请发送夜间时段，格式：<code>23:30 07:00</code>",
	"rankat":   "请发送每天发榜的时间，格式：<code>21:00</code>",
	"vtime":    "请发送验证超时秒数（30 ~ 86400），例如 <code>300</code>",
	"flood":    "请发送防刷屏规则：<code>条数 秒数</code>，例如 <code>6 10</code> 表示 10 秒内超过 6 条；发 <code>0</code> 关闭",
	"filter":   "请发送要添加的屏蔽词，一行一个。\n末尾加 <code> ban</code> / <code> mute</code> / <code> delete</code> 指定处理方式（默认删除并警告）；<code>re:</code> 开头表示正则。",
	"unfilter": "请发送要删除的屏蔽词，一行一个。",
	"send":     "请发送要以机器人身份发到群里的内容（文字 / 图片 / 动图都可以，格式和动态表情会保留）。",
	"padmins":  "请发送可以在私聊里管理本群的人：用户 ID 或 @用户名，空格或换行分隔。",
}

// ---------- 页面 ----------

var pageTitles = map[string]string{
	"home": "首页", "v": "🛡 入群验证", "sp": "🚫 反垃圾", "lk": "🔒 消息锁定", "wn": "⚠️ 警告与处罚", "wc": "👋 欢迎语与群规",
	"pt": "⭐ 积分签到", "iv": "🔗 邀请统计", "lt": "🎁 抽奖", "rp": "📮 举报", "st": "📊 统计", "tl": "🧹 工具", "pa": "👥 谁能管理",
	"fl": "🧾 屏蔽词", "tp": "🧵 话题",
}

// panelView 渲染一个页面
func (g *Guard) panelView(ctx context.Context, grp *model.Group, page string, uid int64, inGroup bool) (string, *models.InlineKeyboardMarkup) {
	b := func(t, d string) pbtn { return g.pb(grp, t, d) }
	act := func(t, a string) pbtn { return g.pb(grp, t, page+":"+a) }
	tg := func(label, col string, on bool) pbtn { return act(tick(on)+label, "t:"+col) }
	cy := func(label, key string) pbtn { return act(label, "c:"+key) }
	ed := func(label, field string) pbtn { return act("✏️ "+label, "e:"+field) }
	var rows [][]pbtn
	var head strings.Builder
	fmt.Fprintf(&head, "<b>%s</b>", esc(grp.Title))
	if page != "home" {
		fmt.Fprintf(&head, " › %s", pageTitles[page])
	}
	head.WriteString("\n")
	w := func(f string, a ...any) { fmt.Fprintf(&head, f, a...) }

	switch page {
	case "v":
		w("新人怎么进群。验证题在私聊里完成（「点按钮」在群里点）。\n")
		if grp.VerifyMode == model.VerifyJoinRequest {
			w("<i>入群申请模式需要在群设置里开启「申请入群需要批准」。</i>\n")
		}
		rows = [][]pbtn{
			{cy("方式："+verifyName(grp.VerifyMode), "vm")},
			{act("题目："+captchaName(grp.CaptchaType), "ct"), cy("超时："+humanDur(time.Duration(grp.VerifyTimeout)*time.Second), "vt")},
			{tg("CAS 黑名单", "cas", grp.CAS), tg("资料检查", "profile_check", grp.ProfileCheck)},
			{cy("防爆群："+zeroOff(grp.RaidLimit, "%d人/分"), "rd")},
		}
	case "sp":
		flood := "关"
		if grp.FloodCount > 0 {
			flood = fmt.Sprintf("%d秒%d条", grp.FloodSecond, grp.FloodCount)
		}
		w("对新人从严、老成员从宽。")
		if grp.AI && !g.ai.enabled() {
			w("\n⚠️ 还没配置 AI 接口，AI 审核不会生效。")
		}
		w("\n")
		rows = [][]pbtn{
			{cy("新人前 "+zeroOff(grp.NewbieMsgs, "%d 条")+" 严查", "nb"), cy("新人 "+zeroOff(grp.NewbieHours, "%d 小时")+" 只能发文字", "nh")},
			{ed("防刷屏："+flood, "flood"), tg("重复内容", "dup_check", grp.DupCheck)},
			{tg("AI 审核", "ai", grp.AI), tg("AI 审所有人", "ai_all", grp.AIAll)},
			{cy("AI 判定后："+aiActionName(grp.AIAction), "ax"), tg("AI 看图片", "ai_image", grp.AIImage)},
			{tg(fmt.Sprintf("夜间模式 %s-%s", grp.NightStart, grp.NightEnd), "night_on", grp.NightOn), ed("夜间时段", "night")},
			{b("🧾 屏蔽词", "fl"), b("🔒 消息锁定", "lk")},
		}
	case "lk":
		w("打开后普通成员不能发这类消息，发了删除并警告。\n")
		items := [][2]string{{"链接", "lock_link"}, {"转发", "lock_forward"}, {"按钮消息", "lock_button"}, {"inline 机器人", "lock_via_bot"},
			{"动态", "lock_story"}, {"贴纸", "lock_sticker"}, {"GIF", "lock_gif"}, {"图片视频文件", "lock_media"}, {"语音", "lock_voice"},
			{"邀请链接", "lock_invite"}, {"名片位置", "lock_contact"}, {"投票", "lock_poll"}, {"骰子游戏", "lock_game"}, {"外文", "lock_foreign"}}
		gm := groupMap(grp)
		var row []pbtn
		for _, it := range items {
			on, _ := gm[it[1]].(bool)
			row = append(row, tg(it[0], it[1], on))
			if len(row) == 2 {
				rows = append(rows, row)
				row = nil
			}
		}
		if row != nil {
			rows = append(rows, row)
		}
	case "fl":
		fs, _ := g.st.Filters(grp.ID)
		w("命中屏蔽词按设置处理。共 %d 条：\n", len(fs))
		for i, f := range fs {
			if i >= 30 {
				w("……（更多请在后台面板查看）\n")
				break
			}
			w("· <code>%s%s</code> %s\n", onOff(f.Regex, "re:", ""), esc(f.Pattern), map[string]string{"ban": "封禁", "mute": "禁言", "delete": "只删除"}[f.Action])
		}
		rows = [][]pbtn{{ed("添加", "filter"), ed("删除", "unfilter")}}
	case "wn":
		w("规则命中会删消息并警告一次，满上限按下面处理。\n")
		rows = [][]pbtn{
			{cy(fmt.Sprintf("警告上限：%d 次", grp.WarnLimit), "wl"), cy("满了："+actionName(grp.WarnAction), "wa")},
			{cy("警告有效期："+onOff(grp.WarnExpire == 0, "永久", fmt.Sprintf("%d 小时", grp.WarnExpire)), "we")},
			{cy("举报自动禁言："+zeroOff(grp.ReportAuto, "%d 人"), "ra"), cy("投票踢人："+zeroOff(grp.VoteKick, "%d 票"), "vk")},
		}
	case "wc":
		wel := "（未设置）"
		if grp.Welcome != "" {
			wel = esc(truncate(grp.Welcome, 120))
		}
		w("欢迎语：%s%s\n群规：%s\n", wel, onOff(grp.WelcomeMedia != "", "（带媒体）", ""), onOff(grp.Rules == "", "（未设置）", esc(truncate(grp.Rules, 60))))
		rows = [][]pbtn{
			{ed("修改欢迎语", "welcome"), act("👀 预览", "pw"), act("🗑 关闭", "cw")},
			{cy("自动删除："+zeroOff(grp.WelcomeTTL, "%d 秒"), "wt"), tg("只留最新一条", "clean_welcome", grp.CleanWelcome)},
			{ed("修改群规", "rules"), act("👀 预览群规", "pr")},
		}
	case "pt":
		rk := map[string]string{"": "关", "daily": "每天", "weekly": "每周日"}[grp.RankPost]
		w("群友发 /checkin 或「签到」得分，/me 看自己，/ptop 积分榜，/top 发言榜。积分可以作为抽奖门槛。\n")
		rows = [][]pbtn{
			{tg("开启积分签到", "points_on", grp.PointsOn)},
			{cy(fmt.Sprintf("签到 +%d", grp.CheckinPoints), "cp"), cy(fmt.Sprintf("连签每天再 +%d", grp.StreakBonus), "sb")},
			{cy("发言积分："+zeroOff(grp.MsgPoints, "+%d/条"), "mp"), cy(fmt.Sprintf("每天上限 %d", grp.MsgPointsCap), "mc")},
			{cy("自动发榜："+rk, "rk"), ed("发榜时间 "+grp.RankAt, "rankat")},
			{act("🏆 积分榜", "rank_p"), act("💬 发言榜", "rank_a")},
		}
	case "iv":
		w("成员在群里发 /link，或私聊机器人发「邀请链接」领取专属链接；私聊发「我邀请的人」看自己拉来的人。通过链接进群并完成验证才算数。/invites 看邀请榜。机器人需要「邀请用户」权限。\n")
		rows = [][]pbtn{
			{tg("开启邀请统计", "invite_on", grp.InviteOn)},
			{tg("有人通过验证时通知邀请人", "invite_notify", grp.InviteNotify)},
			{ed("✏️ 领链接时的文案", "invtext")},
			{cy("每邀请 1 人："+zeroOff(grp.InvitePoints, "+%d 积分"), "ip")},
			{act("🏆 邀请榜", "rank_i")},
		}
	case "lt":
		ls, _ := g.st.Lotteries(grp.ID)
		open := 0
		for _, l := range ls {
			if l.Status != model.LotteryOpen {
				continue
			}
			open++
			if open > 6 {
				continue
			}
			w("#%d %s · %d 人参与 · %s\n", l.ID, esc(l.Title), l.Entries, g.drawRule(&l))
			rows = append(rows, []pbtn{act(fmt.Sprintf("🎯 开奖 #%d", l.ID), fmt.Sprintf("ldq:%d", l.ID)), act(fmt.Sprintf("✖ 取消 #%d", l.ID), fmt.Sprintf("lcq:%d", l.ID))})
		}
		if open == 0 {
			w("没有进行中的抽奖。\n")
		}
		w("\n<i>发起抽奖（卡密 / 文件 / 多奖项 / 参与条件）请在后台面板操作。</i>\n")
	case "rp":
		rs, _ := g.st.Reports(grp.ID, model.ReportOpen, 6)
		if len(rs) == 0 {
			w("没有待处理的举报。\n")
		}
		for _, r := range rs {
			w("#%d %s：%s%s\n", r.ID, esc(r.Target), esc(truncate(r.Text, 50)), onOff(r.Count > 1, fmt.Sprintf("（%d 人举报）", r.Count), ""))
			rows = append(rows, []pbtn{act(fmt.Sprintf("🗑 #%d", r.ID), fmt.Sprintf("r:del:%d", r.ID)), act("🔇", fmt.Sprintf("r:mute:%d", r.ID)),
				act("⛔", fmt.Sprintf("r:ban:%d", r.ID)), act("忽略", fmt.Sprintf("r:ign:%d", r.ID))})
		}
	case "st":
		head.Reset()
		head.WriteString(g.statsText(grp) + "\n")
		rows = [][]pbtn{{act("💬 今日发言榜", "rank_a"), act("📅 近 7 天", "rank_w")}}
	case "tl":
		running, last := g.ZombieStatus(grp.ID)
		w("清理已注销账号：%s\n", onOff(running, "进行中…", onOff(last == "", "还没运行过", esc(last))))
		rows = [][]pbtn{
			{act("🧹 清理已注销账号", "z"), act("🔍 主页查重", "bs")},
			{act("🔄 立即换邀请链接", "rl"), cy("自动换链接："+zeroOff(grp.LinkRotate, "每 %d 小时"), "lr")},
			{ed("以机器人身份发消息", "send")},
			{tg("💱 @机器人 查 U 价", "usdt_on", grp.UsdtOn)},
			{b("🧵 话题设置", "tp")},
		}
	case "tp":
		ts, _ := g.st.Topics(grp.ID)
		name := func(id int) string {
			if id == 0 {
				return "General / 跟随"
			}
			for _, t := range ts {
				if t.ThreadID == id && t.Name != "" {
					return t.Name
				}
			}
			return fmt.Sprintf("#%d", id)
		}
		w("话题群专用。机器人见过的话题：%d 个（新话题在里面有人发言后出现）。\n", len(ts))
		rows = [][]pbtn{
			{act("欢迎语发到："+name(grp.WelcomeThread), "th:welcome_thread")},
			{act("机器人提示发到："+name(grp.BotThread), "th:bot_thread")},
		}
	case "pa":
		w("谁能在 Telegram 里管理本群（私聊面板、群里 /settings 按钮）。机器人主人总是可以。\n")
		if grp.PanelMode == "list" {
			w("当前：<b>只限下面勾选的人</b>\n")
		} else {
			w("当前：<b>所有有「封禁用户」权限的管理员</b>\n")
		}
		rows = append(rows, []pbtn{cy("切换："+onOff(grp.PanelMode == "list", "只限名单", "所有管理员"), "pm")})
		if grp.PanelMode == "list" {
			ids := ParseIDList(grp.PanelAdmins)
			for id, cm := range g.admins.get(ctx, grp.ID) {
				u := memberUser(cm)
				if u == nil || u.IsBot {
					continue
				}
				rows = append(rows, []pbtn{act(tick(slices.Contains(ids, id))+displayName(u), fmt.Sprintf("pa:%d", id))})
			}
			rows = append(rows, []pbtn{ed("按 ID / 用户名填写", "padmins")})
		}
	default: // home
		page = "home"
		w("%s · 题目：%s\n", verifyName(grp.VerifyMode), captchaName(grp.CaptchaType))
		w("AI 审核：%s · 防刷屏：%s · 夜间：%s\n", onOff(grp.AI && g.ai.enabled(), "开", "关"), onOff(grp.FloodCount > 0, "开", "关"), onOff(grp.NightOn, grp.NightStart+"-"+grp.NightEnd, "关"))
		if raidActive(grp) {
			w("🚨 防爆群生效中，暂时拒绝所有新人\n")
		}
		nrep, _ := g.st.Reports(grp.ID, model.ReportOpen, 50)
		rows = [][]pbtn{
			{b("🛡 入群验证", "v"), b("🚫 反垃圾", "sp")},
			{b("🔒 消息锁定", "lk"), b("⚠️ 警告与处罚", "wn")},
			{b("👋 欢迎语与群规", "wc"), b("⭐ 积分签到", "pt")},
			{b("🔗 邀请统计", "iv"), b("🎁 抽奖", "lt")},
			{b("📮 举报"+onOff(len(nrep) > 0, fmt.Sprintf("（%d）", len(nrep)), ""), "rp"), b("📊 统计", "st")},
			{b("🧹 工具", "tl"), b("👥 谁能管理", "pa")},
		}
	}
	// 底部导航
	var nav []pbtn
	if page != "home" {
		back := "home"
		if page == "fl" || page == "tp" {
			back = map[string]string{"fl": "sp", "tp": "tl"}[page]
		}
		nav = append(nav, b("⬅️ 返回", back))
	}
	nav = append(nav, b("🔄", page))
	if inGroup {
		nav = append(nav, pbtn{Text: "私聊打开", URL: fmt.Sprintf("https://t.me/%s?start=s%d", g.tg.Me().Username, grp.ID)}, b("✖ 关闭", "x"))
	} else if page == "home" {
		nav = append(nav, pbtn{Text: "📋 其它群", CallbackData: fmt.Sprintf("s:%d:list", grp.ID)})
		if u := g.miniAppURL(grp.ID); u != "" {
			rows = append(rows, []pbtn{{Text: "📱 完整面板（发起抽奖、编辑广告…）", WebApp: &models.WebAppInfo{URL: u}}})
		}
	}
	rows = append(rows, nav)
	return head.String(), &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}

// ---------- 回调 ----------

// onPanelCallback 处理面板按钮（自己应答 callback）
func (g *Guard) onPanelCallback(ctx context.Context, cq *models.CallbackQuery) {
	answer := func(text string, alert bool) {
		g.tg.AnswerCallbackQuery(ctx, &tgbot.AnswerCallbackQueryParams{CallbackQueryID: cq.ID, Text: text, ShowAlert: alert})
	}
	parts := strings.SplitN(cq.Data, ":", 4)
	if len(parts) < 3 {
		answer("", false)
		return
	}
	gid, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		answer("", false)
		return
	}
	grp, err := g.st.Group(gid, "")
	if err != nil {
		answer("读取失败", false)
		return
	}
	uid := cq.From.ID
	if !g.canPanel(ctx, grp, uid) {
		answer("你没有管理这个群的权限", true)
		return
	}
	page := parts[2]
	action := ""
	if len(parts) == 4 {
		action = parts[3]
	}
	msg := cq.Message.Message
	inGroup := msg != nil && msg.Chat.Type != models.ChatTypePrivate
	switch page {
	case "x":
		if msg != nil {
			g.tg.Delete(ctx, msg.Chat.ID, msg.ID)
		}
		answer("", false)
		return
	case "list":
		answer("", false)
		g.sendGroupPicker(ctx, uid, uid)
		return
	case "pa":
		if !g.canPanelAccess(ctx, grp, uid) {
			answer("只有群主或机器人主人可以设置", true)
			return
		}
	}
	tip, url := "", ""
	if action != "" {
		tip, url = g.panelAction(ctx, grp, page, action, uid, inGroup, msg)
		grp, _ = g.st.Group(gid, "")
	}
	if url != "" {
		g.tg.AnswerCallbackQuery(ctx, &tgbot.AnswerCallbackQueryParams{CallbackQueryID: cq.ID, URL: url})
	} else {
		answer(tip, strings.HasPrefix(tip, "!"))
	}
	if msg == nil {
		return
	}
	// 二次确认页面
	if strings.HasPrefix(action, "ldq:") || strings.HasPrefix(action, "lcq:") {
		id := action[4:]
		draw := action[1] == 'd'
		text := fmt.Sprintf("<b>%s</b>\n确定要%s抽奖 #%s 吗？", esc(grp.Title), onOff(draw, "立即开奖", "取消"), id)
		mk := &models.InlineKeyboardMarkup{InlineKeyboard: [][]pbtn{{
			g.pb(grp, onOff(draw, "✅ 确定开奖", "✅ 确定取消"), "lt:"+onOff(draw, "ld:", "lc:")+id), g.pb(grp, "⬅️ 返回", "lt"),
		}}}
		g.tg.EditMessageText(ctx, &tgbot.EditMessageTextParams{ChatID: msg.Chat.ID, MessageID: msg.ID, Text: text, ParseMode: models.ParseModeHTML, ReplyMarkup: mk})
		return
	}
	text, mk := g.panelView(ctx, grp, page, uid, inGroup)
	if extra, ok := panelExtra.LoadAndDelete(uid); ok {
		text += "\n" + extra.(string)
	}
	if _, err := g.tg.EditMessageText(ctx, &tgbot.EditMessageTextParams{ChatID: msg.Chat.ID, MessageID: msg.ID, Text: text, ParseMode: models.ParseModeHTML,
		ReplyMarkup: mk, LinkPreviewOptions: &models.LinkPreviewOptions{IsDisabled: tgbot.True()}}); err != nil && !strings.Contains(err.Error(), "not modified") {
		// 旧消息是图片等不能编辑成文字的，直接发一条新的
		g.tg.Notify(ctx, msg.Chat.ID, text, mk, 0)
	}
}

// panelExtra 动作结果附在页面下方显示一次（排行榜、统计等）
var panelExtra sync.Map // uid -> string

// panelAction 执行按钮动作，返回提示（! 开头弹窗）和要跳转的链接
func (g *Guard) panelAction(ctx context.Context, grp *model.Group, page, action string, uid int64, inGroup bool, msg *models.Message) (string, string) {
	kind, arg, _ := strings.Cut(action, ":")
	save := func(fields map[string]any) string {
		if err := g.st.UpdateGroup(grp.ID, fields); err != nil {
			return "!保存失败"
		}
		return "已保存"
	}
	switch kind {
	case "t":
		if !panelToggles[arg] {
			return "", ""
		}
		on, _ := groupMap(grp)[arg].(bool)
		if arg == "ai" && !on && !g.ai.enabled() {
			return "!还没配置 AI 接口，请先在后台面板「AI 接口」里填写", ""
		}
		if arg == "invite_on" || arg == "points_on" {
			defer g.syncCommandsLater()
		}
		return save(map[string]any{arg: !on}), ""
	case "c":
		c, ok := panelCycles[arg]
		if !ok {
			return "", ""
		}
		return save(map[string]any{c.col: nextVal(groupMap(grp)[c.col], c.vals)}), ""
	case "ct":
		types := []string{model.CaptchaImage, model.CaptchaMath, model.CaptchaEmoji, model.CaptchaButton}
		if g.captcha != nil {
			types = append(types, model.CaptchaWeb)
		}
		return save(map[string]any{"captcha_type": cycle(grp.CaptchaType, types...)}), ""
	case "th":
		if arg != "welcome_thread" && arg != "bot_thread" {
			return "", ""
		}
		ts, _ := g.st.Topics(grp.ID)
		vals := []any{0}
		for _, t := range ts {
			if !t.Closed {
				vals = append(vals, t.ThreadID)
			}
		}
		if len(vals) == 1 {
			return "!还没见过任何话题。在想要的话题里发一条消息，再回来选。", ""
		}
		return save(map[string]any{arg: nextVal(groupMap(grp)[arg], vals)}), ""
	case "e":
		if _, ok := panelEdits[arg]; !ok {
			return "", ""
		}
		if inGroup { // 群里不能等输入：跳到私聊
			return "", fmt.Sprintf("https://t.me/%s?start=pe_%d_%s", g.tg.Me().Username, grp.ID, arg)
		}
		g.askEdit(ctx, uid, grp, arg)
		return "请在下面发送", ""
	case "cw":
		return save(map[string]any{"welcome": "", "welcome_media": ""}), ""
	case "pw":
		u, _ := g.st.UserByID(uid)
		user := &models.User{ID: uid}
		if u != nil {
			user = &models.User{ID: u.ID, FirstName: u.FirstName, LastName: u.LastName, Username: u.Username}
		}
		if grp.Welcome == "" && grp.WelcomeMedia == "" {
			return "!还没设置欢迎语", ""
		}
		g.previewWelcome(ctx, grp, user, uid)
		return "预览已私聊发给你", ""
	case "pr":
		if grp.Rules == "" {
			return "!还没设置群规", ""
		}
		body, mk := parseButtons(grp.Rules)
		var markup models.ReplyMarkup
		if mk != nil {
			markup = mk
		}
		g.sendRich(ctx, uid, 0, "<b>群规</b>\n"+MarkdownToHTML(body), "", markup, 0)
		return "预览已私聊发给你", ""
	case "rank_p":
		panelExtra.Store(uid, g.PointsRankText(grp.ID))
	case "rank_a":
		panelExtra.Store(uid, g.ActivityRankText(grp.ID, false))
	case "rank_w":
		panelExtra.Store(uid, g.ActivityRankText(grp.ID, true))
	case "rank_i":
		panelExtra.Store(uid, g.InviteRankText(grp.ID))
	case "ld", "lc":
		id, _ := strconv.ParseUint(arg, 10, 32)
		l, _ := g.st.Lottery(uint(id))
		if l == nil || l.GroupID != grp.ID {
			return "!抽奖不存在", ""
		}
		if kind == "ld" {
			n, err := g.DrawLottery(ctx, l.ID)
			if err != nil {
				return "!" + err.Error(), ""
			}
			return fmt.Sprintf("已开奖，%d 人中奖", n), ""
		}
		if err := g.CancelLottery(ctx, l.ID); err != nil {
			return "!" + err.Error(), ""
		}
		return "已取消", ""
	case "ldq", "lcq":
		return "", ""
	case "r":
		act, id, _ := strings.Cut(arg, ":")
		rid, _ := strconv.ParseUint(id, 10, 32)
		r, _ := g.st.Report(uint(rid))
		if r == nil || r.GroupID != grp.ID {
			return "!举报不存在", ""
		}
		name := "管理员"
		if u, _ := g.st.UserByID(uid); u != nil {
			name = strings.TrimSpace(u.FirstName + " " + u.LastName)
		}
		text, err := g.HandleReport(ctx, uint(rid), act, name)
		if err != nil {
			return "!" + err.Error(), ""
		}
		return text, ""
	case "z":
		if !g.StartZombieScan(grp.ID, false) {
			return "!正在清理中", ""
		}
		return "已开始，完成后结果写进日志", ""
	case "bs":
		if !g.StartBioScan(grp.ID, uid) {
			return "!已有查重在进行中", ""
		}
		return "已开始主页查重，结果稍后发给你", ""
	case "rl":
		if _, err := g.RotateLink(ctx, grp.ID); err != nil {
			return "!更换失败：机器人需要「邀请用户」权限", ""
		}
		return "已更换，新链接写进日志", ""
	case "pa":
		id, _ := strconv.ParseInt(arg, 10, 64)
		ids := ParseIDList(grp.PanelAdmins)
		if i := slices.Index(ids, id); i >= 0 {
			ids = slices.Delete(ids, i, i+1)
		} else {
			ids = append(ids, id)
		}
		var ss []string
		for _, x := range ids {
			ss = append(ss, strconv.FormatInt(x, 10))
		}
		return save(map[string]any{"panel_admins": strings.Join(ss, " ")}), ""
	}
	return "", ""
}

// syncCommandsLater 积分 / 邀请开关变了，命令菜单不用改（菜单是全局的），留作扩展
func (g *Guard) syncCommandsLater() {}

func (g *Guard) previewWelcome(ctx context.Context, grp *model.Group, u *models.User, to int64) {
	body, markup := parseButtons(grp.Welcome)
	uname := ""
	if u.Username != "" {
		uname = "@" + esc(u.Username)
	}
	text := strings.NewReplacer("{name}", mention(u), "{group}", esc(grp.Title), "{id}", strconv.FormatInt(u.ID, 10),
		"{username}", uname, "{first}", esc(u.FirstName)).Replace(MarkdownToHTML(body))
	var mk models.ReplyMarkup
	if markup != nil {
		mk = markup
	}
	if _, err := g.sendRich(ctx, to, 0, text, grp.WelcomeMedia, mk, 0); err != nil {
		g.tg.Notify(ctx, to, "预览失败："+esc(err.Error()), nil, 0)
	}
}

// ---------- 私聊输入 ----------

func (g *Guard) askEdit(ctx context.Context, uid int64, grp *model.Group, field string) {
	setWait(uid, pmWait{kind: "edit", gid: grp.ID, field: field})
	g.tg.Notify(ctx, uid, fmt.Sprintf("「%s」\n%s\n\n发 /cancel 取消。", esc(grp.Title), panelEdits[field]), nil, 0)
}

var hmPairRe = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d\s+([01]\d|2[0-3]):[0-5]\d$`)

// applyEdit 私聊收到要修改的内容
func (g *Guard) applyEdit(ctx context.Context, m *models.Message, w pmWait) {
	grp, err := g.st.Group(w.gid, "")
	if err != nil || !g.canPanel(ctx, grp, m.From.ID) {
		g.tg.Notify(ctx, m.Chat.ID, "你没有管理这个群的权限。", nil, 0)
		return
	}
	text := strings.TrimSpace(m.Text)
	again := func(msg string) {
		g.tg.Notify(ctx, m.Chat.ID, msg+"\n请重新发送，或 /cancel 取消。", nil, 0)
		setWait(m.From.ID, w)
	}
	set := func(fields map[string]any) bool {
		if err := g.st.UpdateGroup(grp.ID, fields); err != nil {
			g.tg.Notify(ctx, m.Chat.ID, "保存失败："+esc(err.Error()), nil, 0)
			return false
		}
		return true
	}
	back := map[string]string{"welcome": "wc", "rules": "wc", "night": "sp", "rankat": "pt", "vtime": "v", "flood": "sp",
		"filter": "fl", "unfilter": "fl", "send": "tl", "padmins": "pa"}[w.field]
	done := "✅ 已保存"
	switch w.field {
	case "welcome":
		md, media := MessageMarkdown(m), MediaOf(m)
		if md == "" && media == "" {
			again("没有收到内容。")
			return
		}
		if !set(map[string]any{"welcome": md, "welcome_media": media}) {
			return
		}
		grp.Welcome, grp.WelcomeMedia = md, media
		g.previewWelcome(ctx, grp, m.From, m.Chat.ID)
		done = "✅ 欢迎语已保存，上面是预览"
	case "invtext":
		md := MessageMarkdown(m)
		if strings.TrimSpace(md) == "默认" {
			md = ""
		}
		if !set(map[string]any{"invite_text": md}) {
			return
		}
		done = "✅ 邀请文案已保存"
	case "rules":
		md := MessageMarkdown(m)
		if md == "" {
			again("群规需要文字。")
			return
		}
		if !set(map[string]any{"rules": md}) {
			return
		}
	case "night":
		if !hmPairRe.MatchString(text) {
			again("格式不对，例如 <code>23:30 07:00</code>。")
			return
		}
		f := strings.Fields(text)
		if f[0] == f[1] {
			again("开始和结束不能相同。")
			return
		}
		set(map[string]any{"night_start": f[0], "night_end": f[1]})
	case "rankat":
		if !hmRe.MatchString(text) {
			again("格式不对，例如 <code>21:00</code>。")
			return
		}
		set(map[string]any{"rank_at": text})
	case "vtime":
		n, err := strconv.Atoi(text)
		if err != nil || n < 30 || n > 86400 {
			again("需要 30 ~ 86400 之间的秒数。")
			return
		}
		set(map[string]any{"verify_timeout": n})
	case "flood":
		if text == "0" {
			set(map[string]any{"flood_count": 0})
			break
		}
		var c, s int
		if _, err := fmt.Sscan(text, &c, &s); err != nil || c < 2 || c > 100 || s < 1 || s > 600 {
			again("格式：<code>条数 秒数</code>，例如 <code>6 10</code>。")
			return
		}
		set(map[string]any{"flood_count": c, "flood_second": s})
	case "filter", "unfilter":
		n := 0
		for _, line := range strings.Split(text, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if w.field == "unfilter" {
				if ok, _ := g.st.DelFilter(grp.ID, strings.TrimPrefix(line, "re:")); ok {
					n++
				}
				continue
			}
			f := &model.Filter{GroupID: grp.ID, Pattern: line, Action: "warn"}
			if i := strings.LastIndex(line, " "); i > 0 {
				if a := line[i+1:]; a == "ban" || a == "mute" || a == "delete" || a == "warn" {
					f.Pattern, f.Action = strings.TrimSpace(line[:i]), a
				}
			}
			if p, ok := strings.CutPrefix(f.Pattern, "re:"); ok {
				if _, err := regexp.Compile(p); err != nil {
					g.tg.Notify(ctx, m.Chat.ID, "跳过无效正则："+esc(p), nil, 0)
					continue
				}
				f.Pattern, f.Regex = p, true
			} else {
				f.Pattern = strings.ToLower(f.Pattern)
			}
			if g.st.AddFilter(f) == nil {
				n++
			}
		}
		done = fmt.Sprintf("✅ %s %d 条", onOff(w.field == "filter", "已添加", "已删除"), n)
	case "send":
		md, media := MessageMarkdown(m), MediaOf(m)
		if _, err := g.sendRich(ctx, grp.ID, grp.BotThread, MarkdownToHTML(md), media, nil, 0); err != nil {
			g.tg.Notify(ctx, m.Chat.ID, "发送失败："+esc(err.Error()), nil, 0)
			return
		}
		done = "✅ 已发到群里"
	case "padmins":
		if !g.canPanelAccess(ctx, grp, m.From.ID) {
			g.tg.Notify(ctx, m.Chat.ID, "只有群主或机器人主人可以设置。", nil, 0)
			return
		}
		norm, err := g.NormalizeWhitelist(text)
		if err != nil {
			again(esc(err.Error()))
			return
		}
		set(map[string]any{"panel_admins": norm, "panel_mode": "list"})
	}
	g.tg.Notify(ctx, m.Chat.ID, done, nil, 0)
	g.sendPanelPage(ctx, m.Chat.ID, grp.ID, back, m.From.ID)
}

// cycle 在候选值里取下一个
func cycle[T comparable](cur T, opts ...T) T {
	for i, o := range opts {
		if o == cur {
			return opts[(i+1)%len(opts)]
		}
	}
	return opts[0]
}

func aiActionName(a string) string {
	switch a {
	case model.ActionMute:
		return "禁言"
	case "warn":
		return "删除+警告"
	}
	return "封禁"
}

func lockValue(grp *model.Group, name string) bool {
	on, _ := groupMap(grp)[lockColumns[name]].(bool)
	return on
}

// CanPanel / CanPanelAccess 给 Mini App 接口用
func (g *Guard) CanPanel(ctx context.Context, grp *model.Group, uid int64) bool {
	return g.canPanel(ctx, grp, uid)
}

func (g *Guard) CanPanelAccess(ctx context.Context, grp *model.Group, uid int64) bool {
	return g.canPanelAccess(ctx, grp, uid)
}

// miniAppURL 完整面板地址（没配置 web.public_url 时为空）
func (g *Guard) miniAppURL(gid int64) string {
	if g.cfg.Web.PublicURL == "" {
		return ""
	}
	u := g.cfg.Web.PublicURL + "/app/"
	if gid != 0 {
		u += "?gid=" + strconv.FormatInt(gid, 10)
	}
	return u
}

// setPanelMenu 给能管理群的人在私聊里放一个「管理面板」菜单按钮（每人只设一次）
func (g *Guard) setPanelMenu(ctx context.Context, uid int64) {
	u := g.miniAppURL(0)
	if u == "" {
		return
	}
	if _, done := menuSet.LoadOrStore(uid, true); done {
		return
	}
	if _, err := g.tg.SetChatMenuButton(ctx, &tgbot.SetChatMenuButtonParams{ChatID: uid,
		MenuButton: models.MenuButtonWebApp{Type: models.MenuButtonTypeWebApp, Text: "管理面板", WebApp: models.WebAppInfo{URL: u}}}); err != nil {
		menuSet.Delete(uid)
	}
}

var menuSet sync.Map
