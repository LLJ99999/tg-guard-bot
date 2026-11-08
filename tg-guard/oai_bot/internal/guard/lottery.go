package guard

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"oai_bot/internal/model"
	"oai_bot/internal/store"
	"oai_bot/internal/telegram"
)

// LotteryMaxFile 文件奖品大小上限（面板经 AstrBot 转发，base64 后体积会变大）
const LotteryMaxFile = 10 << 20

var lotteryDirty sync.Map // 参与人数有变化、需要刷新群消息的抽奖 ID

func (g *Guard) lotteryDir() string { return filepath.Join(filepath.Dir(g.cfg.DB.Path), "lottery") }

var unsafeName = regexp.MustCompile(`[^\p{L}\p{N}._\- ]+`)

// ParseCodes 卡密文本：每行一条，去掉空行和首尾空白
func ParseCodes(s string) []string {
	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(s, "\r", ""), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// LotteryTier 一个奖项：名字、中奖人数、该奖项的卡密
type LotteryTier struct {
	Name    string   `json:"name"`
	Winners int      `json:"winners"`
	Codes   []string `json:"codes,omitempty"`
}

// lotteryTiers 解析奖项；没设置多奖项时返回一个默认奖项
func lotteryTiers(l *model.Lottery) []LotteryTier {
	var ts []LotteryTier
	if l.Tiers != "" {
		json.Unmarshal([]byte(l.Tiers), &ts)
	}
	if len(ts) == 0 {
		ts = []LotteryTier{{Name: "", Winners: l.Winners}}
	}
	return ts
}

// CreateLottery 面板新建抽奖：保存奖品，发到群里。tiers 为空表示单一奖项（codes 是它的卡密）
func (g *Guard) CreateLottery(ctx context.Context, l *model.Lottery, codes []string, fileName, fileData string, tiers ...LotteryTier) error {
	ctx = context.WithoutCancel(ctx)
	l.Status = model.LotteryOpen
	if l.CreatedAt.IsZero() {
		l.CreatedAt = time.Now()
	}
	l.RequireChat = strings.TrimSpace(l.RequireChat)
	if l.RequireChat != "" {
		var chat any = l.RequireChat
		if id, err := strconv.ParseInt(l.RequireChat, 10, 64); err == nil {
			chat = id
		} else if !strings.HasPrefix(l.RequireChat, "@") {
			chat = "@" + l.RequireChat
		}
		// 机器人必须在那个频道 / 群里才能查成员
		if _, err := g.tg.GetChatMember(ctx, &tgbot.GetChatMemberParams{ChatID: chat, UserID: g.tg.Me().ID}); err != nil {
			return fmt.Errorf("无法检查 %s 的成员：请先把机器人加进那个频道 / 群（设为管理员）", l.RequireChat)
		}
	}
	// 多奖项：中奖总人数 = 各奖项之和
	if len(tiers) > 0 {
		total := 0
		for i := range tiers {
			tiers[i].Name = strings.TrimSpace(tiers[i].Name)
			if tiers[i].Name == "" {
				tiers[i].Name = fmt.Sprintf("%d 等奖", i+1)
			}
			if tiers[i].Winners < 1 {
				return fmt.Errorf("「%s」的中奖人数至少 1", tiers[i].Name)
			}
			total += tiers[i].Winners
		}
		l.Winners = total
	} else {
		tiers = []LotteryTier{{Winners: l.Winners, Codes: codes}}
	}
	var codeRows []model.LotteryCode
	switch l.PrizeType {
	case model.PrizeCode:
		for i, t := range tiers {
			if len(t.Codes) == 0 {
				return fmt.Errorf("请填写%s卡密，每行一条", onOff(t.Name != "", "「"+t.Name+"」的", ""))
			}
			if len(t.Codes) > 1 && len(t.Codes) < t.Winners {
				return fmt.Errorf("%s卡密只有 %d 条，少于中奖人数 %d（只填 1 条表示所有中奖者共用）", onOff(t.Name != "", "「"+t.Name+"」", ""), len(t.Codes), t.Winners)
			}
			for _, c := range t.Codes {
				codeRows = append(codeRows, model.LotteryCode{Code: c, Tier: i})
			}
		}
	case model.PrizeFile:
		if fileData == "" && l.FilePath != "" {
			break // 周期抽奖复用上一期的文件
		}
		_, b64, ok := strings.Cut(fileData, ",")
		if !ok {
			b64 = fileData
		}
		raw, err := base64.StdEncoding.DecodeString(b64)
		if err != nil || len(raw) == 0 {
			return fmt.Errorf("请上传奖品文件")
		}
		if len(raw) > LotteryMaxFile {
			return fmt.Errorf("文件不能超过 10MB")
		}
		name := strings.TrimSpace(unsafeName.ReplaceAllString(filepath.Base(fileName), "_"))
		if name == "" || name == "." {
			name = "prize.bin"
		}
		if err := os.MkdirAll(g.lotteryDir(), 0o755); err != nil {
			return err
		}
		local := strconv.FormatInt(time.Now().UnixNano(), 36) + "_" + name
		if err := os.WriteFile(filepath.Join(g.lotteryDir(), local), raw, 0o644); err != nil {
			return err
		}
		l.FileName, l.FilePath, l.FileSize = name, local, int64(len(raw))
	default:
		return fmt.Errorf("奖品类型无效")
	}
	if len(tiers) > 1 || tiers[0].Name != "" {
		slim := make([]LotteryTier, len(tiers))
		for i, t := range tiers {
			slim[i] = LotteryTier{Name: t.Name, Winners: t.Winners}
		}
		b, _ := json.Marshal(slim)
		l.Tiers = string(b)
	}
	// 公平性：先公布种子的哈希，开奖后公开种子，任何人都能按同样规则复算中奖名单
	var seed [16]byte
	crand.Read(seed[:])
	l.Seed = hex.EncodeToString(seed[:])
	sum := sha256.Sum256([]byte(l.Seed))
	l.SeedHash = hex.EncodeToString(sum[:])
	seedHidden := l.Seed
	l.Seed = "" // 开奖前不落库公开字段……种子单独存
	if err := g.st.CreateLotteryRows(l, codeRows); err != nil {
		return err
	}
	g.st.SetLotterySeed(l.ID, seedHidden)
	msg, err := telegram.Retry(ctx, func() (*models.Message, error) {
		return g.tg.SendMessage(ctx, &tgbot.SendMessageParams{ChatID: l.GroupID, MessageThreadID: l.ThreadID, Text: g.lotteryText(l), ParseMode: models.ParseModeHTML,
			ReplyMarkup: g.lotteryJoinButton(l), LinkPreviewOptions: &models.LinkPreviewOptions{IsDisabled: tgbot.True()}})
	})
	if err != nil {
		g.st.UpdateLottery(l.ID, map[string]any{"status": model.LotteryCancelled})
		return fmt.Errorf("发到群里失败：%v", err)
	}
	l.MsgID = msg.ID
	g.st.UpdateLottery(l.ID, map[string]any{"msg_id": msg.ID})
	if l.PinMsg {
		g.tg.PinChatMessage(ctx, &tgbot.PinChatMessageParams{ChatID: l.GroupID, MessageID: msg.ID, DisableNotification: true})
	}
	g.logf(ctx, "发起抽奖 #%d「%s」，群 %d", l.ID, esc(l.Title), l.GroupID)
	return nil
}

func (g *Guard) prizeLabel(l *model.Lottery) string {
	if l.PrizeType == model.PrizeFile {
		return "文件 " + esc(l.FileName)
	}
	return "卡密"
}

func (g *Guard) drawRule(l *model.Lottery) string {
	var parts []string
	if l.DrawAt != nil {
		parts = append(parts, l.DrawAt.In(g.cfg.Location()).Format("01-02 15:04")+" 开奖")
	}
	if l.DrawCount > 0 {
		parts = append(parts, fmt.Sprintf("满 %d 人开奖", l.DrawCount))
	}
	if len(parts) == 0 {
		return "管理员手动开奖"
	}
	return strings.Join(parts, "，或") + (map[bool]string{true: "（先到先开）"}[len(parts) > 1])
}

func lotteryConds(l *model.Lottery) []string {
	var conds []string
	if l.RequireChat != "" {
		conds = append(conds, "加入 "+esc(requireChatLabel(l.RequireChat)))
	}
	if l.MinMsgs > 0 {
		conds = append(conds, fmt.Sprintf("群内发言满 %d 条", l.MinMsgs))
	}
	if l.MinPoints > 0 {
		conds = append(conds, fmt.Sprintf("积分满 %d", l.MinPoints))
	}
	if l.CostPoints > 0 {
		conds = append(conds, fmt.Sprintf("参与消耗 %d 积分", l.CostPoints))
	}
	if l.MinInvites > 0 {
		conds = append(conds, fmt.Sprintf("邀请满 %d 人", l.MinInvites))
	}
	if l.OldOnly {
		conds = append(conds, "仅限老成员")
	}
	return conds
}

func (g *Guard) lotteryText(l *model.Lottery) string {
	var b strings.Builder
	head := "🎁 <b>抽奖：" + esc(l.Title) + "</b>"
	switch l.Status {
	case model.LotteryDrawn:
		head += "（已开奖）"
	case model.LotteryCancelled:
		head += "（已取消）"
	}
	b.WriteString(head + "\n")
	if d := strings.TrimSpace(l.Desc); d != "" {
		b.WriteString("<blockquote>" + MarkdownToHTML(d) + "</blockquote>\n")
	}
	fmt.Fprintf(&b, "\n<b>奖品：</b>%s\n", g.prizeLabel(l))
	if ts := lotteryTiers(l); len(ts) > 1 || ts[0].Name != "" {
		for _, t := range ts {
			fmt.Fprintf(&b, "　· %s × %d\n", esc(t.Name), t.Winners)
		}
	} else {
		fmt.Fprintf(&b, "<b>中奖人数：</b>%d 人\n", l.Winners)
	}
	if conds := lotteryConds(l); len(conds) > 0 && l.Status == model.LotteryOpen {
		b.WriteString("<b>参与条件：</b>" + strings.Join(conds, "、") + "\n")
	}
	if l.Status == model.LotteryOpen {
		how := "点下面按钮参与（会打开和机器人的私聊，点「开始」即完成），中奖后奖品私聊发给你。"
		if l.JoinInGroup {
			how = "点下面按钮直接参与。中奖后请私聊机器人领奖。"
		}
		fmt.Fprintf(&b, "<b>开奖：</b>%s\n<b>已参与：</b>%d 人\n\n👇 <b>%s</b>", g.drawRule(l), l.Entries, how)
		if l.SeedHash != "" {
			fmt.Fprintf(&b, "\n\n<i>公平性：种子哈希 <code>%s</code>，开奖时公开种子</i>", l.SeedHash[:16])
		}
	} else {
		fmt.Fprintf(&b, "<b>参与：</b>%d 人", l.Entries)
	}
	return b.String()
}

func (g *Guard) lotteryJoinButton(l *model.Lottery) *models.InlineKeyboardMarkup {
	if l.JoinInGroup {
		return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{
			{Text: "🎉 参与抽奖", CallbackData: fmt.Sprintf("lj:%d", l.ID)},
		}}}
	}
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{
		{Text: "🎉 参与抽奖", URL: fmt.Sprintf("https://t.me/%s?start=lt%d", g.tg.Me().Username, l.ID)},
	}}}
}

