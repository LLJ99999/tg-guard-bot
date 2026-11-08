package guard

import (
	"context"
	"fmt"
	"math/rand/v2"
	"regexp"
	"sort"
	"strings"
	"unicode/utf16"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"oai_bot/internal/model"
	"oai_bot/internal/telegram"
)

// mdEmoji 动态表情（Telegram MarkdownV2 同款写法）：![😀](tg://emoji?id=5368324170671202286)
var mdEmoji = regexp.MustCompile(`!\[([^\]\n]+)\]\(tg://emoji\?id=(\d+)\)`)

// EntitiesToMarkdown 把 Telegram 消息的文字 + 格式实体转成面板用的 Markdown（含动态表情）。
// 私聊发给机器人的内容靠它「抓」下来，不用手动输入 emoji ID。
func EntitiesToMarkdown(text string, ents []models.MessageEntity) string {
	u := utf16.Encode([]rune(text))
	type mark struct {
		pos   int
		open  bool
		order int // 打开时外层先开，关闭时内层先关
		s     string
	}
	var marks []mark
	quote := make([]bool, len(u)+1) // 每个位置是否在引用里
	for i, e := range ents {
		if e.Offset < 0 || e.Offset+e.Length > len(u) || e.Length <= 0 {
			continue
		}
		var o, c string
		switch e.Type {
		case models.MessageEntityTypeBold:
			o, c = "**", "**"
		case models.MessageEntityTypeItalic:
			o, c = "*", "*"
		case models.MessageEntityTypeUnderline:
			o, c = "++", "++"
		case models.MessageEntityTypeStrikethrough:
			o, c = "~~", "~~"
		case models.MessageEntityTypeSpoiler:
			o, c = "||", "||"
		case models.MessageEntityTypeCode:
			o, c = "`", "`"
		case models.MessageEntityTypePre:
			o, c = "```\n", "\n```"
		case models.MessageEntityTypeTextLink:
			o, c = "[", "]("+e.URL+")"
		case models.MessageEntityTypeTextMention:
			if e.User != nil {
				o, c = "[", fmt.Sprintf("](tg://user?id=%d)", e.User.ID)
			}
		case models.MessageEntityTypeCustomEmoji:
			o, c = "![", "](tg://emoji?id="+e.CustomEmojiID+")"
		case models.MessageEntityTypeBlockquote, models.MessageEntityTypeExpandableBlockquote:
			for k := e.Offset; k < e.Offset+e.Length; k++ {
				quote[k] = true
			}
			marks = append(marks, mark{pos: e.Offset, open: true, order: -i - 1, s: "> "})
			continue
		}
		if o == "" {
			continue
		}
		marks = append(marks, mark{e.Offset, true, i, o}, mark{e.Offset + e.Length, false, i, c})
	}
	sort.SliceStable(marks, func(a, b int) bool {
		if marks[a].pos != marks[b].pos {
			return marks[a].pos < marks[b].pos
		}
		if marks[a].open != marks[b].open {
			return !marks[a].open // 先关后开
		}
		if marks[a].open {
			return marks[a].order < marks[b].order
		}
		return marks[a].order > marks[b].order
	})
	var b strings.Builder
	mi := 0
	for i := 0; i <= len(u); i++ {
		for mi < len(marks) && marks[mi].pos == i {
			b.WriteString(marks[mi].s)
			mi++
		}
		if i == len(u) {
			break
		}
		// 代理对：一次取两个 UTF-16 单元
		var r rune
		if utf16.IsSurrogate(rune(u[i])) && i+1 < len(u) {
			r = utf16.DecodeRune(rune(u[i]), rune(u[i+1]))
			b.WriteRune(r)
			i++
			for mi < len(marks) && marks[mi].pos == i { // 实体边界不会落在代理对中间，保险起见
				b.WriteString(marks[mi].s)
				mi++
			}
			continue
		}
		r = rune(u[i])
		b.WriteRune(r)
		if r == '\n' && i+1 < len(u) && quote[i] && quote[i+1] {
			b.WriteString("> ")
		}
	}
	return b.String()
}

// mediaKinds 抓取 / 保存时支持的媒体
var mediaKinds = []string{"photo", "animation", "video", "document", "sticker", "audio", "voice"}

// MediaOf 消息里的媒体，返回 "photo:<file_id>" 这种格式
func MediaOf(m *models.Message) string {
	switch {
	case len(m.Photo) > 0:
		return "photo:" + m.Photo[len(m.Photo)-1].FileID
	case m.Animation != nil:
		return "animation:" + m.Animation.FileID
	case m.Video != nil:
		return "video:" + m.Video.FileID
	case m.Document != nil:
		return "document:" + m.Document.FileID
	case m.Sticker != nil:
		return "sticker:" + m.Sticker.FileID
	case m.Audio != nil:
		return "audio:" + m.Audio.FileID
	case m.Voice != nil:
		return "voice:" + m.Voice.FileID
	}
	return ""
}

// MessageMarkdown 消息的文字（或说明文字）转 Markdown
func MessageMarkdown(m *models.Message) string {
	if m.Text != "" {
		return EntitiesToMarkdown(m.Text, m.Entities)
	}
	return EntitiesToMarkdown(m.Caption, m.CaptionEntities)
}

