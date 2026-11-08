package guard

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"oai_bot/internal/model"
)

const notesPage = 12

// noteIcon 笔记类型的小图标（按保存的媒体判断）
func noteIcon(n *model.Note) string {
	kind, _, _ := strings.Cut(n.Media, ":")
	switch kind {
	case "photo":
		return "🖼"
	case "video":
		return "🎬"
	case "animation":
		return "🎞"
	case "document":
		return "📄"
	case "audio":
		return "🎵"
	case "voice":
		return "🎤"
	case "sticker":
		return "🌟"
	}
	return "📝"
}

// fileNameOf 文件 / 音频 / 视频的原文件名
func fileNameOf(m *models.Message) string {
	switch {
	case m.Document != nil:
		return m.Document.FileName
	case m.Audio != nil:
		if m.Audio.FileName != "" {
			return m.Audio.FileName
		}
		return m.Audio.Title
	case m.Video != nil:
		return m.Video.FileName
	}
	return ""
}

// noteLink 打开机器人私聊并直接取这条笔记的链接
func (g *Guard) noteLink(id uint) string {
	return fmt.Sprintf("https://t.me/%s?start=nt%d", g.tg.Me().Username, id)
}

// notesIndex 笔记目录：每页 12 条，两列按钮。
// 群里：点按钮不在群里发任何东西，直接跳到机器人私聊，笔记在私聊里发给本人；
// 私聊（private=true）：点按钮当场发出笔记。
func (g *Guard) notesIndex(chatID int64, page int, private bool) (string, *models.InlineKeyboardMarkup) {
	ns, _ := g.st.Notes(chatID)
	if len(ns) == 0 {
		return "本群还没有笔记。\n管理员回复一条消息发 <code>/save 名字</code> 就能存一条（文字、图片、文件都行）。", nil
	}
	pages := (len(ns) + notesPage - 1) / notesPage
	page = min(max(page, 0), pages-1)
	var b strings.Builder
	title := "本群笔记"
	if private {
		if grp, err := g.st.Group(chatID, ""); err == nil && grp.Title != "" {
			title = "「" + esc(grp.Title) + "」的笔记"
		}
	}
	fmt.Fprintf(&b, "<b>📒 %s</b>　共 %d 条", title, len(ns))
	if pages > 1 {
		fmt.Fprintf(&b, "　第 %d/%d 页", page+1, pages)
	}
	if private {
		b.WriteString("\n点按钮查看\n")
	} else {
		b.WriteString("\n点按钮会打开和我的私聊，笔记在私聊里发给你\n")
	}
	var rows [][]models.InlineKeyboardButton
	var row []models.InlineKeyboardButton
	for _, n := range ns[page*notesPage : min(len(ns), (page+1)*notesPage)] {
		n := n
		row = append(row, models.InlineKeyboardButton{Text: noteIcon(&n) + " " + truncate(n.Name, 24), CallbackData: fmt.Sprintf("nt:%d", n.ID)})
		if len(row) == 2 {
			rows = append(rows, row)
			row = nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	var nav []models.InlineKeyboardButton
	pg := func(p int) string {
		if private {
			return fmt.Sprintf("np:%d:%d", p, chatID)
		}
		return fmt.Sprintf("np:%d", p)
	}
	if page > 0 {
		nav = append(nav, models.InlineKeyboardButton{Text: "‹ 上一页", CallbackData: pg(page - 1)})
	}
	if page < pages-1 {
		nav = append(nav, models.InlineKeyboardButton{Text: "下一页 ›", CallbackData: pg(page + 1)})
	}
	if len(nav) > 0 {
		rows = append(rows, nav)
	}
	return b.String(), &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}

// sendNotesIndex 群里发笔记目录，10 分钟后自动删除
func (g *Guard) sendNotesIndex(ctx context.Context, chatID int64, thread int) {
	text, mk := g.notesIndex(chatID, 0, false)
	var markup models.ReplyMarkup
	if mk != nil {
		markup = mk
	}
	g.tg.Notify(ctx, chatID, text, markup, 10*time.Minute)
}

// notePrompt 群里有人发 #名字 或 /get：不在群里发笔记，只回一条带私聊按钮的提示，30 秒后删掉
func (g *Guard) notePrompt(ctx context.Context, chatID int64, n *model.Note) {
	if !g.replyCD.allowFor(fmt.Sprintf("ntp:%d:%d", chatID, n.ID), 10*time.Second) {
		return
	}
	g.tg.Notify(ctx, chatID, fmt.Sprintf("笔记 <b>#%s</b> 请私信我获取 👇", esc(n.Name)),
		&models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{{Text: "📩 私信查看 #" + truncate(n.Name, 20), URL: g.noteLink(n.ID)}}}}, 30*time.Second)
}

// deliverNote 私聊里把笔记发给本人：只给该群成员，发完记一笔
func (g *Guard) deliverNote(ctx context.Context, user *models.User, n *model.Note) {
	if cm, err := g.tg.GetChatMember(ctx, &tgbot.GetChatMemberParams{ChatID: n.GroupID, UserID: user.ID}); err == nil && cm != nil && !isIn(*cm) {
		g.tg.Notify(ctx, user.ID, "这条笔记只有群成员能查看。", nil, 0)
		return
	}
	if g.sendContent(ctx, user.ID, 0, n.Content, 0) == nil {
		g.tg.Notify(ctx, user.ID, "笔记发送失败，请稍后再试。", nil, 0)
		return
	}
	g.st.NoteViewed(n.ID)
	name := strings.TrimSpace(user.FirstName + " " + user.LastName)
	g.st.AddNoteLog(&model.NoteLog{GroupID: n.GroupID, NoteID: n.ID, NoteName: n.Name, UserID: user.ID, Name: name, Username: user.Username})
}