// refreshLotteryMsg 刷新群里的抽奖消息（人数 / 已开奖）
func (g *Guard) refreshLotteryMsg(ctx context.Context, l *model.Lottery) {
	if l.MsgID == 0 {
		return
	}
	p := &tgbot.EditMessageTextParams{ChatID: l.GroupID, MessageID: l.MsgID, Text: g.lotteryText(l), ParseMode: models.ParseModeHTML,
		LinkPreviewOptions: &models.LinkPreviewOptions{IsDisabled: tgbot.True()}}
	if l.Status == model.LotteryOpen {
		p.ReplyMarkup = g.lotteryJoinButton(l)
	}
	if _, err := g.tg.EditMessageText(ctx, p); err != nil && !strings.Contains(err.Error(), "not modified") {
		slog.Warn("edit lottery", "id", l.ID, "err", err)
	}
}

// joinLottery 私聊 /start lt<ID>
func (g *Guard) joinLottery(ctx context.Context, chatID int64, u *models.User, id uint) {
	reply := func(s string) { g.tg.Notify(ctx, chatID, s, nil, 0) }
	l, _ := g.st.Lottery(id)
	if l == nil {
		reply("这个抽奖不存在。")
		return
	}
	if l.Status != model.LotteryOpen {
		if e, _ := g.st.LotteryEntry(id, u.ID); e != nil && e.Won {
			g.deliverPrize(ctx, l, e)
			return
		}
		reply(fmt.Sprintf("「%s」已经%s了。", esc(l.Title), map[bool]string{true: "开奖", false: "取消"}[l.Status == model.LotteryDrawn]))
		return
	}
	text, _ := g.tryJoinLottery(ctx, l, u, true)
	reply(text)
}

