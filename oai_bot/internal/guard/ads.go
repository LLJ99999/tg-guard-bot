package guard

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"html"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"oai_bot/internal/model"
	"oai_bot/internal/telegram"
)

// ---------- Markdown → Telegram HTML ----------

var (
	mdCodeBlock = regexp.MustCompile("(?s)```(?:[a-zA-Z0-9_+-]*\\n)?(.*?)```")
	mdCode      = regexp.MustCompile("`([^`\\n]+)`")
	mdLink      = regexp.MustCompile(`\[([^\]\n]+)\]\((https?://[^)\s]+|tg://[^)\s]+)\)`)
	mdBold      = regexp.MustCompile(`\*\*([^*\n]+?)\*\*|__([^_\n]+?)__`)
	mdItalic    = regexp.MustCompile(`(^|[^\w*])\*([^*\n]+?)\*($|[^\w*])|(^|[^\w])_([^_\n]+?)_($|[^\w])`)
	mdStrike    = regexp.MustCompile(`~~([^~\n]+?)~~`)
	mdSpoiler   = regexp.MustCompile(`\|\|([^|\n]+?)\|\|`)
	mdUnder     = regexp.MustCompile(`\+\+([^+\n]+?)\+\+`)
	mdHeading   = regexp.MustCompile(`(?m)^#{1,6}\s+(.+)$`)
	mdQuote     = regexp.MustCompile(`(?m)(?:^&gt; ?.*(?:\n|$))+`)
)

// MarkdownToHTML 常用 Markdown 转成 Telegram 支持的 HTML：
// **粗体** *斜体* ++下划线++ __粗体__ _斜体_ ~~删除线~~ ||剧透|| `代码` ```代码块``` [文字](链接) # 标题（转粗体） > 引用
func MarkdownToHTML(md string) string {
	s := html.EscapeString(strings.ReplaceAll(md, "\r\n", "\n"))
	// 代码块和行内代码先抽出来，里面不再解析
	var keep []string
	hold := func(v string) string { keep = append(keep, v); return fmt.Sprintf("\x00%d\x00", len(keep)-1) }
	s = mdCodeBlock.ReplaceAllStringFunc(s, func(m string) string {
		return hold("<pre>" + strings.TrimRight(mdCodeBlock.FindStringSubmatch(m)[1], "\n") + "</pre>")
	})
	s = mdCode.ReplaceAllStringFunc(s, func(m string) string { return hold("<code>" + mdCode.FindStringSubmatch(m)[1] + "</code>") })
	s = mdLink.ReplaceAllStringFunc(s, func(m string) string {
		p := mdLink.FindStringSubmatch(m)
		return hold(`<a href="`+p[2]+`">`) + p[1] + hold("</a>")
	})
	s = mdHeading.ReplaceAllString(s, "<b>$1</b>")
	s = mdBold.ReplaceAllStringFunc(s, func(m string) string {
		p := mdBold.FindStringSubmatch(m)
		return "<b>" + p[1] + p[2] + "</b>"
	})
	s = mdItalic.ReplaceAllStringFunc(s, func(m string) string {
		p := mdItalic.FindStringSubmatch(m)
		if p[2] != "" {
			return p[1] + "<i>" + p[2] + "</i>" + p[3]
		}
		return p[4] + "<i>" + p[5] + "</i>" + p[6]
	})
	s = mdStrike.ReplaceAllString(s, "<s>$1</s>")
	s = mdSpoiler.ReplaceAllString(s, "<tg-spoiler>$1</tg-spoiler>")
	s = mdUnder.ReplaceAllString(s, "<u>$1</u>")
	s = mdQuote.ReplaceAllStringFunc(s, func(m string) string {
		lines := strings.Split(strings.TrimRight(m, "\n"), "\n")
		for i, l := range lines {
			lines[i] = strings.TrimPrefix(strings.TrimPrefix(l, "&gt;"), " ")
		}
		end := ""
		if strings.HasSuffix(m, "\n") {
			end = "\n"
		}
		return "<blockquote>" + strings.Join(lines, "\n") + "</blockquote>" + end
	})
	for i := len(keep) - 1; i >= 0; i-- {
		s = strings.ReplaceAll(s, fmt.Sprintf("\x00%d\x00", i), keep[i])
	}
	return s
}

