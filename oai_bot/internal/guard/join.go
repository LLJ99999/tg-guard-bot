package guard

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"oai_bot/internal/model"
)

var (
	ErrNoPending = errors.New("没有待完成的验证，可能已超时，请重新申请入群")
)

// riskCheck 入群前的风控：全局封禁 / CAS 黑名单 / 昵称带广告。返回拦截原因，空表示放行
func (g *Guard) riskCheck(ctx context.Context, grp *model.Group, u *models.User, bio string) string {
	if g.st.GBanned(u.ID) {
		return "全局封禁"
	}
	if b := g.st.FedBanned(grp.FedID, u.ID); b != nil {
		return "联邦封禁" + onOff(b.Reason != "", "："+b.Reason, "")
	}
	if grp.CAS && g.cas.banned(ctx, u.ID) {
		return "CAS 黑名单"
	}
	if !grp.ProfileCheck {
		return ""
	}
	words := g.filterWords(grp.ID)
	if spamName(u.FirstName+" "+u.LastName, words) || spamWord(u.FirstName+u.LastName) {
		return "昵称含广告"
	}
	// 简介里放个人频道链接很常见，只在出现引流词 / 屏蔽词时拦截
	if bio != "" && (spamWord(bio) || containsAny(strings.ToLower(bio), words)) {
		return "简介含广告"
	}
	return ""
}

// filterWords 本群的普通屏蔽词（昵称检查用）
func (g *Guard) filterWords(groupID int64) []string {
	fs, _ := g.st.Filters(groupID)
	var ws []string
	for _, f := range fs {
		if !f.Regex {
			ws = append(ws, strings.ToLower(f.Pattern))
		}
	}
	return ws
}

// spamName 昵称里带链接、@用户名或屏蔽词
func spamName(name string, words []string) bool {
	if linkRe.MatchString(name) || strings.Contains(name, "@") {
		return true
	}
	low := strings.ToLower(name)
	for _, w := range words {
		if w != "" && strings.Contains(low, w) {
			return true
		}
	}
	return false
}

// onJoinRequest 「入群需审核」的群收到申请：风控 → 私聊发 Mini App 验证 → 通过后批准
func (g *Guard) onJoinRequest(ctx context.Context, r *models.ChatJoinRequest) {
	grp, err := g.st.Group(r.Chat.ID, r.Chat.Title)
	if err != nil {
		slog.Error("group", "chat", r.Chat.ID, "err", err)
		return
	}
	g.st.SaveUser(r.From.ID, r.From.Username, r.From.FirstName, r.From.LastName)

	g.count(r.Chat.ID, "joins")
	if g.checkRaid(ctx, grp) {
		g.rejectRaider(ctx, r.Chat.ID, r.From.ID, true)
		return
	}
	if reason := g.riskCheck(ctx, grp, &r.From, r.Bio); reason != "" {
		g.tg.DeclineChatJoinRequest(ctx, &tgbot.DeclineChatJoinRequestParams{ChatID: r.Chat.ID, UserID: r.From.ID})
		g.logf(ctx, "拒绝入群申请 %s（%d）\n群：%s\n原因：%s", mention(&r.From), r.From.ID, esc(r.Chat.Title), reason)
		return
	}
	if grp.VerifyMode != model.VerifyJoinRequest || !g.canVerify(grp) {
		return // 未启用机器人审核：留给管理员手动处理
	}
	timeout := time.Duration(grp.VerifyTimeout) * time.Second
	p := &model.Pending{GroupID: r.Chat.ID, UserID: r.From.ID, Mode: model.VerifyJoinRequest, ExpiresAt: time.Now().Add(timeout)}
	if old, _ := g.st.Pending(r.Chat.ID, r.From.ID); old != nil {
		p.ChalMsg = old.ChalMsg // 重复申请：替换旧题
	}
	if err := g.st.SavePending(p); err != nil {
		slog.Error("save pending", "err", err)
		return
	}
	// 收到入群申请后，机器人可以主动私聊申请人（user_chat_id）
	g.sendChallenge(ctx, r.UserChatID, grp, p, fmt.Sprintf("你申请加入 <b>%s</b>，完成验证后会自动批准。", esc(r.Chat.Title)))
}