// tryJoinLottery 参与抽奖（私聊和群内按钮共用），返回给用户看的提示
func (g *Guard) tryJoinLottery(ctx context.Context, l *model.Lottery, u *models.User, pm bool) (string, bool) {
	if e, _ := g.st.LotteryEntry(l.ID, u.ID); e != nil {
		return fmt.Sprintf("你已经参与过「%s」了，当前 %d 人参与。开奖：%s。", l.Title, l.Entries, g.drawRule(l)), false
	}
	if why := g.lotteryEligible(ctx, l, u.ID); why != "" {
		return why, false
	}
	if l.CostPoints > 0 && !g.st.SpendPoints(l.GroupID, u.ID, l.CostPoints) {
		return fmt.Sprintf("参与需要消耗 %d 积分，你的积分不够（当前 %d）。", l.CostPoints, g.st.PointsOf(l.GroupID, u.ID).Points), false
	}
	added, n, err := g.st.JoinLottery(&model.LotteryEntry{LotteryID: l.ID, UserID: u.ID, Name: displayName(u), Username: u.Username, CreatedAt: time.Now()})
	if err != nil || !added {
		if l.CostPoints > 0 {
			g.st.AddPoints(l.GroupID, u.ID, l.CostPoints) // 没参与成功，积分退回
		}
		if errors.Is(err, store.ErrLotteryClosed) {
			return fmt.Sprintf("「%s」刚刚开奖了，下次早点来～", l.Title), false
		}
		if err != nil {
			return "参与失败，请稍后再试。", false
		}
		return "你已经参与过了。", false
	}
	lotteryDirty.Store(l.ID, true)
	if l.DrawCount > 0 && n >= l.DrawCount {
		go g.DrawLottery(context.WithoutCancel(ctx), l.ID)
	}
	cost := ""
	if l.CostPoints > 0 {
		cost = fmt.Sprintf("（已扣 %d 积分）", l.CostPoints)
	}
	if pm {
		return fmt.Sprintf("✅ 已参与「%s」%s！当前 %d 人参与。\n开奖：%s\n中奖后奖品会在这里私聊发给你，请不要屏蔽机器人。", esc(l.Title), cost, n, g.drawRule(l)), true
	}
	return fmt.Sprintf("✅ 已参与%s！当前 %d 人。中奖后请私聊机器人领奖。", cost, n), true
}