// onNoteStart 私聊 /start nt<笔记ID>（取一条）或 /start nts<群ID>（看目录）
func (g *Guard) onNoteStart(ctx context.Context, m *models.Message, arg string) bool {
	if gs, ok := strings.CutPrefix(arg, "nts"); ok {
		gid, err := strconv.ParseInt(gs, 10, 64)
		if err != nil {
			return false
		}
		text, mk := g.notesIndex(gid, 0, true)
		var markup models.ReplyMarkup
		if mk != nil {
			markup = mk
		}
		g.tg.Notify(ctx, m.Chat.ID, text, markup, 0)
		return true
	}
	if ns, ok := strings.CutPrefix(arg, "nt"); ok {
		id, err := strconv.ParseUint(ns, 10, 32)
		if err != nil {
			return false
		}
		n, _ := g.st.NoteByIDAny(uint(id))
		if n == nil {
			g.tg.Notify(ctx, m.Chat.ID, "这条笔记已经被删除了。", nil, 0)
			return true
		}
		g.deliverNote(ctx, m.From, n)
		return true
	}
	return false
}

// onNoteCallback nt:<笔记ID>、np:<页>[:<群ID>]
// 群里点：不发任何消息，直接跳到机器人私聊（带参数，私聊里自动发出笔记）；私聊里点：当场发出
func (g *Guard) onNoteCallback(ctx context.Context, cq *models.CallbackQuery) bool {
	kind, arg, ok := strings.Cut(cq.Data, ":")
	if !ok || (kind != "nt" && kind != "np") {
		return false
	}
	answer := func(p tgbot.AnswerCallbackQueryParams) {
		p.CallbackQueryID = cq.ID
		g.tg.AnswerCallbackQuery(ctx, &p)
	}
	m := cq.Message.Message
	if m == nil {
		answer(tgbot.AnswerCallbackQueryParams{})
		return true
	}
	private := m.Chat.Type == models.ChatTypePrivate
	if kind == "np" {
		ps, gs, _ := strings.Cut(arg, ":")
		n, _ := strconv.Atoi(ps)
		chat := m.Chat.ID
		if private {
			chat, _ = strconv.ParseInt(gs, 10, 64)
		}
		text, mk := g.notesIndex(chat, n, private)
		answer(tgbot.AnswerCallbackQueryParams{})
		if mk != nil {
			g.tg.EditMessageText(ctx, &tgbot.EditMessageTextParams{ChatID: m.Chat.ID, MessageID: m.ID, Text: text, ParseMode: models.ParseModeHTML, ReplyMarkup: mk})
		}
		return true
	}
	id, _ := strconv.ParseUint(arg, 10, 32)
	var note *model.Note
	if private {
		note, _ = g.st.NoteByIDAny(uint(id))
	} else {
		note, _ = g.st.NoteByID(m.Chat.ID, uint(id))
	}
	if note == nil {
		answer(tgbot.AnswerCallbackQueryParams{Text: "这条笔记已经被删除了", ShowAlert: true})
		return true
	}
	if !private {
		// 打开 t.me/机器人?start=nt<ID>：客户端直接跳进私聊，群里不留任何消息
		answer(tgbot.AnswerCallbackQueryParams{URL: g.noteLink(note.ID)})
		return true
	}
	answer(tgbot.AnswerCallbackQueryParams{})
	g.deliverNote(ctx, &cq.From, note)
	return true
}

// MediaData 面板预览笔记里的图片 / 视频 / 文件：返回 data URL。超过 maxBytes 只返回大小
type MediaData struct {
	Kind    string `json:"kind"`
	Mime    string `json:"mime"`
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	Data    string `json:"data,omitempty"` // data:<mime>;base64,...
	TooBig  bool   `json:"too_big,omitempty"`
	Message string `json:"message,omitempty"`
}

func (g *Guard) MediaData(ctx context.Context, ref string, maxBytes int64) (*MediaData, error) {
	kind, id, ok := strings.Cut(ref, ":")
	if !ok || id == "" {
		return nil, fmt.Errorf("媒体格式不对")
	}
	out := &MediaData{Kind: kind}
	f, err := g.tg.GetFile(ctx, &tgbot.GetFileParams{FileID: id})
	if err != nil {
		if strings.Contains(err.Error(), "too big") {
			out.TooBig, out.Message = true, "文件超过 20MB，Telegram 不允许机器人下载，只能在群里查看"
			return out, nil
		}
		return nil, fmt.Errorf("从 Telegram 取文件失败：%w", err)
	}
	out.Size = int64(f.FileSize)
	out.Name = path.Base(f.FilePath)
	out.Mime = mime.TypeByExtension(strings.ToLower(path.Ext(f.FilePath)))
	if out.Mime == "" {
		out.Mime = map[string]string{"photo": "image/jpeg", "video": "video/mp4", "animation": "video/mp4", "voice": "audio/ogg", "sticker": "image/webp"}[kind]
	}
	if out.Mime == "" {
		out.Mime = "application/octet-stream"
	}
	if out.Size > maxBytes {
		out.TooBig, out.Message = true, fmt.Sprintf("文件有 %.1f MB，太大不在面板里预览", float64(out.Size)/(1<<20))
		return out, nil
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, g.tg.FileDownloadLink(f), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("下载失败：%w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("下载失败：%w", err)
	}
	out.Size = int64(len(raw))
	out.Data = "data:" + out.Mime + ";base64," + base64.StdEncoding.EncodeToString(raw)
	return out, nil
}