// onChatMember 成员状态变化：新人进群（restrict 模式禁言并要求验证）、管理员变动
func (g *Guard) onChatMember(ctx context.Context, u *models.ChatMemberUpdated) {
	if u.OldChatMember.Type == models.ChatMemberTypeAdministrator || u.NewChatMember.Type == models.ChatMemberTypeAdministrator ||
		u.OldChatMember.Type == models.ChatMemberTypeOwner || u.NewChatMember.Type == models.ChatMemberTypeOwner {
		g.admins.invalidate(u.Chat.ID)
	}
	joined := !isIn(u.OldChatMember) && isIn(u.NewChatMember)
	user := memberUser(u.NewChatMember)
	if isIn(u.OldChatMember) && !isIn(u.NewChatMember) && user != nil {
		g.st.UpdateMember(u.Chat.ID, user.ID, map[string]any{"has_left": true})
		return
	}
	if !joined || user == nil || user.IsBot && user.ID == g.tg.Me().ID {
		return
	}
	grp, err := g.st.Group(u.Chat.ID, u.Chat.Title)
	if err != nil {
		return
	}
	g.st.SaveUser(user.ID, user.Username, user.FirstName, user.LastName)
	g.st.Member(u.Chat.ID, user.ID)
	// 重新进群按新人计算（严格检查前 N 条），警告次数保留，防止退群重进刷掉警告
	g.st.UpdateMember(u.Chat.ID, user.ID, map[string]any{"joined_at": time.Now(), "msg_count": 0, "newcomer": true, "has_left": false})
	g.queueBio(u.Chat.ID, user.ID)

	// 管理员拉进来的人 / 机器人自己批准的申请，视为已验证
	addedByAdmin := u.From.ID != user.ID && g.admins.isAdmin(ctx, u.Chat.ID, u.From.ID)
	if addedByAdmin || u.ViaJoinRequest {
		g.st.UpdateMember(u.Chat.ID, user.ID, map[string]any{"verified": true})
		g.sendWelcome(ctx, grp, user)
		return
	}
	g.count(u.Chat.ID, "joins")
	if g.checkRaid(ctx, grp) {
		g.rejectRaider(ctx, u.Chat.ID, user.ID, false)
		return
	}
	if user.IsBot {
		// 普通成员拉机器人进群：直接踢掉（广告机器人常见手法）
		g.note(u.Chat.ID, user.ID, punNote{Source: "join", Reason: "非管理员拉入的机器人", Name: "@" + user.Username})
		g.tg.Kick(ctx, u.Chat.ID, user.ID)
		g.logf(ctx, "踢出非管理员拉入的机器人 @%s，群：%s", esc(user.Username), esc(u.Chat.Title))
		return
	}
	if reason := g.riskCheck(ctx, grp, user, ""); reason != "" {
		g.note(u.Chat.ID, user.ID, punNote{Source: "join", Reason: stripHTML(reason), Name: displayName(user)})
		g.tg.Ban(ctx, u.Chat.ID, user.ID, 0, true)
		g.logf(ctx, "封禁新成员 %s（%d）\n群：%s\n原因：%s", mention(user), user.ID, esc(u.Chat.Title), reason)
		return
	}
	// join 模式下，有人通过不需要审核的邀请链接直接进群，也要验证，否则等于没开验证
	if !needsJoinVerify(grp.VerifyMode) || !g.canVerify(grp) {
		g.sendWelcome(ctx, grp, user)
		return
	}
	if err := g.tg.Mute(ctx, u.Chat.ID, user.ID, 0); err != nil {
		slog.Warn("mute newcomer", "chat", u.Chat.ID, "err", err)
		return
	}
	timeout := time.Duration(grp.VerifyTimeout) * time.Second
	admin := []models.InlineKeyboardButton{
		{Text: "放行（管理员）", CallbackData: fmt.Sprintf("pass:%d:%d", u.Chat.ID, user.ID)},
		{Text: "踢出（管理员）", CallbackData: fmt.Sprintf("kick:%d:%d", u.Chat.ID, user.ID)},
	}
	var first models.InlineKeyboardButton
	if grp.CaptchaType == model.CaptchaButton {
		// 按钮验证直接在群里点，不用跳私聊
		first = models.InlineKeyboardButton{Text: "我不是机器人（点我）", CallbackData: fmt.Sprintf("vb:%d:%d", u.Chat.ID, user.ID)}
	} else {
		first = models.InlineKeyboardButton{Text: "点我开始验证", URL: fmt.Sprintf("https://t.me/%s?start=v%d", g.tg.Me().Username, u.Chat.ID)}
	}
	prompt := g.tg.Notify(ctx, u.Chat.ID,
		fmt.Sprintf("欢迎 %s！请在 %s 内完成验证（%s），否则会被移出群。", mention(user), humanDur(timeout), captchaName(grp.CaptchaType)),
		&models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{first}, admin}}, 0)
	p := &model.Pending{GroupID: u.Chat.ID, UserID: user.ID, Mode: model.VerifyRestrict, ExpiresAt: time.Now().Add(timeout)}
	if prompt != nil {
		p.PromptMsg = prompt.ID
	}
	g.st.SavePending(p)
}

