package admin

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	tgbot "github.com/go-telegram/bot"

	"oai_bot/internal/config"
	"oai_bot/internal/guard"
	"oai_bot/internal/store"
	"oai_bot/internal/telegram"
	"oai_bot/pkg/tginit"
)

func initData(token string, uid int64) string {
	user := fmt.Sprintf(`{"id":%d,"first_name":"U"}`, uid)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	dcs := "auth_date=" + ts + "\nuser=" + user
	hash := hex.EncodeToString(tginit.Sign(tginit.Sign([]byte("WebAppData"), []byte(token)), []byte(dcs)))
	return url.Values{"user": {user}, "auth_date": {ts}, "hash": {hash}}.Encode()
}

func TestMiniAppAccess(t *testing.T) {
	tgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var res any = true
		if strings.HasSuffix(r.URL.Path, "getMe") {
			res = map[string]any{"id": 42, "is_bot": true, "first_name": "b", "username": "b"}
		}
		if strings.HasSuffix(r.URL.Path, "getChatAdministrators") {
			res = []any{}
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": res})
	}))
	defer tgSrv.Close()
	const token = "1:t"
	tg, err := telegram.New(token, tgbot.WithServerURL(tgSrv.URL))
	if err != nil {
		t.Fatal(err)
	}
	st, _ := store.Open(filepath.Join(t.TempDir(), "a.db"))
	cfg := &config.Config{}
	cfg.Bot.Token = token
	cfg.Bot.OwnerIDs = []int64{1}
	cfg.Web.PublicURL = "https://x.example"
	g := guard.New(cfg, tg, st)
	st.Group(-1, "A")
	st.Group(-2, "B")
	st.UpdateGroup(-1, map[string]any{"panel_mode": "list", "panel_admins": "7"})

	gin.SetMode(gin.TestMode)
	r := gin.New()
	MountMiniApp(r, cfg, g)
	do := func(uid int64, method, path string, body any) (int, string) {
		var b []byte
		if body != nil {
			b, _ = json.Marshal(body)
		}
		req := httptest.NewRequest(method, path, bytes.NewReader(b))
		if uid != 0 {
			req.Header.Set("X-Telegram-Init-Data", initData(token, uid))
		}
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code, w.Body.String()
	}
	if c, body := do(0, "GET", "/app/", nil); c != 200 || !strings.Contains(body, "miniapp.js") {
		t.Fatal("index", c)
	}
	if c, _ := do(0, "GET", "/app/api/groups", nil); c != 401 {
		t.Fatal("没有 initData 应该 401", c)
	}
	if c, body := do(7, "GET", "/app/api/groups", nil); c != 200 || !strings.Contains(body, `"id":-1`) || strings.Contains(body, `"id":-2`) {
		t.Fatal("名单里的人只能看到自己的群", c, body)
	}
	if c, _ := do(7, "GET", "/app/api/groups/-2", nil); c != 403 {
		t.Fatal("别的群应该 403", c)
	}
	if c, body := do(7, "PATCH", "/app/api/groups/-1", map[string]any{"flood_count": 5}); c != 200 {
		t.Fatal("能改自己管理的群", c, body)
	}
	if c, _ := do(7, "PATCH", "/app/api/groups/-1", map[string]any{"panel_mode": ""}); c != 403 {
		t.Fatal("不是群主不能改谁能管理", c)
	}
	if c, _ := do(7, "GET", "/app/api/logs", nil); c != 403 {
		t.Fatal("全局设置只有主人", c)
	}
	if c, body := do(7, "GET", "/app/api/status", nil); c != 200 || !strings.Contains(body, `"owner":false`) || !strings.Contains(body, `"groups":1`) {
		t.Fatal("status", c, body)
	}
	if c, _ := do(1, "GET", "/app/api/logs", nil); c != 200 {
		t.Fatal("主人能看日志", c)
	}
	if c, body := do(1, "GET", "/app/api/groups", nil); c != 200 || !strings.Contains(body, `"id":-2`) {
		t.Fatal("主人看全部群", c)
	}
	if c, _ := do(8, "GET", "/app/api/groups/-1", nil); c != 403 {
		t.Fatal("不在名单里", c)
	}
}