// onLotteryButton 群里的「参与抽奖」按钮
func (g *Guard) onLotteryButton(ctx context.Context, cq *models.CallbackQuery, id uint) (string, bool) {
	l, _ := g.st.Lottery(id)
	if l == nil || l.Status != model.LotteryOpen {
		return "这个抽奖已经结束了", true
	}
	return g.tryJoinLottery(ctx, l, &cq.From, false)
}

// lotteryEligible 参与条件检查，返回不满足的原因（空表示可以参与）
func (g *Guard) lotteryEligible(ctx context.Context, l *model.Lottery, uid int64) string {
	m, err := g.tg.GetChatMember(ctx, &tgbot.GetChatMemberParams{ChatID: l.GroupID, UserID: uid})
	if err != nil || m == nil || !isIn(*m) {
		return "只有群成员才能参与这个抽奖，先加入群再来点按钮吧。"
	}
	if p, _ := g.st.Pending(l.GroupID, uid); p != nil {
		return "你还没有完成入群验证，验证通过后再来参与吧。"
	}
	if l.OldOnly || l.MinMsgs > 0 {
		mem, _ := g.st.Member(l.GroupID, uid)
		// 机器人进群前就在的老成员没有入群记录（Newcomer=false），视为老成员
		if l.OldOnly && mem != nil && mem.Newcomer && mem.JoinedAt.After(l.CreatedAt) {
			return "这个抽奖只限抽奖发起前就在群里的成员参与。"
		}
		if l.MinMsgs > 0 && (mem == nil || mem.MsgCount < l.MinMsgs) {
			n := 0
			if mem != nil {
				n = mem.MsgCount
			}
			return fmt.Sprintf("参与条件：在群里至少发言 %d 条（你目前 %d 条）。", l.MinMsgs, n)
		}
	}
	if l.MinPoints > 0 {
		if p := g.st.PointsOf(l.GroupID, uid); p.Points < l.MinPoints {
			return fmt.Sprintf("参与条件：积分至少 %d（你目前 %d）。群里发 /checkin 签到可以攒积分。", l.MinPoints, p.Points)
		}
	}
	if l.MinInvites > 0 {
		if n := g.st.InviteCount(l.GroupID, uid); n < l.MinInvites {
			return fmt.Sprintf("参与条件：邀请至少 %d 人进群并通过验证（你目前 %d 人）。群里发 /link 领取专属邀请链接。", l.MinInvites, n)
		}
	}
	if rc := strings.TrimSpace(l.RequireChat); rc != "" {
		var chat any = rc
		if id, err := strconv.ParseInt(rc, 10, 64); err == nil {
			chat = id
		} else if !strings.HasPrefix(rc, "@") {
			chat = "@" + rc
		}
		cm, err := g.tg.GetChatMember(ctx, &tgbot.GetChatMemberParams{ChatID: chat, UserID: uid})
		if err != nil || cm == nil || !isIn(*cm) {
			return fmt.Sprintf("参与条件：需要先加入 %s，加入后再点一次参与按钮。", requireChatLabel(rc))
		}
	}
	return ""
}

