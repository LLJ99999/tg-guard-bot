package store

import (
	"path/filepath"
	"testing"

	"oai_bot/internal/model"
)

func TestBioStore(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "b.db"))
	if err != nil {
		t.Fatal(err)
	}
	g, _ := st.Group(-1, "g")
	if g.BioScan || g.BioScanHours != 24 || g.BioScanMin != 3 {
		t.Fatalf("defaults %+v", g)
	}
	st.Member(-1, 1)
	st.Member(-1, 2)
	st.UpdateMember(-1, 2, map[string]any{"has_left": true})
	ms, err := st.ActiveMembers(-1)
	if err != nil || len(ms) != 1 || ms[0].UserID != 1 {
		t.Fatalf("active %v %v", ms, err)
	}
	for i := 0; i < 25; i++ {
		if err := st.SaveBioReport(&model.BioReport{GroupID: -1, Scanned: i}); err != nil {
			t.Fatal(err)
		}
	}
	r, _ := st.LatestBioReport(-1)
	if r == nil || r.Scanned != 24 {
		t.Fatalf("latest %+v", r)
	}
	var n int64
	st.db.Model(&model.BioReport{}).Count(&n)
	if n != 20 {
		t.Fatalf("kept %d", n)
	}
	st.SaveUserBio(&model.UserBio{UserID: 1, Links: "t.me/x"})
	if b, _ := st.UserBio(1); b == nil || b.Links != "t.me/x" {
		t.Fatal("userbio")
	}
}
