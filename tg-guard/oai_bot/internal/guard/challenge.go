package guard

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"image"
	"image/color/palette"
	"image/draw"
	"image/gif"
	"image/png"
	"log/slog"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/dchest/captcha"
	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"oai_bot/internal/model"
	"oai_bot/internal/telegram"
)

// 机器人内置验证码：不需要网页、域名、Turnstile，在 Telegram 里点按钮完成。
//
// 回调数据：
//   vb:<群>:<用户>  群里的「我不是机器人」按钮（button 类型 + 进群禁言模式）
//   cv:<群>:<值>    私聊里选择题的答案（算术 / 表情 / 按钮）
//   ck:<群>:<键>    私聊里图片验证码的数字键盘（0-9，b 退格）
//   cr:<群>         换一题

const (
	imageDigits = 4
	maxRefresh  = 3
)

var emojiPool = []struct{ e, n string }{
	{"🐶", "狗"}, {"🐱", "猫"}, {"🐭", "老鼠"}, {"🐰", "兔子"}, {"🦊", "狐狸"}, {"🐻", "熊"}, {"🐼", "熊猫"}, {"🐯", "老虎"},
	{"🦁", "狮子"}, {"🐮", "牛"}, {"🐷", "猪"}, {"🐸", "青蛙"}, {"🐵", "猴子"}, {"🐔", "鸡"}, {"🐧", "企鹅"}, {"🐟", "鱼"},
	{"🍎", "苹果"}, {"🍌", "香蕉"}, {"🍇", "葡萄"}, {"🍉", "西瓜"}, {"🍓", "草莓"}, {"🍑", "桃子"}, {"🥕", "胡萝卜"}, {"🌽", "玉米"},
	{"🚗", "汽车"}, {"🚲", "自行车"}, {"✈️", "飞机"}, {"🚀", "火箭"}, {"⚽", "足球"}, {"🎸", "吉他"}, {"⏰", "闹钟"}, {"☂️", "雨伞"},
}

func randInt(n int) int {
	v, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		return int(time.Now().UnixNano() % int64(n))
	}
	return int(v.Int64())
}

func shuffle[T any](s []T) {
	for i := len(s) - 1; i > 0; i-- {
		j := randInt(i + 1)
		s[i], s[j] = s[j], s[i]
	}
}

func captchaName(t string) string {
	switch t {
	case model.CaptchaMath:
		return "算术题"
	case model.CaptchaEmoji:
		return "点表情"
	case model.CaptchaButton:
		return "点按钮"
	case model.CaptchaWeb:
		return "网页验证"
	}
	return "动态验证码"
}

// canVerify 这个群当前的验证码类型能不能用（只有网页验证依赖外部配置）
func (g *Guard) canVerify(grp *model.Group) bool {
	return grp.CaptchaType != model.CaptchaWeb || g.captcha != nil
}

