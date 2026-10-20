// Package telegram Bot API 客户端封装：429 自动重试与常用调用。
package telegram

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// AllowedUpdates 群管需要 chat_member（必须显式订阅）和 chat_join_request
var AllowedUpdates = []string{"message", "edited_message", "callback_query", "chat_member", "my_chat_member", "chat_join_request"}

type Client struct {
	*bot.Bot
	WebhookSecret string
	me            *models.User
	// OnAction 封禁 / 禁言 / 踢出 / 解除成功后回调，用于记录处罚
	OnAction func(chatID, userID int64, action string, d time.Duration)
}

func (c *Client) fire(chatID, userID int64, action string, d time.Duration) {
	if c.OnAction != nil {
		c.OnAction(chatID, userID, action, d)
	}
}

func New(token string, extra ...bot.Option) (*Client, error) {
	sum := sha256.Sum256([]byte("oai-webhook:" + token))
	secret := hex.EncodeToString(sum[:16])
	opts := append([]bot.Option{
		bot.WithNotAsyncHandlers(), // 更新循环串行，只负责按序入队，处理在 queue 里并发
		bot.WithAllowedUpdates(AllowedUpdates),
		bot.WithWebhookSecretToken(secret),
		bot.WithDefaultHandler(func(context.Context, *bot.Bot, *models.Update) {}),
		bot.WithErrorsHandler(func(err error) { slog.Error("telegram", "err", err) }),
	}, extra...)
	b, err := bot.New(token, opts...)
	if err != nil {
		return nil, err
	}
	me, err := b.GetMe(context.Background())
	if err != nil {
		return nil, err
	}
	return &Client{Bot: b, WebhookSecret: secret, me: me}, nil
}

func (c *Client) Me() *models.User { return c.me }

// Retry 遇到 429 按 retry_after 等待后重试，最多 3 次
func Retry[T any](ctx context.Context, f func() (T, error)) (T, error) {
	for i := 0; ; i++ {
		v, err := f()
		var tm *bot.TooManyRequestsError
		if i >= 3 || !errors.As(err, &tm) {
			return v, err
		}
		select {
		case <-ctx.Done():
			return v, err
		case <-time.After(time.Duration(tm.RetryAfter+1) * time.Second):
		}
	}
}

// HTML 发送 HTML 消息
func (c *Client) HTML(ctx context.Context, chatID int64, text string, markup models.ReplyMarkup) (*models.Message, error) {
	return Retry(ctx, func() (*models.Message, error) {
		return c.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID, Text: text, ParseMode: models.ParseModeHTML, ReplyMarkup: markup,
			LinkPreviewOptions: &models.LinkPreviewOptions{IsDisabled: bot.True()},
		})
	})
}

// Notify 发送 HTML 消息，失败只记日志；ttl > 0 时到点自动删除
func (c *Client) Notify(ctx context.Context, chatID int64, text string, markup models.ReplyMarkup, ttl time.Duration) *models.Message {
	m, err := c.HTML(ctx, chatID, text, markup)
	if err != nil {
		slog.Warn("send", "chat", chatID, "err", err)
		return nil
	}
	if ttl > 0 {
		c.DeleteLater(chatID, m.ID, ttl)
	}
	return m
}

// DeleteLater 延迟删除消息（进程重启会丢失，只用于提示类消息）
func (c *Client) DeleteLater(chatID int64, msgID int, after time.Duration) {
	time.AfterFunc(after, func() { c.Delete(context.Background(), chatID, msgID) })
}

func (c *Client) Delete(ctx context.Context, chatID int64, msgIDs ...int) {
	if len(msgIDs) == 0 {
		return
	}
	var err error
	if len(msgIDs) == 1 {
		_, err = c.DeleteMessage(ctx, &bot.DeleteMessageParams{ChatID: chatID, MessageID: msgIDs[0]})
	} else {
		for start := 0; start < len(msgIDs); start += 100 { // 一次最多 100 条
			end := min(start+100, len(msgIDs))
			_, err = c.DeleteMessages(ctx, &bot.DeleteMessagesParams{ChatID: chatID, MessageIDs: msgIDs[start:end]})
		}
	}
	if err != nil {
		slog.Debug("delete", "chat", chatID, "err", err)
	}
}

// 禁言 / 解禁用的权限
var (
	NoPerms  = &models.ChatPermissions{}
	AllPerms = &models.ChatPermissions{
		CanSendMessages: true, CanSendAudios: true, CanSendDocuments: true, CanSendPhotos: true,
		CanSendVideos: true, CanSendVideoNotes: true, CanSendVoiceNotes: true, CanSendPolls: true,
		CanSendOtherMessages: true, CanAddWebPagePreviews: true, CanReactToMessages: true,
	}
)

// Mute 禁言，d 为 0 表示永久
func (c *Client) Mute(ctx context.Context, chatID, userID int64, d time.Duration) error {
	_, err := c.RestrictChatMember(ctx, &bot.RestrictChatMemberParams{
		ChatID: chatID, UserID: userID, Permissions: NoPerms, UntilDate: until(d),
	})
	if err == nil {
		c.fire(chatID, userID, "mute", d)
	}
	return err
}

// Unmute 解除禁言。恢复为全部权限；群本身的默认权限仍然生效（取交集）
func (c *Client) Unmute(ctx context.Context, chatID, userID int64) error {
	_, err := c.RestrictChatMember(ctx, &bot.RestrictChatMemberParams{
		ChatID: chatID, UserID: userID, Permissions: AllPerms,
	})
	if err == nil {
		c.fire(chatID, userID, "unmute", 0)
	}
	return err
}

// Ban 封禁，d 为 0 表示永久
func (c *Client) Ban(ctx context.Context, chatID, userID int64, d time.Duration, revoke bool) error {
	_, err := c.BanChatMember(ctx, &bot.BanChatMemberParams{ChatID: chatID, UserID: userID, UntilDate: until(d), RevokeMessages: revoke})
	if err == nil {
		c.fire(chatID, userID, "ban", d)
	}
	return err
}

func (c *Client) Unban(ctx context.Context, chatID, userID int64) error {
	_, err := c.UnbanChatMember(ctx, &bot.UnbanChatMemberParams{ChatID: chatID, UserID: userID, OnlyIfBanned: true})
	if err == nil {
		c.fire(chatID, userID, "unban", 0)
	}
	return err
}

// Kick 踢出但允许再次加入
func (c *Client) Kick(ctx context.Context, chatID, userID int64) error {
	if _, err := c.BanChatMember(ctx, &bot.BanChatMemberParams{ChatID: chatID, UserID: userID}); err != nil {
		return err
	}
	_, err := c.UnbanChatMember(ctx, &bot.UnbanChatMemberParams{ChatID: chatID, UserID: userID, OnlyIfBanned: true})
	if err == nil {
		c.fire(chatID, userID, "kick", 0)
	}
	return err
}

func until(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	// Telegram：少于 30 秒或超过 366 天视为永久
	if d < 30*time.Second {
		d = 30 * time.Second
	}
	return int(time.Now().Add(d).Unix())
}
