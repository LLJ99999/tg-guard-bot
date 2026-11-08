package store

import (
	"path/filepath"
	"testing"
	"time"

	"oai_bot/internal/model"
)

func TestInviters(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	g, _ := st.Group(-100, "群")
	if !g.InviteNotify {
		t.Fatal("invite_notify 默认应开启")
	}
	st.SaveUser(1, "alice", "Alice", "")
	st.SaveUser(11, "", "小明", "王")
	now := time.Now()
	st.SaveInviteLink(&model.InviteLink{GroupID: -100, UserID: 1, Link: "https://t.me/+a", CreatedAt: now})
	st.SaveInviteLink(&model.InviteLink{GroupID: -100, UserID: 2, Link: "https://t.me/+b", CreatedAt: now})
	st.AddInvite(&model.Invite{GroupID: -100, UserID: 11, InviterID: 1, Verified: true, CreatedAt: now.Add(-time.Hour)})
	st.AddInvite(&model.Invite{GroupID: -100, UserID: 12, InviterID: 1, CreatedAt: now})
	st.AddInvite(&model.Invite{GroupID: -100, UserID: 13, InviterID: 1, Verified: true, Left: true, CreatedAt: now})
	rows, total := st.Inviters(-100, 50, 0)
	if total != 2 || len(rows) != 2 {
		t.Fatalf("inviters %d %+v", total, rows)
	}
	r := rows[0]
	if r.UserID != 1 || r.Total != 3 || r.Valid != 1 || r.Pending != 1 || r.Left != 1 || r.Username != "alice" || r.Link != "https://t.me/+a" {
		t.Fatalf("row %+v", r)
	}
	if rows[1].Total != 0 {
		t.Fatalf("row2 %+v", rows[1])
	}
	list, n := st.InvitesBy(-100, 1, 10, 0)
	if n != 3 || len(list) != 3 || list[2].UserID != 11 || list[2].Name() != "小明 王" || !list[2].Verified {
		t.Fatalf("invitees %d %+v", n, list)
	}
	if st.UserName(1) != "Alice" {
		t.Fatal(st.UserName(1))
	}
}
