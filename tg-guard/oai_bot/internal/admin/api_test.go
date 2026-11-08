package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	tgbot "github.com/go-telegram/bot"

	"oai_bot/internal/config"
	"oai_bot/internal/guard"
	"oai_bot/internal/model"
	"oai_bot/internal/store"
	"oai_bot/internal/telegram"
)

func TestCopyExportImportFed(t *testing.T) {
	tgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var res any = true
		if strings.HasSuffix(r.URL.Path, "getMe") {
			res = map[string]any{"id": 42, "is_bot": true, "first_name": "b", "username": "b"}
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": res})
	}))
	defer tgSrv.Close()
	tg, err := telegram.New("1:t", tgbot.WithServerURL(tgSrv.URL))
	if err != nil {
		t.Fatal(err)
	}
	st, _ := store.Open(filepath.Join(t.TempDir(), "a.db"))
	cfg := &config.Config{}
	cfg.Admin.Listen, cfg.Admin.Token = ":0", "0123456789abcdef0123"
	cfg.Bot.OwnerIDs = []int64{1}
	g := guard.New(cfg, tg, st)
	h := New(cfg, g).Handler
	do := func(method, path string, body any) map[string]any {
		var b []byte
		if body != nil {
			b, _ = json.Marshal(body)
		}
		req := httptest.NewRequest(method, "/api"+path, bytes.NewReader(b))
		req.Header.Set("Authorization", "Bearer "+cfg.Admin.Token)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != 200 {
			t.Fatalf("%s %s -> %d %s", method, path, w.Code, w.Body.String())
		}
		var out map[string]any
		json.Unmarshal(w.Body.Bytes(), &out)
		return out
	}
	st.Group(-1, "A")
	st.Group(-2, "B")
	st.UpdateGroup(-1, map[string]any{"flood_count": 9, "lock_link": true, "welcome": "hi"})
	st.AddFilter(&model.Filter{GroupID: -1, Pattern: "博彩", Action: "ban"})

	if r := do("POST", "/groups/-1/copy", map[string]any{"targets": []int64{-2}, "filters": true}); r["groups"].(float64) != 1 || r["filters"].(float64) != 1 {
		t.Fatal(r)
	}
	b, _ := st.Group(-2, "")
	if b.FloodCount != 9 || !b.LockLink || b.Welcome != "" {
		t.Fatalf("复制结果 %+v", b)
	}
	exp := do("GET", "/groups/-1/export", nil)
	st.Group(-3, "C")
	do("POST", "/groups/-3/import", exp)
	c, _ := st.Group(-3, "")
	fs, _ := st.Filters(-3)
	if c.FloodCount != 9 || c.Welcome != "hi" || len(fs) != 1 || fs[0].Action != "ban" {
		t.Fatalf("导入结果 %+v %+v", c, fs)
	}

	fed := do("POST", "/feds", map[string]any{"name": "联盟"})
	id := fed["id"].(string)
	do("POST", "/feds/"+id+"/groups", map[string]any{"group_id": -1})
	do("POST", "/feds/"+id+"/bans", map[string]any{"user_id": 99, "reason": "x"})
	if st.FedBanned(id, 99) == nil {
		t.Fatal("面板联邦封禁")
	}
	req := httptest.NewRequest("GET", "/api/feds", nil)
	req.Header.Set("Authorization", "Bearer "+cfg.Admin.Token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if !strings.Contains(w.Body.String(), `"bans":1`) || !strings.Contains(w.Body.String(), `"title":"A"`) {
		t.Fatal(w.Body.String())
	}
}