// verifyButton 私聊里打开 Mini App 验证页的按钮（web_app 按钮只能用在私聊）
func (g *Guard) verifyButton(groupID int64) *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{
		{Text: "开始验证", WebApp: &models.WebAppInfo{URL: fmt.Sprintf("%s%s?g=%d", g.cfg.Web.PublicURL, VerifyPath, groupID)}},
	}}}
}

// onPrivate 私聊：/start v<群ID> 来自群里的验证按钮
func (g *Guard) onPrivate(ctx context.Context, m *models.Message) {
	g.st.SaveUser(m.From.ID, m.From.Username, m.From.FirstName, m.From.LastName)
	name, arg := parseCommand(m.Text, g.tg.Me().Username)
	if name == "settings" {
		g.sendGroupPicker(ctx, m.Chat.ID, m.From.ID)
		return
	}
	if name != "start" && name != "help" {
		return
	}
	if strings.HasPrefix(arg, "s") {
		if gid, err := strconv.ParseInt(arg[1:], 10, 64); err == nil {
			if g.cfg.IsOwner(m.From.ID) || g.admins.canRestrict(ctx, gid, m.From.ID) {
				g.sendPanel(ctx, m.Chat.ID, gid)
			} else {
				g.tg.Notify(ctx, m.Chat.ID, "你不是该群有封禁权限的管理员。", nil, 0)
			}
			return
		}
	}
	if strings.HasPrefix(arg, "v") {
		if gid, err := strconv.ParseInt(arg[1:], 10, 64); err == nil {
			if g.startChallenge(ctx, m.Chat.ID, gid, "") {
				return
			}
			g.tg.Notify(ctx, m.Chat.ID, "你在该群没有待完成的验证（可能已经通过或已超时）。", nil, 0)
			return
		}
	}
	if ps, _ := g.st.PendingsOfUser(m.From.ID); len(ps) > 0 && g.startChallenge(ctx, m.Chat.ID, ps[0].GroupID, "你有待完成的入群验证：") {
		return
	}
	g.tg.Notify(ctx, m.Chat.ID, helpText, nil, 0)
	if name == "start" && len(g.panelGroups(ctx, m.From.ID)) > 0 {
		g.sendGroupPicker(ctx, m.Chat.ID, m.From.ID) // 管理员私聊 /start 直接给出群列表
	}
}

// OnVerified 验证页校验通过后调用（HTTP 协程），实际处理放进该成员的队列
func (g *Guard) OnVerified(ctx context.Context, groupID, userID int64) error {
	p, err := g.st.Pending(groupID, userID)
	if err != nil {
		return err
	}
	if p == nil {
		return ErrNoPending
	}
	bg := context.WithoutCancel(ctx)
	g.q.Do(memberKey(groupID, userID), func() { g.pass(bg, groupID, userID, "验证通过") })
	return nil
}