func requireChatLabel(rc string) string {
	if strings.HasPrefix(rc, "-") {
		return "指定频道"
	}
	return "@" + strings.TrimPrefix(rc, "@")
}

// stillIn 开奖时确认中奖者还在群里（查询失败按还在处理，避免网络抖动误判）
func (g *Guard) stillIn(ctx context.Context, groupID, uid int64) bool {
	m, err := g.tg.GetChatMember(ctx, &tgbot.GetChatMemberParams{ChatID: groupID, UserID: uid})
	if err != nil || m == nil {
		return true
	}
	return isIn(*m)
}

// LotteryOrder 公平开奖的排序规则：按 SHA-256(种子 + ":" + 用户ID) 从小到大。
// 种子开奖后公开，任何人拿参与名单都能复算出同样的顺序。
func LotteryOrder(seed string, es []model.LotteryEntry) {
	key := func(uid int64) string {
		s := sha256.Sum256([]byte(seed + ":" + strconv.FormatInt(uid, 10)))
		return hex.EncodeToString(s[:])
	}
	sort.SliceStable(es, func(i, j int) bool { return key(es[i].UserID) < key(es[j].UserID) })
}

// DrawLottery 开奖：按公开种子排序抽中奖者、群里公布，奖品在后台私聊发送。返回中奖人数。
// 不依赖调用方的 ctx（面板 HTTP 请求超时 / 断开不会让发奖中途失败）。
func (g *Guard) DrawLottery(ctx context.Context, id uint) (int, error) {
	ctx = context.WithoutCancel(ctx)
	if !g.st.ClaimLottery(id, model.LotteryDrawn) {
		return 0, fmt.Errorf("这个抽奖已经开奖或取消了")
	}
	lotteryDirty.Delete(id)
	l, err := g.st.Lottery(id)
	if err != nil || l == nil {
		return 0, fmt.Errorf("抽奖不存在")
	}
	seed := g.st.LotterySeed(id)
	if seed == "" { // 旧版本发起的抽奖没有种子，临时生成一个
		var b [16]byte
		crand.Read(b[:])
		seed = hex.EncodeToString(b[:])
	}
	entries, _ := g.st.LotteryEntries(id)
	LotteryOrder(seed, entries)
	tiers := lotteryTiers(l)
	var codes []model.LotteryCode
	if l.PrizeType == model.PrizeCode {
		codes, _ = g.st.LotteryCodes(id)
	}
	codesOf := func(tier int) []model.LotteryCode {
		var out []model.LotteryCode
		for _, c := range codes {
			if c.Tier == tier {
				out = append(out, c)
			}
		}
		return out
	}
	// 依次取候选人，已退群的跳过（最多多查 200 人）
	type win struct {
		e    model.LotteryEntry
		tier int
	}
	var winners []win
	skipped, next := 0, 0
	for ti, t := range tiers {
		tc := codesOf(ti)
		want := t.Winners
		if len(tc) > 1 {
			want = min(want, len(tc))
		}
		got := 0
		for next < len(entries) && got < want {
			e := entries[next]
			next++
			if skipped < 200 && !g.stillIn(ctx, l.GroupID, e.UserID) {
				skipped++
				continue
			}
			e.Won, e.Tier = true, t.Name
			fields := map[string]any{"won": true, "tier": t.Name}
			if len(tc) == 1 {
				e.Code = tc[0].Code
			} else if len(tc) > 1 {
				e.Code = tc[got].Code
				g.st.AssignCode(tc[got].ID, e.UserID)
			}
			if e.Code != "" {
				fields["code"] = e.Code
			}
			g.st.UpdateEntry(id, e.UserID, fields)
			winners = append(winners, win{e, ti})
			got++
		}
	}
	n := len(winners)
	g.st.UpdateLottery(id, map[string]any{"seed": seed})
	l.Seed = seed
	g.refreshLotteryMsg(ctx, l)
	if l.PinMsg && l.MsgID != 0 {
		g.tg.UnpinChatMessage(ctx, &tgbot.UnpinChatMessageParams{ChatID: l.GroupID, MessageID: l.MsgID})
	}
	var b strings.Builder
	if n == 0 {
		fmt.Fprintf(&b, "🎁 「%s」开奖：没有符合条件的参与者，本次无人中奖。", esc(l.Title))
	} else {
		fmt.Fprintf(&b, "🎉 <b>「%s」开奖啦！</b>共 %d 人参与，中奖名单：\n", esc(l.Title), len(entries))
		cur, k := -1, 0
		for _, w := range winners {
			if len(tiers) > 1 || tiers[0].Name != "" {
				if w.tier != cur {
					cur, k = w.tier, 0
					fmt.Fprintf(&b, "\n<b>%s</b>\n", esc(tiers[w.tier].Name))
				}
			}
			k++
			fmt.Fprintf(&b, "%d. %s\n", k, mentionID(w.e.UserID, w.e.Name))
		}
		if skipped > 0 {
			fmt.Fprintf(&b, "<i>（%d 位抽中者已退群，已顺延重抽）</i>\n", skipped)
		}
		b.WriteString("\n奖品已私聊发送，没收到的点下面按钮领取。")
	}
	fmt.Fprintf(&b, "\n\n<i>公平性验证：种子 <code>%s</code>，每人按 SHA-256(种子:用户ID) 从小到大排序，依次中奖。</i>", seed)
	var mk models.ReplyMarkup
	if n > 0 {
		mk = &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{
			{Text: "🎁 领取奖品", URL: fmt.Sprintf("https://t.me/%s?start=lc%d", g.tg.Me().Username, l.ID)},
		}}}
	}
	params := &tgbot.SendMessageParams{ChatID: l.GroupID, MessageThreadID: l.ThreadID, Text: b.String(), ParseMode: models.ParseModeHTML, ReplyMarkup: mk,
		LinkPreviewOptions: &models.LinkPreviewOptions{IsDisabled: tgbot.True()}}
	if l.MsgID != 0 {
		params.ReplyParameters = &models.ReplyParameters{MessageID: l.MsgID, AllowSendingWithoutReply: true}
	}
	if m, err := telegram.Retry(ctx, func() (*models.Message, error) { return g.tg.SendMessage(ctx, params) }); err == nil {
		g.st.UpdateLottery(id, map[string]any{"result_msg": m.ID})
		if l.PinMsg {
			g.tg.PinChatMessage(ctx, &tgbot.PinChatMessageParams{ChatID: l.GroupID, MessageID: m.ID, DisableNotification: true})
		}
	} else {
		slog.Warn("lottery result", "id", id, "err", err)
	}
	g.logf(ctx, "抽奖 #%d「%s」开奖：%d 人参与，%d 人中奖", id, esc(l.Title), len(entries), n)
	go func() {
		for i := range winners {
			g.deliverPrize(ctx, l, &winners[i].e)
			time.Sleep(50 * time.Millisecond) // 私聊发送 30 条/秒限制
		}
	}()
	g.repeatLottery(ctx, l)
	return n, nil
}

