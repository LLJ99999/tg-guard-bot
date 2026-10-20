package store

import (
	"path/filepath"
	"testing"
	"time"

	"oai_bot/internal/model"
)

func TestStore(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	g, err := st.Group(-100, "测试群")
	if err != nil || g.VerifyMode != model.VerifyJoinRequest || g.WarnLimit != 3 || !g.LockForward || g.RaidLimit != 15 || g.NightStart != "00:00" || g.NightEnd != "07:00" || !g.DupCheck {
		t.Fatalf("defaults: %+v %v", g, err)
	}
	if n, _ := st.AddWarn(-100, 1, 1); n != 1 {
		t.Fatal("warn 1")
	}
	if n, _ := st.AddWarn(-100, 1, -5); n != 0 {
		t.Fatal("warn floor 0")
	}
	st.SaveNote(&model.Note{GroupID: -100, Name: "faq", Content: model.Content{Text: "a"}})
	st.SaveNote(&model.Note{GroupID: -100, Name: "faq", Content: model.Content{Text: "b"}})
	if n, _ := st.Note(-100, "FAQ"); n == nil || n.Text != "b" {
		t.Fatalf("note upsert: %+v", n)
	}
	st.SavePending(&model.Pending{GroupID: -100, UserID: 2})
	if st.IncFails(-100, 2) != 1 || st.IncFails(-100, 3) != -1 {
		t.Fatal("fails")
	}
	if p, _ := st.TakePending(-100, 2); p == nil {
		t.Fatal("take")
	}
	if p, _ := st.TakePending(-100, 2); p != nil {
		t.Fatal("take twice")
	}
	st.Count(-100, "2026-10-18", "banned")
	st.Count(-100, "2026-10-18", "banned")
	if ss, _ := st.Stats(-100, []string{"2026-10-18"}); len(ss) != 1 || ss[0].Banned != 2 {
		t.Fatalf("stats %+v", ss)
	}
	st.SetApproved(-100, 9, true)
	if !st.Approved(-100, 9) {
		t.Fatal("approved")
	}
}

func TestWarnExpire(t *testing.T) {
	s, err := Open(t.TempDir() + "/w.db")
	if err != nil {
		t.Fatal(err)
	}
	s.AddWarn(1, 2, 1, time.Hour)
	if n, _ := s.AddWarn(1, 2, 1, time.Hour); n != 2 {
		t.Fatal(n)
	}
	old := time.Now().Add(-2 * time.Hour)
	s.UpdateMember(1, 2, map[string]any{"warn_at": old})
	if n, _ := s.AddWarn(1, 2, 1, time.Hour); n != 1 {
		t.Fatal("过期警告应清零", n)
	}
}
