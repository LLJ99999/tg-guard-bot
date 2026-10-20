package guard

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"oai_bot/internal/config"
	"oai_bot/internal/model"
	"oai_bot/internal/store"
	"oai_bot/internal/telegram"
)

// fakeTG 假的 Bot API：记录调用的方法，全部返回成功
type fakeTG struct {
	mu    sync.Mutex
	calls []string
	msgID int
	fail  map[string]bool
}

func (f *fakeTG) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	f.mu.Lock()
	f.calls = append(f.calls, method)
	f.msgID++
	id := f.msgID
	fail := f.fail[method]
	f.mu.Unlock()
	if fail {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{"ok": false, "error_code": 400, "description": "synthetic failure"})
		return
	}
	var result any = true
	switch method {
	case "getMe":
		result = map[string]any{"id": 42, "is_bot": true, "first_name": "oai", "username": "oai_bot"}
	case "sendMessage", "sendPhoto", "sendAnimation", "editMessageCaption":
		result = map[string]any{"message_id": id, "date": 0, "chat": map[string]any{"id": 7, "type": "private"}}
	case "getChatAdministrators":
		result = []any{}
	}
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
}

func (f *fakeTG) has(m string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c == m {
			return true
		}
	}
	return false
}

func newTestGuard(t *testing.T) (*Guard, *fakeTG) {
	f := &fakeTG{}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	tg, err := telegram.New("1:test", tgbot.WithServerURL(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	return New(cfg, tg, st), f
}

func waitFor(t *testing.T, cond func() bool) {
	for i := 0; i < 100; i++ {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timeout")
}

func TestJoinRequestImageCaptcha(t *testing.T) {
	g, f := newTestGuard(t)
	ctx := context.Background()
	const gid, uid = int64(-100), int64(7)
	g.st.Group(gid, "测试群")
	g.st.UpdateGroup(gid, map[string]any{"verify_mode": model.VerifyJoinRequest, "captcha_type": model.CaptchaImage, "cas": false})

	g.onJoinRequest(ctx, &models.ChatJoinRequest{Chat: models.Chat{ID: gid, Title: "测试群", Type: "supergroup"}, From: models.User{ID: uid, FirstName: "张三"}, UserChatID: uid})
	if !f.has("sendAnimation") {
		t.Fatal("没有发送图片验证码", f.calls)
	}
	p, _ := g.st.Pending(gid, uid)
	if p == nil || len(p.Answer) != imageDigits || p.ChalMsg == 0 {
		t.Fatalf("pending %+v", p)
	}
	cq := func(data string) string {
		msg, _ := g.onChallengeCallback(ctx, &models.CallbackQuery{From: models.User{ID: uid}, Data: data,
			Message: models.MaybeInaccessibleMessage{Message: &models.Message{ID: p.ChalMsg, Chat: models.Chat{ID: uid}}}})
		return msg
	}
	// 输错一次：换新题，失败 +1
	wrong := "0000"
	if p.Answer == wrong {
		wrong = "1111"
	}
	for _, d := range wrong {
		cq(fmt.Sprintf("ck:%d:%c", gid, d))
	}
	p2, _ := g.st.Pending(gid, uid)
	if p2 == nil || p2.Fails != 1 || p2.Input != "" {
		t.Fatalf("答错后 %+v", p2)
	}
	// 退格 + 正确输入
	cq(fmt.Sprintf("ck:%d:5", gid))
	cq(fmt.Sprintf("ck:%d:b", gid))
	var last string
	for _, d := range p2.Answer {
		last = cq(fmt.Sprintf("ck:%d:%c", gid, d))
	}
	if !strings.Contains(last, "通过") {
		t.Fatal(last)
	}
	waitFor(t, func() bool { return f.has("approveChatJoinRequest") })
	if p, _ := g.st.Pending(gid, uid); p != nil {
		t.Fatal("通过后应删除待验证")
	}
}

func TestMathCaptchaFailLimit(t *testing.T) {
	g, f := newTestGuard(t)
	ctx := context.Background()
	const gid, uid = int64(-200), int64(8)
	g.st.Group(gid, "群2")
	g.st.UpdateGroup(gid, map[string]any{"verify_mode": model.VerifyJoinRequest, "captcha_type": model.CaptchaMath, "cas": false})
	g.onJoinRequest(ctx, &models.ChatJoinRequest{Chat: models.Chat{ID: gid, Title: "群2"}, From: models.User{ID: uid, FirstName: "李四"}, UserChatID: uid})
	if !f.has("sendMessage") {
		t.Fatal(f.calls)
	}
	for i := 0; i < MaxVerifyFails; i++ {
		p, _ := g.st.Pending(gid, uid)
		if p == nil {
			break
		}
		g.onChallengeCallback(ctx, &models.CallbackQuery{From: models.User{ID: uid}, Data: fmt.Sprintf("cv:%d:-1", gid)})
	}
	waitFor(t, func() bool { return f.has("declineChatJoinRequest") })
}

func TestButtonCaptchaOnlySelf(t *testing.T) {
	g, _ := newTestGuard(t)
	ctx := context.Background()
	g.st.SavePending(&model.Pending{GroupID: -300, UserID: 9, Mode: model.VerifyRestrict, ExpiresAt: time.Now().Add(time.Minute)})
	if msg, _ := g.onChallengeCallback(ctx, &models.CallbackQuery{From: models.User{ID: 10}, Data: "vb:-300:9"}); !strings.Contains(msg, "不是你") {
		t.Fatal(msg)
	}
}

func TestAICheckReasoningModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer k" {
			w.WriteHeader(404)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{
			"content": "", "reasoning_content": "<think>{x}</think>{\"spam\":true,\"category\":\"广告\",\"confidence\":0.95,\"reason\":\"引流\"}"}}}})
	}))
	defer srv.Close()
	a := newAIClient(config.AI{BaseURL: srv.URL + "/v1", APIKey: "k", Model: "m"})
	v, err := a.check(context.Background(), "x", "加我微信 abc123 日结 500", true, "")
	if err != nil || !v.Spam || v.Confidence != 0.95 {
		t.Fatal(v, err)
	}
}