// HasPending 验证接口在调用验证码服务前先确认确实有待验证记录
func (g *Guard) HasPending(groupID, userID int64) bool {
	p, _ := g.st.Pending(groupID, userID)
	return p != nil
}

// pass 放行：批准申请或解除禁言，发送欢迎语
func (g *Guard) pass(ctx context.Context, groupID, userID int64, how string) {
	p, err := g.st.TakePending(groupID, userID)
	if err != nil || p == nil {
		return // 已经处理过（超时 / 管理员操作）
	}
	grp, err := g.st.Group(groupID, "")
	if err != nil {
		return
	}
	if p.ChalMsg != 0 {
		g.tg.Delete(ctx, userID, p.ChalMsg)
	}
	switch p.Mode {
	case model.VerifyJoinRequest:
		if _, err := g.tg.ApproveChatJoinRequest(ctx, &tgbot.ApproveChatJoinRequestParams{ChatID: groupID, UserID: userID}); err != nil {
			slog.Warn("approve join", "chat", groupID, "user", userID, "err", err)
			g.tg.Notify(ctx, userID, "批准入群失败，申请可能已被处理或已过期，请重新申请。", nil, 0)
			return
		}
		g.tg.Notify(ctx, userID, fmt.Sprintf("验证通过，已批准加入 <b>%s</b>。", esc(grp.Title)), nil, 0)
	case model.VerifyRestrict:
		if err := g.tg.Unmute(ctx, groupID, userID); err != nil {
			slog.Warn("unmute", "chat", groupID, "user", userID, "err", err)
		}
		if p.PromptMsg != 0 {
			g.tg.Delete(ctx, groupID, p.PromptMsg)
		}
		g.tg.Notify(ctx, userID, "验证通过，可以在群里发言了。", nil, 0)
	}
	now := time.Now()
	g.count(groupID, "verified")
	g.st.Member(groupID, userID)
	g.st.UpdateMember(groupID, userID, map[string]any{"verified": true, "verified_at": now})
	u, _ := g.st.UserByID(userID)
	var user *models.User
	if u != nil {
		user = &models.User{ID: u.ID, FirstName: u.FirstName, LastName: u.LastName, Username: u.Username}
	} else {
		user = &models.User{ID: userID}
	}
	if p.Mode == model.VerifyRestrict {
		g.sendWelcome(ctx, grp, user) // join 模式的欢迎语在进群（chat_member）时发
	}
	g.logf(ctx, "%s：%s（%d），群：%s", how, mention(user), userID, esc(grp.Title))
}

// expire 超时未验证：拒绝申请或踢出
func (g *Guard) expire(ctx context.Context, groupID, userID int64) {
	p, err := g.st.TakePending(groupID, userID)
	if err != nil || p == nil || time.Now().Before(p.ExpiresAt) {
		if p != nil { // 期间被重新申请延长了时间，放回去
			g.st.SavePending(p)
		}
		return
	}
	if p.ChalMsg != 0 {
		g.tg.Delete(ctx, userID, p.ChalMsg)
	}
	switch p.Mode {
	case model.VerifyJoinRequest:
		g.tg.DeclineChatJoinRequest(ctx, &tgbot.DeclineChatJoinRequestParams{ChatID: groupID, UserID: userID})
		g.tg.Notify(ctx, userID, "验证超时，入群申请已被拒绝，可以重新申请。", nil, 0)
	case model.VerifyRestrict:
		g.tg.Kick(ctx, groupID, userID)
		if p.PromptMsg != 0 {
			g.tg.Delete(ctx, groupID, p.PromptMsg)
		}
	}
	g.logf(ctx, "验证超时：%s，群 %d", mentionID(userID, ""), groupID)
}