// repeatLottery 周期抽奖：开奖后按同样设置再发一期（卡密用上一期剩下的）
func (g *Guard) repeatLottery(ctx context.Context, l *model.Lottery) {
	if l.Repeat != "daily" && l.Repeat != "weekly" || l.DrawAt == nil {
		return
	}
	step := 24 * time.Hour
	if l.Repeat == "weekly" {
		step = 7 * 24 * time.Hour
	}
	next := *l
	next.ID, next.MsgID, next.ResultMsg, next.Entries, next.DrawnAt, next.FileID = 0, 0, 0, 0, nil, l.FileID
	next.ParentID = l.ID
	at := l.DrawAt.Add(step)
	for at.Before(time.Now().Add(time.Minute)) {
		at = at.Add(step)
	}
	next.DrawAt = &at
	next.CreatedAt = time.Now()
	var tiers []LotteryTier
	if l.PrizeType == model.PrizeCode {
		codes, _ := g.st.LotteryCodes(l.ID)
		ts := lotteryTiers(l)
		for i, t := range ts {
			var left []string
			for _, c := range codes {
				if c.Tier == i && c.UserID == 0 {
					left = append(left, c.Code)
				}
			}
			if n := countTier(codes, i); n == 1 { // 共用卡密：继续共用
				for _, c := range codes {
					if c.Tier == i {
						left = []string{c.Code}
					}
				}
			}
			if len(left) == 0 || (len(left) > 1 && len(left) < t.Winners) {
				g.logf(ctx, "周期抽奖 #%d 没有足够的剩余卡密，已停止自动续期", l.ID)
				return
			}
			t.Codes = left
			tiers = append(tiers, t)
		}
	}
	if l.PrizeType == model.PrizeFile && l.Tiers != "" {
		tiers = lotteryTiers(l)
	}
	var err error
	if l.Tiers != "" || l.PrizeType == model.PrizeCode {
		if l.Tiers == "" && len(tiers) == 1 {
			err = g.CreateLottery(ctx, &next, tiers[0].Codes, "", "")
		} else {
			err = g.CreateLottery(ctx, &next, nil, "", "", tiers...)
		}
	} else {
		err = g.CreateLottery(ctx, &next, nil, "", "")
	}
	if err != nil {
		g.logf(ctx, "周期抽奖 #%d 续期失败：%s", l.ID, esc(err.Error()))
	}
}