func (f *fakeTG) count(m string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c == m {
			n++
		}
	}
	return n
}

func TestFedAndReport(t *testing.T) {
	g, f := newTestGuard(t)
	g.cfg.Bot.OwnerIDs = []int64{1}
	ctx := context.Background()
	g1, _ := g.st.Group(-301, "A")
	g.st.Group(-302, "B")
	owner := &models.User{ID: 1, FirstName: "主人"}
	msg := func(chat int64, text string, reply *models.Message) *models.Message {
		return &models.Message{ID: 900, Chat: models.Chat{ID: chat, Type: "supergroup"}, From: owner, Text: text, ReplyToMessage: reply}
	}
	var out []string
	reply := func(s string) { out = append(out, s) }
	g.fedCommand(ctx, g1, msg(-301, "/newfed 联盟", nil), "newfed", "联盟", reply)
	fs, _ := g.st.Feds()
	if len(fs) != 1 {
		t.Fatal(out)
	}
	g2, _ := g.st.Group(-302, "")
	g.fedCommand(ctx, g2, msg(-302, "", nil), "joinfed", fs[0].ID, reply)
	g1, _ = g.st.Group(-301, "")
	before := f.count("banChatMember")
	g.fedCommand(ctx, g1, msg(-301, "", nil), "fban", "555 广告", reply)
	if f.count("banChatMember")-before != 2 || g.st.FedBanned(fs[0].ID, 555) == nil {
		t.Fatal("联邦封禁应在两个群生效", out)
	}
	g2, _ = g.st.Group(-302, "")
	if r := g.riskCheck(ctx, g2, &models.User{ID: 555, FirstName: "x"}, ""); !strings.Contains(r, "联邦") {
		t.Fatal("入群应被联邦拦截", r)
	}

	// 举报：重复举报合并，按钮封禁
	bad := &models.Message{ID: 50, Chat: models.Chat{ID: -301}, From: &models.User{ID: 77, FirstName: "广告"}, Text: "加我"}
	rep := &models.Message{ID: 51, Chat: models.Chat{ID: -301, Title: "A"}, From: &models.User{ID: 88, FirstName: "群友"}, ReplyToMessage: bad}
	g.report(ctx, rep)
	g.report(ctx, rep)
	rs, _ := g.st.Reports(-301, model.ReportOpen, 10)
	if len(rs) != 1 || rs[0].Count != 2 || rs[0].NotifyMsg == 0 {
		t.Fatalf("%+v", rs)
	}
	before = f.count("banChatMember")
	if txt, err := g.HandleReport(ctx, rs[0].ID, "ban", "测试"); err != nil || !strings.Contains(txt, "封禁") {
		t.Fatal(txt, err)
	}
	if f.count("banChatMember")-before != 1 {
		t.Fatal("应封禁")
	}
	if txt, _ := g.HandleReport(ctx, rs[0].ID, "ign", "测试"); !strings.Contains(txt, "处理过") {
		t.Fatal("重复处理", txt)
	}
}