func (g *Guard) sendWelcome(ctx context.Context, grp *model.Group, u *models.User) {
	if grp.Welcome == "" {
		return
	}
	body, markup := parseButtons(grp.Welcome)
	// esc 会把 {name} 里的 HTML 一起转义，所以先转义模板再替换
	text := strings.NewReplacer("{name}", mention(u), "{group}", esc(grp.Title), "{id}", strconv.FormatInt(u.ID, 10)).Replace(esc(body))
	var mk models.ReplyMarkup
	if markup != nil {
		mk = markup
	}
	msg := g.tg.Notify(ctx, grp.ID, text, mk, time.Duration(grp.WelcomeTTL)*time.Second)
	if msg == nil {
		return
	}
	if grp.CleanWelcome && grp.LastWelcome != 0 && grp.LastWelcome != msg.ID {
		g.tg.Delete(ctx, grp.ID, grp.LastWelcome)
	}
	g.st.UpdateGroup(grp.ID, map[string]any{"last_welcome": msg.ID})
}

// parseButtons 从欢迎语 / 规则末尾解析按钮：每行「按钮文字 - https://链接」，同一行用 | 分隔多个按钮
func parseButtons(s string) (string, *models.InlineKeyboardMarkup) {
	lines := strings.Split(strings.TrimRight(s, "\n "), "\n")
	var rows [][]models.InlineKeyboardButton
	i := len(lines)
	for i > 0 {
		var row []models.InlineKeyboardButton
		for _, part := range strings.Split(lines[i-1], "|") {
			text, url, ok := strings.Cut(part, " - ")
			text, url = strings.TrimSpace(text), strings.TrimSpace(url)
			if !ok || text == "" || !(strings.HasPrefix(url, "https://") || strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "tg://")) {
				row = nil
				break
			}
			row = append(row, models.InlineKeyboardButton{Text: text, URL: url})
		}
		if row == nil {
			break
		}
		rows = append([][]models.InlineKeyboardButton{row}, rows...)
		i--
	}
	if len(rows) == 0 {
		return s, nil
	}
	return strings.TrimRight(strings.Join(lines[:i], "\n"), "\n "), &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}

// 昵称 / 简介里常见的引流词（只用于入群资料检查，故意保守，避免误杀）
var profileSpamWords = []string{"加v", "加微", "vx", "wx:", "微信号", "私聊我", "私我", "看我简介", "看简介", "看主页", "日结", "兼职", "代开", "博彩",
	"娱乐城", "棋牌", "引流", "接单", "返佣", "返利", "跑分", "搬砖", "带单", "喊单", "空投", "招代理", "招商", "裸聊", "约炮", "同城约", "福利姬", "接码", "实名号"}

func spamWord(s string) bool {
	low := strings.ToLower(strings.ReplaceAll(s, " ", ""))
	return containsAny(low, profileSpamWords)
}

func containsAny(s string, words []string) bool {
	for _, w := range words {
		if w != "" && strings.Contains(s, w) {
			return true
		}
	}
	return false
}

func humanDur(d time.Duration) string {
	switch {
	case d >= time.Hour && d%time.Hour == 0:
		return fmt.Sprintf("%d 小时", d/time.Hour)
	case d >= time.Minute && d%time.Minute == 0:
		return fmt.Sprintf("%d 分钟", d/time.Minute)
	}
	return fmt.Sprintf("%d 秒", int(d.Seconds()))
}

// MaxVerifyFails 同一次入群验证允许失败的次数，超过后立即按超时处理（拒绝 / 踢出）
const MaxVerifyFails = 3

// RecordFail 记录一次验证失败，返回是否已超过上限（超过则立即作废本次入群）
func (g *Guard) RecordFail(ctx context.Context, groupID, userID int64) bool {
	n := g.st.IncFails(groupID, userID)
	if n < MaxVerifyFails {
		return false
	}
	bg := context.WithoutCancel(ctx)
	g.q.Do(memberKey(groupID, userID), func() {
		if p, _ := g.st.Pending(groupID, userID); p != nil {
			p.ExpiresAt = time.Now().Add(-time.Second)
			g.st.SavePending(p)
			g.expire(bg, groupID, userID)
		}
	})
	return true
}

// needsJoinVerify 直接进群（不是通过入群申请）的新人是否需要先禁言验证
func needsJoinVerify(mode string) bool {
	return mode == model.VerifyRestrict || mode == model.VerifyJoinRequest
}