// ParseButtons 每行一排；「文字 - 链接」，同一排多个用 | 分隔
func ParseButtons(src string) (*models.InlineKeyboardMarkup, error) {
	var rows [][]models.InlineKeyboardButton
	for n, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var row []models.InlineKeyboardButton
		for _, cell := range strings.Split(line, "|") {
			cell = strings.TrimSpace(cell)
			i := strings.LastIndex(cell, " - ")
			if i <= 0 {
				return nil, fmt.Errorf("第 %d 行按钮格式应为「文字 - 链接」", n+1)
			}
			text, url := strings.TrimSpace(cell[:i]), strings.TrimSpace(cell[i+3:])
			if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "tg://") {
				return nil, fmt.Errorf("第 %d 行按钮链接需以 https:// 或 tg:// 开头", n+1)
			}
			row = append(row, models.InlineKeyboardButton{Text: text, URL: url})
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &models.InlineKeyboardMarkup{InlineKeyboard: rows}, nil
}

// ---------- 图片 ----------

func (g *Guard) adDir() string { return filepath.Join(filepath.Dir(g.cfg.DB.Path), "ads") }

const adMaxPhoto = 8 << 20

// SaveAdPhoto 面板上传的 data:image/...;base64,... 存到本地，返回 local:文件名
func (g *Guard) SaveAdPhoto(dataURL string) (string, error) {
	head, b64, ok := strings.Cut(dataURL, ",")
	if !ok || !strings.HasPrefix(head, "data:image/") || !strings.HasSuffix(head, ";base64") {
		return "", fmt.Errorf("图片格式不对")
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(raw) == 0 {
		return "", fmt.Errorf("图片解码失败")
	}
	if len(raw) > adMaxPhoto {
		return "", fmt.Errorf("图片不能超过 8MB")
	}
	ext := map[string]string{"jpeg": ".jpg", "jpg": ".jpg", "png": ".png", "webp": ".webp", "gif": ".gif"}[strings.TrimSuffix(strings.TrimPrefix(head, "data:image/"), ";base64")]
	if ext == "" {
		return "", fmt.Errorf("只支持 jpg / png / webp / gif")
	}
	if err := os.MkdirAll(g.adDir(), 0o755); err != nil {
		return "", err
	}
	name := strconv.FormatInt(time.Now().UnixNano(), 36) + ext
	if err := os.WriteFile(filepath.Join(g.adDir(), name), raw, 0o644); err != nil {
		return "", err
	}
	return "local:" + name, nil
}

func (g *Guard) adPhotoInput(a *model.Ad) (models.InputFile, error) {
	if a.PhotoID != "" {
		return &models.InputFileString{Data: a.PhotoID}, nil
	}
	if name, ok := strings.CutPrefix(a.Photo, "local:"); ok {
		raw, err := os.ReadFile(filepath.Join(g.adDir(), filepath.Base(name)))
		if err != nil {
			return nil, fmt.Errorf("本地图片丢失：%w", err)
		}
		return &models.InputFileUpload{Filename: filepath.Base(name), Data: bytes.NewReader(raw)}, nil
	}
	return &models.InputFileString{Data: a.Photo}, nil
}

// ---------- 发送 ----------

// SendAd 发送一条广告，返回消息 ID
func (g *Guard) SendAd(ctx context.Context, a *model.Ad) (int, error) {
	return g.sendAdTo(ctx, a, a.GroupID)
}

// sendAdTo 发到指定聊天（群或预览用的私聊）
func (g *Guard) sendAdTo(ctx context.Context, a *model.Ad, chatID int64) (int, error) {
	kb, err := ParseButtons(a.Buttons)
	if err != nil {
		return 0, err
	}
	var markup models.ReplyMarkup
	if kb != nil {
		markup = kb
	}
	var msgID int
	switch {
	case a.MsgID != 0:
		r, e := telegram.Retry(ctx, func() (*models.MessageID, error) {
			return g.tg.CopyMessage(ctx, &tgbot.CopyMessageParams{ChatID: chatID, FromChatID: a.FromChat, MessageID: a.MsgID, ReplyMarkup: markup})
		})
		if e != nil {
			return 0, e
		}
		msgID = r.ID
	case a.Photo != "":
		text := MarkdownToHTML(a.Text)
		if len([]rune(stripHTML(text))) > 1024 {
			return 0, fmt.Errorf("带图片时文字不能超过 1024 字")
		}
		photo, e := g.adPhotoInput(a)
		if e != nil {
			return 0, e
		}
		m, e := g.tg.SendPhoto(ctx, &tgbot.SendPhotoParams{ChatID: chatID, Photo: photo, Caption: text, ParseMode: models.ParseModeHTML, ReplyMarkup: markup})
		if e != nil {
			return 0, e
		}
		msgID = m.ID
		if a.PhotoID == "" && len(m.Photo) > 0 && strings.HasPrefix(a.Photo, "local:") {
			a.PhotoID = m.Photo[len(m.Photo)-1].FileID
			g.st.UpdateAd(a.ID, map[string]any{"photo_id": a.PhotoID})
		}
	default:
		if strings.TrimSpace(a.Text) == "" {
			return 0, fmt.Errorf("广告内容为空")
		}
		m, e := telegram.Retry(ctx, func() (*models.Message, error) {
			return g.tg.SendMessage(ctx, &tgbot.SendMessageParams{ChatID: chatID, Text: MarkdownToHTML(a.Text), ParseMode: models.ParseModeHTML, ReplyMarkup: markup,
				LinkPreviewOptions: &models.LinkPreviewOptions{IsDisabled: tgbot.True()}})
		})
		if e != nil {
			return 0, e
		}
		msgID = m.ID
	}
	return msgID, nil
}

// runAd 按设置删旧、发新、置顶并记录
func (g *Guard) runAd(ctx context.Context, a *model.Ad, key string) error {
	now := time.Now()
	g.st.UpdateAd(a.ID, map[string]any{"last_key": key, "last_run_at": now}) // 先标记，避免重复
	if a.DeletePrev && a.LastMsgID != 0 {
		g.tg.Delete(ctx, a.GroupID, a.LastMsgID)
	}
	id, err := g.SendAd(ctx, a)
	if err != nil {
		g.logf(ctx, "⚠️ 定时广告「%s」发送失败\n群：%d\n%s", esc(a.Name), a.GroupID, esc(err.Error()))
		return err
	}
	if a.Pin {
		g.tg.PinChatMessage(ctx, &tgbot.PinChatMessageParams{ChatID: a.GroupID, MessageID: id, DisableNotification: true})
	}
	return g.st.UpdateAd(a.ID, map[string]any{"last_msg_id": id, "sent": a.Sent + 1})
}

// PanelSendAd 面板「立即发送」
func (g *Guard) PanelSendAd(ctx context.Context, groupID int64, id uint) error {
	a, err := g.st.Ad(groupID, id)
	if err != nil || a == nil {
		return fmt.Errorf("广告不存在")
	}
	return g.runAd(ctx, a, a.LastKey)
}

func adDue(a *model.Ad, now time.Time) (bool, string) {
	if !a.Enabled {
		return false, ""
	}
	if strings.TrimSpace(a.Times) != "" {
		hm := now.Format("15:04")
		for _, t := range strings.Split(a.Times, ",") {
			key := now.Format("2006-01-02 ") + hm
			if strings.TrimSpace(t) == hm && a.LastKey != key {
				return true, key
			}
		}
		return false, ""
	}
	if a.Interval <= 0 {
		return false, ""
	}
	if a.LastRunAt == nil || now.Sub(*a.LastRunAt) >= time.Duration(a.Interval)*time.Minute-15*time.Second {
		return true, a.LastKey
	}
	return false, ""
}

func (g *Guard) tickAds(ctx context.Context, now time.Time) {
	as, err := g.st.Ads(0)
	if err != nil {
		return
	}
	for i := range as {
		a := &as[i]
		due, key := adDue(a, now)
		if !due {
			continue
		}
		if grp, _ := g.st.Group(a.GroupID, ""); grp != nil && grp.NightActive {
			continue // 夜间模式期间不发
		}
		if err := g.runAd(ctx, a, key); err != nil {
			slog.Warn("ad", "id", a.ID, "err", err)
		}
	}
}

// ValidateAdSchedule 校验时间设置，返回规范化的 times
func ValidateAdSchedule(times string, interval int) (string, error) {
	var out []string
	for _, t := range strings.FieldsFunc(times, func(r rune) bool { return r == ',' || r == '，' || r == ' ' }) {
		if !hmRe.MatchString(t) {
			return "", fmt.Errorf("时间 %s 需要 HH:MM 格式", t)
		}
		out = append(out, t)
	}
	if len(out) == 0 && (interval < 10 || interval > 10080) {
		return "", fmt.Errorf("请填写每天的发送时间，或 10~10080 分钟的间隔")
	}
	return strings.Join(out, ","), nil
}

// parseAdWhen 群命令里的时间：「2h」「30m」「09:00,21:00」
func parseAdWhen(s string) (times string, interval int, err error) {
	if m := regexp.MustCompile(`^(\d+)([mhd])$`).FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[1])
		interval = n * map[string]int{"m": 1, "h": 60, "d": 1440}[m[2]]
		_, err = ValidateAdSchedule("", interval)
		return "", interval, err
	}
	times, err = ValidateAdSchedule(s, 0)
	return times, 0, err
}