var tgEmojiTag = regexp.MustCompile(`</?tg-emoji[^>]*>`)

// sendRich 发送 HTML 文字 + 可选媒体 + 按钮到指定话题，返回消息。
// 动态表情需要机器人在 Fragment 有收藏用户名才能发；被拒绝时自动去掉动态表情（保留普通 emoji）重发一次
func (g *Guard) sendRich(ctx context.Context, chatID int64, thread int, html, media string, markup models.ReplyMarkup, replyTo int) (*models.Message, error) {
	m, err := g.sendRichOnce(ctx, chatID, thread, html, media, markup, replyTo)
	if err != nil && strings.Contains(html, "<tg-emoji") {
		if m2, err2 := g.sendRichOnce(ctx, chatID, thread, tgEmojiTag.ReplaceAllString(html, ""), media, markup, replyTo); err2 == nil {
			return m2, nil
		}
	}
	return m, err
}

func (g *Guard) sendRichOnce(ctx context.Context, chatID int64, thread int, html, media string, markup models.ReplyMarkup, replyTo int) (*models.Message, error) {
	var rp *models.ReplyParameters
	if replyTo != 0 {
		rp = &models.ReplyParameters{MessageID: replyTo, AllowSendingWithoutReply: true}
	}
	kind, id, _ := strings.Cut(media, ":")
	if id == "" {
		if strings.TrimSpace(html) == "" {
			return nil, fmt.Errorf("内容为空")
		}
		return telegram.Retry(ctx, func() (*models.Message, error) {
			return g.tg.SendMessage(ctx, &tgbot.SendMessageParams{ChatID: chatID, MessageThreadID: thread, Text: html, ParseMode: models.ParseModeHTML,
				ReplyMarkup: markup, ReplyParameters: rp, LinkPreviewOptions: &models.LinkPreviewOptions{IsDisabled: tgbot.True()}})
		})
	}
	f := &models.InputFileString{Data: id}
	if len([]rune(stripHTML(html))) > 1024 {
		return nil, fmt.Errorf("带图片 / 文件时文字不能超过 1024 字")
	}
	return telegram.Retry(ctx, func() (*models.Message, error) {
		switch kind {
		case "photo":
			return g.tg.SendPhoto(ctx, &tgbot.SendPhotoParams{ChatID: chatID, MessageThreadID: thread, Photo: f, Caption: html, ParseMode: models.ParseModeHTML, ReplyMarkup: markup, ReplyParameters: rp})
		case "animation":
			return g.tg.SendAnimation(ctx, &tgbot.SendAnimationParams{ChatID: chatID, MessageThreadID: thread, Animation: f, Caption: html, ParseMode: models.ParseModeHTML, ReplyMarkup: markup, ReplyParameters: rp})
		case "video":
			return g.tg.SendVideo(ctx, &tgbot.SendVideoParams{ChatID: chatID, MessageThreadID: thread, Video: f, Caption: html, ParseMode: models.ParseModeHTML, ReplyMarkup: markup, ReplyParameters: rp})
		case "audio":
			return g.tg.SendAudio(ctx, &tgbot.SendAudioParams{ChatID: chatID, MessageThreadID: thread, Audio: f, Caption: html, ParseMode: models.ParseModeHTML, ReplyMarkup: markup, ReplyParameters: rp})
		case "voice":
			return g.tg.SendVoice(ctx, &tgbot.SendVoiceParams{ChatID: chatID, MessageThreadID: thread, Voice: f, Caption: html, ParseMode: models.ParseModeHTML, ReplyMarkup: markup, ReplyParameters: rp})
		case "sticker": // 贴纸不能带文字：先发贴纸，有文字再发一条
			m, err := g.tg.SendSticker(ctx, &tgbot.SendStickerParams{ChatID: chatID, MessageThreadID: thread, Sticker: f, ReplyParameters: rp})
			if err != nil || strings.TrimSpace(html) == "" {
				return m, err
			}
			return g.tg.SendMessage(ctx, &tgbot.SendMessageParams{ChatID: chatID, MessageThreadID: thread, Text: html, ParseMode: models.ParseModeHTML, ReplyMarkup: markup,
				LinkPreviewOptions: &models.LinkPreviewOptions{IsDisabled: tgbot.True()}})
		default:
			return g.tg.SendDocument(ctx, &tgbot.SendDocumentParams{ChatID: chatID, MessageThreadID: thread, Document: f, Caption: html, ParseMode: models.ParseModeHTML, ReplyMarkup: markup, ReplyParameters: rp})
		}
	})
}

// contentHTML 内容的文字部分转 HTML；Markdown 内容支持多条随机（单独一行 --- 分隔）
func contentHTML(c model.Content) string {
	text := c.Text
	if parts := splitVariants(text); len(parts) > 1 {
		text = parts[rand.IntN(len(parts))]
	}
	if c.Markdown {
		return MarkdownToHTML(text)
	}
	return esc(text)
}

var variantSep = regexp.MustCompile(`(?m)^\s*---\s*$`)

func splitVariants(s string) []string {
	var out []string
	for _, p := range variantSep.Split(s, -1) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