// sendChallenge 在私聊里出题（新题会替换旧题）
func (g *Guard) sendChallenge(ctx context.Context, chatID int64, grp *model.Group, p *model.Pending, intro string) {
	if p.ChalMsg != 0 {
		g.tg.Delete(ctx, chatID, p.ChalMsg)
	}
	left := humanDur(time.Until(p.ExpiresAt).Round(time.Second))
	head := ""
	if intro != "" {
		head = intro + "\n\n"
	}
	cb := func(text, data string) models.InlineKeyboardButton {
		return models.InlineKeyboardButton{Text: text, CallbackData: fmt.Sprintf("%s:%d:%s", data[:2], grp.ID, data[3:])}
	}
	refresh := []models.InlineKeyboardButton{{Text: "换一题", CallbackData: fmt.Sprintf("cr:%d", grp.ID)}}
	var msg *models.Message
	var err error
	answer := ""

	switch grp.CaptchaType {
	case model.CaptchaWeb:
		msg, err = g.tg.HTML(ctx, chatID, head+fmt.Sprintf("请在 %s 内点击下方按钮完成验证：", left), g.verifyButton(grp.ID))

	case model.CaptchaButton:
		answer = "ok"
		text := head + fmt.Sprintf("请在 %s 内点击下方按钮，证明你不是机器人。", left)
		msg, err = g.tg.HTML(ctx, chatID, text, &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{cb("我不是机器人", "cv:ok")}}})

	case model.CaptchaMath:
		a, b := randInt(20)+1, randInt(20)+1
		op := []string{"+", "-", "×"}[randInt(3)]
		var ans int
		switch op {
		case "+":
			ans = a + b
		case "-":
			if a < b {
				a, b = b, a
			}
			ans = a - b
		default:
			a, b = randInt(9)+2, randInt(9)+2
			ans = a * b
		}
		answer = strconv.Itoa(ans)
		opts := map[int]bool{ans: true}
		for len(opts) < 6 {
			if v := ans + randInt(21) - 10; v >= 0 {
				opts[v] = true
			}
		}
		vals := make([]int, 0, 6)
		for v := range opts {
			vals = append(vals, v)
		}
		shuffle(vals)
		rows := [][]models.InlineKeyboardButton{{}, {}}
		for i, v := range vals {
			rows[i/3] = append(rows[i/3], cb(strconv.Itoa(v), "cv:"+strconv.Itoa(v)))
		}
		rows = append(rows, refresh)
		text := head + fmt.Sprintf("请在 %s 内选出正确答案：\n\n<b>%d %s %d = ?</b>", left, a, op, b)
		msg, err = g.tg.HTML(ctx, chatID, text, &models.InlineKeyboardMarkup{InlineKeyboard: rows})

	case model.CaptchaEmoji:
		idx := make([]int, len(emojiPool))
		for i := range idx {
			idx[i] = i
		}
		shuffle(idx)
		pick := idx[:8]
		target := pick[randInt(len(pick))]
		answer = strconv.Itoa(target)
		rows := [][]models.InlineKeyboardButton{{}, {}}
		for i, k := range pick {
			rows[i/4] = append(rows[i/4], cb(emojiPool[k].e, "cv:"+strconv.Itoa(k)))
		}
		rows = append(rows, refresh)
		text := head + fmt.Sprintf("请在 %s 内点击：<b>%s</b>", left, emojiPool[target].n)
		msg, err = g.tg.HTML(ctx, chatID, text, &models.InlineKeyboardMarkup{InlineKeyboard: rows})

	default: // 动态验证码：GIF 一直闪（每帧扭曲 / 噪点不同，数字不变），下面几个选项按钮点一下
		digits := make([]byte, imageDigits)
		var sb strings.Builder
		for i := range digits {
			digits[i] = byte(randInt(10))
			sb.WriteByte('0' + digits[i])
		}
		answer = sb.String()
		var buf bytes.Buffer
		if buf, err = captchaGIF(digits); err != nil {
			break
		}
		opts := codeOptions(answer, 6)
		rows := [][]models.InlineKeyboardButton{{}, {}}
		for i, v := range opts {
			rows[i/3] = append(rows[i/3], cb(v, "cv:"+v))
		}
		rows = append(rows, refresh)
		caption := head + fmt.Sprintf("请在 %s 内点击动图里显示的 %d 位数字：", left, imageDigits)
		msg, err = telegram.Retry(ctx, func() (*models.Message, error) {
			return g.tg.SendAnimation(ctx, &tgbot.SendAnimationParams{
				ChatID: chatID, Animation: &models.InputFileUpload{Filename: "captcha.gif", Data: bytes.NewReader(buf.Bytes())},
				Width: 300, Height: 100,
				Caption: caption, ParseMode: models.ParseModeHTML, ReplyMarkup: &models.InlineKeyboardMarkup{InlineKeyboard: rows},
			})
		})
	}
	if err != nil {
		slog.Warn("send challenge", "chat", chatID, "type", grp.CaptchaType, "err", err)
		return
	}
	g.st.UpdatePending(grp.ID, p.UserID, map[string]any{"answer": answer, "input": "", "chal_msg": msg.ID})
	p.Answer, p.Input, p.ChalMsg = answer, "", msg.ID
}