func countTier(cs []model.LotteryCode, tier int) int {
	n := 0
	for _, c := range cs {
		if c.Tier == tier {
			n++
		}
	}
	return n
}

// deliverPrize 私聊发奖品（重复领取也会重发，卡密 / 文件不变）
func (g *Guard) deliverPrize(ctx context.Context, l *model.Lottery, e *model.LotteryEntry) error {
	head := fmt.Sprintf("🎉 恭喜！你在抽奖「%s」中奖了。", esc(l.Title))
	var err error
	if l.PrizeType == model.PrizeFile {
		var in models.InputFile
		if l.FileID != "" {
			in = &models.InputFileString{Data: l.FileID}
		} else {
			raw, rerr := os.ReadFile(filepath.Join(g.lotteryDir(), filepath.Base(l.FilePath)))
			if rerr != nil {
				err = fmt.Errorf("奖品文件丢失")
			} else {
				in = &models.InputFileUpload{Filename: l.FileName, Data: bytes.NewReader(raw)}
			}
		}
		if in != nil {
			var m *models.Message
			m, err = telegram.Retry(ctx, func() (*models.Message, error) {
				return g.tg.SendDocument(ctx, &tgbot.SendDocumentParams{ChatID: e.UserID, Document: in, Caption: head + "\n奖品文件如下：", ParseMode: models.ParseModeHTML})
			})
			if err == nil && l.FileID == "" && m.Document != nil {
				l.FileID = m.Document.FileID
				g.st.UpdateLottery(l.ID, map[string]any{"file_id": l.FileID})
			}
		}
	} else {
		_, err = g.tg.HTML(ctx, e.UserID, head+"\n卡密：<code>"+esc(e.Code)+"</code>\n（点一下即可复制）", nil)
	}
	fields := map[string]any{"delivered": err == nil, "deliver_err": ""}
	if err != nil {
		fields["deliver_err"] = shortErr(err)
		slog.Warn("deliver prize", "lottery", l.ID, "user", e.UserID, "err", err)
	}
	g.st.UpdateEntry(l.ID, e.UserID, fields)
	return err
}

