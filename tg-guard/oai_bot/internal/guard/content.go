package guard

import (
	"context"
	"log/slog"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"oai_bot/internal/model"
	"oai_bot/internal/telegram"
)

// contentFrom 从命令构造可复用内容：回复了某条消息则保存那条消息（图片、格式、按钮都保留），否则保存文字
func contentFrom(m *models.Message, text string) (model.Content, bool) {
	if r := m.ReplyToMessage; r != nil && text == "" {
		// 同时记下媒体：原消息被删时还能发，面板也能预览
		return model.Content{FromChat: r.Chat.ID, MsgID: r.ID, Text: msgText(r), Media: MediaOf(r)}, true
	}
	return model.Content{Text: text}, text != ""
}

// sendContent 发送保存的内容：群里回复消息保存的优先复制原消息；面板编辑的按 Markdown + 媒体 + 按钮发送。
// 复制失败（原消息被删）时回退文字。
func (g *Guard) sendContent(ctx context.Context, chatID int64, thread int, c model.Content, replyTo int) *models.Message {
	var markup models.ReplyMarkup
	if kb, err := ParseButtons(c.Buttons); err == nil && kb != nil {
		markup = kb
	}
	if c.MsgID != 0 {
		var rp *models.ReplyParameters
		if replyTo != 0 {
			rp = &models.ReplyParameters{MessageID: replyTo, AllowSendingWithoutReply: true}
		}
		r, err := telegram.Retry(ctx, func() (*models.MessageID, error) {
			return g.tg.CopyMessage(ctx, &tgbot.CopyMessageParams{ChatID: chatID, MessageThreadID: thread, FromChatID: c.FromChat, MessageID: c.MsgID, ReplyParameters: rp, ReplyMarkup: markup})
		})
		if err == nil {
			return &models.Message{ID: r.ID}
		}
		slog.Warn("copy content", "err", err)
		if c.Text == "" && c.Media == "" {
			return nil
		}
	}
	m, err := g.sendRich(ctx, chatID, thread, contentHTML(c), c.Media, markup, replyTo)
	if err != nil {
		slog.Warn("send content", "chat", chatID, "err", err)
		return nil
	}
	return m
}