func imageCaption(left, input string) string {
	slots := make([]string, imageDigits)
	for i := range slots {
		if i < len(input) {
			slots[i] = "●"
		} else {
			slots[i] = "＿"
		}
	}
	return fmt.Sprintf("请在 %s 内输入图中的 %d 位数字：\n\n<b>%s</b>", left, imageDigits, strings.Join(slots, " "))
}

func keypad(groupID int64) *models.InlineKeyboardMarkup {
	k := func(t, v string) models.InlineKeyboardButton {
		return models.InlineKeyboardButton{Text: t, CallbackData: fmt.Sprintf("ck:%d:%s", groupID, v)}
	}
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{
		{k("1", "1"), k("2", "2"), k("3", "3")},
		{k("4", "4"), k("5", "5"), k("6", "6")},
		{k("7", "7"), k("8", "8"), k("9", "9")},
		{{Text: "换一题", CallbackData: fmt.Sprintf("cr:%d", groupID)}, k("0", "0"), k("退格", "b")},
	}}
}

// startChallenge 有待验证记录时，在私聊里开始验证
func (g *Guard) startChallenge(ctx context.Context, chatID, groupID int64, intro string) bool {
	p, _ := g.st.Pending(groupID, chatID)
	if p == nil {
		return false
	}
	grp, err := g.st.Group(groupID, "")
	if err != nil {
		return false
	}
	g.sendChallenge(ctx, chatID, grp, p, intro)
	return true
}

// onChallengeCallback 处理验证相关按钮，返回给用户的提示；ok=false 表示不是验证按钮
func (g *Guard) onChallengeCallback(ctx context.Context, cq *models.CallbackQuery) (string, bool) {
	parts := strings.Split(cq.Data, ":")
	if len(parts) < 2 {
		return "", false
	}
	kind := parts[0]
	if kind != "vb" && kind != "cv" && kind != "ck" && kind != "cr" {
		return "", false
	}
	gid, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return "", true
	}
	uid := cq.From.ID
	if kind == "vb" { // 群里的按钮：只能本人点
		if len(parts) != 3 || parts[2] != strconv.FormatInt(uid, 10) {
			return "这不是你的验证按钮", true
		}
		if p, _ := g.st.Pending(gid, uid); p == nil {
			return "验证已结束", true
		}
		g.q.Do(memberKey(gid, uid), func() { g.pass(context.WithoutCancel(ctx), gid, uid, "验证通过（按钮）") })
		return "验证通过", true
	}

	p, _ := g.st.Pending(gid, uid)
	if p == nil {
		if m := cq.Message.Message; m != nil {
			g.tg.Delete(ctx, m.Chat.ID, m.ID)
		}
		return "验证已结束（可能已通过或已超时）", true
	}
	grp, err := g.st.Group(gid, "")
	if err != nil {
		return "读取失败", true
	}
	chatID := uid
	if m := cq.Message.Message; m != nil {
		chatID = m.Chat.ID
	}

	switch kind {
	case "cr":
		if p.Refresh >= maxRefresh {
			return "换题次数用完了", true
		}
		g.st.UpdatePending(gid, uid, map[string]any{"refresh": p.Refresh + 1})
		g.sendChallenge(ctx, chatID, grp, p, "")
		return "已换一题", true

	case "cv":
		if len(parts) != 3 {
			return "", true
		}
		if parts[2] == p.Answer {
			g.q.Do(memberKey(gid, uid), func() {
				g.pass(context.WithoutCancel(ctx), gid, uid, "验证通过（"+captchaName(grp.CaptchaType)+"）")
			})
			return "验证通过", true
		}
		return g.wrongAnswer(ctx, chatID, grp, p), true

	case "ck":
		if len(parts) != 3 || p.Answer == "" {
			return "", true
		}
		in := p.Input
		switch k := parts[2]; {
		case k == "b":
			if in != "" {
				in = in[:len(in)-1]
			}
		case len(k) == 1 && k[0] >= '0' && k[0] <= '9' && len(in) < imageDigits:
			in += k
		default:
			return "", true
		}
		if len(in) == imageDigits {
			if in == p.Answer {
				g.q.Do(memberKey(gid, uid), func() { g.pass(context.WithoutCancel(ctx), gid, uid, "验证通过（图片验证码）") })
				return "验证通过", true
			}
			return g.wrongAnswer(ctx, chatID, grp, p), true
		}
		if !g.st.UpdatePending(gid, uid, map[string]any{"input": in}) {
			return "验证已结束", true
		}
		if m := cq.Message.Message; m != nil {
			g.tg.EditMessageCaption(ctx, &tgbot.EditMessageCaptionParams{
				ChatID: m.Chat.ID, MessageID: m.ID, ParseMode: models.ParseModeHTML,
				Caption:     imageCaption(humanDur(time.Until(p.ExpiresAt).Round(time.Second)), in),
				ReplyMarkup: keypad(gid),
			})
		}
		return "", true
	}
	return "", true
}