func shortErr(err error) string {
	s := err.Error()
	switch {
	case strings.Contains(s, "blocked"):
		return "用户屏蔽了机器人"
	case strings.Contains(s, "chat not found"), strings.Contains(s, "can't initiate"):
		return "用户没有私聊过机器人"
	}
	if r := []rune(s); len(r) > 80 {
		s = string(r[:80])
	}
	return s
}

// claimPrize 私聊 /start lc<ID>：中奖者补领
func (g *Guard) claimPrize(ctx context.Context, chatID int64, u *models.User, id uint) {
	l, _ := g.st.Lottery(id)
	e, _ := g.st.LotteryEntry(id, u.ID)
	if l == nil || l.Status != model.LotteryDrawn || e == nil || !e.Won {
		g.tg.Notify(ctx, chatID, "你没有中这个抽奖，下次加油～", nil, 0)
		return
	}
	if err := g.deliverPrize(ctx, l, e); err != nil {
		g.tg.Notify(ctx, chatID, "奖品发送失败："+esc(shortErr(err))+"，请联系管理员。", nil, 0)
	}
}

// ResendPrizes 给还没收到奖品的中奖者重发
func (g *Guard) ResendPrizes(ctx context.Context, id uint) (int, int) {
	ctx = context.WithoutCancel(ctx)
	l, _ := g.st.Lottery(id)
	if l == nil || l.Status != model.LotteryDrawn {
		return 0, 0
	}
	es, _ := g.st.LotteryEntries(id)
	ok, failed := 0, 0
	for i := range es {
		if !es[i].Won || es[i].Delivered {
			continue
		}
		if g.deliverPrize(ctx, l, &es[i]) == nil {
			ok++
		} else {
			failed++
		}
	}
	return ok, failed
}

// CancelLottery 取消未开奖的抽奖
func (g *Guard) CancelLottery(ctx context.Context, id uint) error {
	if !g.st.ClaimLottery(id, model.LotteryCancelled) {
		return fmt.Errorf("这个抽奖已经开奖或取消了")
	}
	lotteryDirty.Delete(id)
	if l, _ := g.st.Lottery(id); l != nil {
		if l.FilePath != "" && g.st.LotteryFileUsers(l.FilePath) <= 1 { // 取消后奖品文件没用了（周期抽奖可能共用），删掉
			os.Remove(filepath.Join(g.lotteryDir(), filepath.Base(l.FilePath)))
			g.st.ClearLotteryFile(id)
		}
		g.refreshLotteryMsg(ctx, l)
		g.logf(ctx, "取消抽奖 #%d「%s」", id, esc(l.Title))
	}
	return nil
}

// tickLotteries 定时：到点开奖、刷新人数
func (g *Guard) tickLotteries(ctx context.Context, now time.Time) {
	ls, err := g.st.OpenLotteries()
	if err != nil {
		return
	}
	for i := range ls {
		l := &ls[i]
		if l.DrawAt != nil && !now.Before(*l.DrawAt) {
			go g.DrawLottery(ctx, l.ID) // 不阻塞定时循环（定时消息 / 广告按分钟匹配，卡住会漏发）
			continue
		}
		if _, dirty := lotteryDirty.LoadAndDelete(l.ID); dirty {
			g.refreshLotteryMsg(ctx, l)
		}
	}
}

// ---------- 定时删除（欢迎语等） ----------

// deleteAfter 到时删除消息；记录落库，进程重启后由 tickDeletes 补删
func (g *Guard) deleteAfter(chatID int64, msgID int, after time.Duration) {
	id := g.st.AddDelayedDelete(chatID, msgID, time.Now().Add(after))
	time.AfterFunc(after, func() {
		g.tg.Delete(context.Background(), chatID, msgID)
		if id != 0 {
			g.st.DoneDelete(id)
		}
	})
}

func (g *Guard) tickDeletes(ctx context.Context) {
	ds, err := g.st.DueDeletes(time.Now().Add(-5 * time.Second))
	if err != nil {
		return
	}
	for _, d := range ds {
		g.tg.Delete(ctx, d.ChatID, d.MsgID)
		g.st.DoneDelete(d.ID)
	}
}
