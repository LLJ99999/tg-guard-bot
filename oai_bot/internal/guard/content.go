package guard

import (
	"context"
	"log/slog"
	"strings"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"oai_bot/internal/model"
	"oai_bot/internal/telegram"
)

// contentFrom 从命令构造可复用内容：回复了某条消息则保存那条消息（图片、格式、按钮都保留），否则保存文字
func contentFrom(m *models.Message, text string) (model.Content, bool) {
	if r := m.ReplyToMessage; r != nil && text == "" {
		return model.Content{FromChat: r.Chat.ID, MsgID: r.ID, Text: msgText(r)}, true
	}
	return model.Content{Text: text}, text != ""
}

// sendContent 发送保存的内容；复制原消息失败（原消息被删）时回退纯文字
func (g *Guard) sendContent(ctx context.Context, chatID int64, c model.Content, replyTo int) {
	var rp *models.ReplyParameters
	if replyTo != 0 {
		rp = &models.ReplyParameters{MessageID: replyTo, AllowSendingWithoutReply: true}
	}
	if c.MsgID != 0 {
		_, err := telegram.Retry(ctx, func() (*models.MessageID, error) {
			return g.tg.CopyMessage(ctx, &tgbot.CopyMessageParams{ChatID: chatID, FromChatID: c.FromChat, MessageID: c.MsgID, ReplyParameters: rp})
		})
		if err == nil || c.Text == "" {
			if err != nil {
				slog.Warn("copy content", "err", err)
			}
			return
		}
	}
	if strings.TrimSpace(c.Text) == "" {
		return
	}
	telegram.Retry(ctx, func() (*models.Message, error) {
		return g.tg.SendMessage(ctx, &tgbot.SendMessageParams{ChatID: chatID, Text: c.Text, ReplyParameters: rp,
			LinkPreviewOptions: &models.LinkPreviewOptions{IsDisabled: tgbot.True()}})
	})
}