// wrongAnswer 答错：记一次失败，超过上限作废本次入群，否则换一题
func (g *Guard) wrongAnswer(ctx context.Context, chatID int64, grp *model.Group, p *model.Pending) string {
	if g.RecordFail(ctx, grp.ID, p.UserID) {
		if p.ChalMsg != 0 {
			g.tg.Delete(ctx, chatID, p.ChalMsg)
		}
		return "错误次数过多，本次入群已作废"
	}
	left := MaxVerifyFails - (p.Fails + 1)
	g.sendChallenge(ctx, chatID, grp, p, fmt.Sprintf("答错了，还剩 %d 次机会。", left))
	return fmt.Sprintf("答错了，还剩 %d 次", left)
}

// captchaPNG 生成白底验证码图片（原图背景透明，Telegram 深色模式下看不清）
func captchaPNG(digits []byte) (bytes.Buffer, error) {
	var buf bytes.Buffer
	src := captcha.NewImage(fmt.Sprintf("%d", time.Now().UnixNano()), digits, 300, 100)
	dst := image.NewRGBA(src.Bounds())
	draw.Draw(dst, dst.Bounds(), image.White, image.Point{}, draw.Src)
	draw.Draw(dst, dst.Bounds(), src, src.Bounds().Min, draw.Over)
	err := png.Encode(&buf, dst)
	return buf, err
}

// codeOptions 正确答案 + 干扰项（大多只改 1~2 位，必须看清动图才能选对），打乱顺序
func codeOptions(answer string, n int) []string {
	set := map[string]bool{answer: true}
	out := []string{answer}
	for len(out) < n {
		b := []byte(answer)
		changes := 1 + randInt(2)
		for c := 0; c < changes; c++ {
			b[randInt(len(b))] = byte('0' + randInt(10))
		}
		if v := string(b); !set[v] {
			set[v] = true
			out = append(out, v)
		}
	}
	shuffle(out)
	return out
}

// captchaGIF 同一组数字画 8 帧，每帧扭曲、噪点、颜色都不同，循环播放形成闪烁效果
func captchaGIF(digits []byte) (bytes.Buffer, error) {
	var buf bytes.Buffer
	const frames = 8
	anim := &gif.GIF{LoopCount: 0}
	for f := 0; f < frames; f++ {
		src := captcha.NewImage(fmt.Sprintf("%d-%d", time.Now().UnixNano(), f), digits, 300, 100)
		pm := image.NewPaletted(src.Bounds(), palette.Plan9)
		draw.Draw(pm, pm.Bounds(), image.White, image.Point{}, draw.Src)
		draw.Draw(pm, pm.Bounds(), src, src.Bounds().Min, draw.Over)
		anim.Image = append(anim.Image, pm)
		anim.Delay = append(anim.Delay, 18+randInt(10)) // 0.18~0.27 秒一帧
	}
	err := gif.EncodeAll(&buf, anim)
	return buf, err
}

var _ = png.Encode