func adWhenText(a *model.Ad) string {
	if a.Times != "" {
		return "每天 " + a.Times
	}
	if a.Interval%60 == 0 {
		return fmt.Sprintf("每 %d 小时", a.Interval/60)
	}
	return fmt.Sprintf("每 %d 分钟", a.Interval)
}

// adCommand 群命令：/ad /ads /delad /adon /adoff /adsend
func (g *Guard) adCommand(ctx context.Context, m *models.Message, grp *model.Group, name, arg string, reply func(string)) bool {
	switch name {
	case "ad":
		first, rest, _ := strings.Cut(strings.TrimSpace(arg), "\n")
		f := strings.Fields(first)
		if m.ReplyToMessage == nil || len(f) == 0 {
			reply("用法：先发好广告（可带图片和格式），再回复它：\n/ad 2h 名字\n按钮文字 - https://链接 | 按钮2 - https://链接\n\n时间可写 30m / 2h / 1d，或每天固定时间 09:00,21:00。按钮每行一排，可省略。\n更方便的是在 AstrBot 后台「定时广告」里用 Markdown 编辑。")
			return true
		}
		times, interval, err := parseAdWhen(f[0])
		if err != nil {
			reply("⚠️ " + esc(err.Error()))
			return true
		}
		if _, err := ParseButtons(rest); err != nil {
			reply("⚠️ " + esc(err.Error()))
			return true
		}
		nm := strings.Join(f[1:], " ")
		if nm == "" {
			nm = "广告"
		}
		a := &model.Ad{GroupID: grp.ID, Name: nm, Text: msgText(m.ReplyToMessage), FromChat: m.Chat.ID, MsgID: m.ReplyToMessage.ID,
			Buttons: strings.TrimSpace(rest), Times: times, Interval: interval, Enabled: true, DeletePrev: true}
		if err := g.st.SaveAd(a); err != nil {
			reply("⚠️ " + esc(err.Error()))
			return true
		}
		note := ""
		if m.From != nil {
			if n, _ := g.PreviewAd(ctx, a, []int64{m.From.ID}); n > 0 {
				note = "\n预览已私聊发给你。"
			} else {
				note = "\n（私聊预览没发出去：先私聊机器人点一下 /start）"
			}
		}
		reply(fmt.Sprintf("✅ 已添加定时广告 #%d「%s」：%s\n注意：别删掉被回复的原消息，广告是复制它发出的。%s", a.ID, esc(nm), adWhenText(a), note))
	case "ads":
		as, _ := g.st.Ads(grp.ID)
		if len(as) == 0 {
			reply("暂无定时广告")
			return true
		}
		var b strings.Builder
		b.WriteString("<b>定时广告</b>\n")
		for _, a := range as {
			fmt.Fprintf(&b, "#%d %s「%s」%s，已发 %d 次\n", a.ID, onOff(a.Enabled, "✅", "⏸"), esc(a.Name), adWhenText(&a), a.Sent)
		}
		b.WriteString("\n/adoff ID 暂停　/adon ID 恢复　/adpreview ID 私聊预览　/adsend ID 立即发　/delad ID 删除")
		reply(b.String())
	case "delad", "adon", "adoff", "adsend", "adpreview":
		id, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(arg), "#"))
		if err != nil {
			reply("用法：/" + name + " 广告ID（/ads 查看）")
			return true
		}
		a, _ := g.st.Ad(grp.ID, uint(id))
		if a == nil {
			reply("没有这条广告")
			return true
		}
		switch name {
		case "delad":
			g.st.DelAd(grp.ID, a.ID)
			reply("✅ 已删除")
		case "adon", "adoff":
			g.st.UpdateAd(a.ID, map[string]any{"enabled": name == "adon"})
			reply("✅ " + onOff(name == "adon", "已恢复", "已暂停"))
		case "adpreview":
			to := []int64{}
			if m.From != nil {
				to = append(to, m.From.ID)
			}
			if n, _ := g.PreviewAd(ctx, a, to); n > 0 {
				reply("✅ 预览已私聊发给你")
			} else {
				reply("⚠️ 私聊发不出去：先私聊机器人点一下 /start")
			}
		case "adsend":
			if err := g.runAd(ctx, a, a.LastKey); err != nil {
				reply("⚠️ " + esc(err.Error()))
			}
		}
	default:
		return false
	}
	return true
}
