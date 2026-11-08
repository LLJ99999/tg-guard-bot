package guard

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"oai_bot/internal/model"

	"github.com/go-telegram/bot/models"
)

// ======================= 私聊抓取 / 私聊输入 =======================

// pmWait 私聊里等用户发的下一条消息：面板抓取，或私聊面板里点了「修改」
type pmWait struct {
	kind  string // cap | edit
	token string
	gid   int64
	field string
	exp   time.Time
}

var pmWaits = struct {
	sync.Mutex
	m map[int64]pmWait
}{m: map[int64]pmWait{}}

func setWait(uid int64, w pmWait) {
	w.exp = time.Now().Add(10 * time.Minute)
	pmWaits.Lock()
	pmWaits.m[uid] = w
	pmWaits.Unlock()
}

func takeWait(uid int64) (pmWait, bool) {
	pmWaits.Lock()
	defer pmWaits.Unlock()
	w, ok := pmWaits.m[uid]
	delete(pmWaits.m, uid)
	return w, ok && time.Now().Before(w.exp)
}

// NewCapture 面板发起抓取，返回 token 和私聊深链
func (g *Guard) NewCapture(purpose string) (*model.Capture, string, error) {
	var b [9]byte
	rand.Read(b[:])
	c := &model.Capture{Token: hex.EncodeToString(b[:]), Purpose: purpose, CreatedAt: time.Now()}
	if err := g.st.CreateCapture(c); err != nil {
		return nil, "", err
	}
	return c, fmt.Sprintf("https://t.me/%s?start=cap_%s", g.tg.Me().Username, c.Token), nil
}

func (g *Guard) startCapture(ctx context.Context, chatID int64, uid int64, token string) {
	c := g.st.Capture(token)
	if c == nil || c.Done || time.Since(c.CreatedAt) > time.Hour {
		g.tg.Notify(ctx, chatID, "这个获取链接已经失效了，请回到面板重新点「从 Telegram 获取」。", nil, 0)
		return
	}
	setWait(uid, pmWait{kind: "cap", token: token})
	what := "文字（可以带粗体、链接、动态表情）、图片、动图、视频或文件"
	switch c.Purpose {
	case "text":
		what = "文字（可以带粗体、链接、动态表情）"
	case "media":
		what = "一张图片 / 动图 / 视频 / 文件"
	}
	g.tg.Notify(ctx, chatID, "📥 请发送要保存的"+what+"。\n直接发或转发都行，10 分钟内有效；发 /cancel 取消。", nil, 0)
}

// fillCapture 私聊收到内容，存进抓取记录，面板轮询拿走
func (g *Guard) fillCapture(ctx context.Context, m *models.Message, token string) {
	c := g.st.Capture(token)
	if c == nil {
		return
	}
	c.Markdown = MessageMarkdown(m)
	c.Media = MediaOf(m)
	c.FromChat, c.MsgID, c.UserID, c.Done = m.Chat.ID, m.ID, m.From.ID, true
	var parts []string
	if c.Media != "" {
		k, _, _ := strings.Cut(c.Media, ":")
		parts = append(parts, map[string]string{"photo": "图片", "animation": "动图", "video": "视频", "document": "文件", "sticker": "贴纸", "audio": "音频", "voice": "语音"}[k])
	}
	if c.Markdown != "" {
		parts = append(parts, fmt.Sprintf("文字 %d 字", len([]rune(c.Markdown))))
	}
	if n := strings.Count(c.Markdown, "tg://emoji?id="); n > 0 {
		parts = append(parts, fmt.Sprintf("动态表情 %d 个", n))
	}
	c.Preview = strings.Join(parts, " + ")
	if c.Preview == "" {
		g.tg.Notify(ctx, m.Chat.ID, "这条消息里没有能保存的内容，请发文字、图片、动图、视频或文件。", nil, 0)
		setWait(m.From.ID, pmWait{kind: "cap", token: token})
		return
	}
	g.st.SaveCapture(c)
	g.tg.Notify(ctx, m.Chat.ID, "✅ 已获取（"+esc(c.Preview)+"），回到面板就能看到。", nil, 0)
}

// CaptureOf 面板轮询
func (g *Guard) CaptureOf(token string) *model.Capture { return g.st.Capture(token) }
