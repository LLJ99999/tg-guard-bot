package store

import (
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"oai_bot/internal/model"
	"os"
	"path/filepath"
	"testing"
)

func TestUpgradeKeepsLegacyRules(t *testing.T) {
	p := filepath.Join(t.TempDir(), "legacy.db")
	d, err := gorm.Open(sqlite.Open(p), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = d.Exec("CREATE TABLE groups (id integer PRIMARY KEY, title text, verify_mode text, flood_count integer)").Error; err != nil {
		t.Fatal(err)
	}
	d.Exec("INSERT INTO groups(id,title,verify_mode,flood_count) VALUES(-1,'synthetic','off',0)")
	sqlDB, _ := d.DB()
	sqlDB.Close()
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	g, err := s.Group(-1, "")
	if err != nil {
		t.Fatal(err)
	}
	if g.VerifyMode != "off" || g.FloodCount != 0 || g.ProfileCheck || g.LockInvite || g.LockContact || g.CleanWelcome || g.WelcomeTTL != 0 || g.CaptchaType != model.CaptchaWeb {
		t.Fatalf("legacy rules changed: %+v", g)
	}
	r, _, _ := s.AddReport(&model.Report{GroupID: -1, MsgID: 1})
	if ok, e := s.BeginReport(r.ID, "test"); !ok || e != nil {
		t.Fatal(e)
	}
	if ok, _ := s.BeginReport(r.ID, "duplicate"); ok {
		t.Fatal("duplicate claim")
	}
	if err = s.FinishReport(r.ID, model.ReportOpen, ""); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.BeginReport(r.ID, "retry"); !ok {
		t.Fatal("retry claim")
	}
	sqlDB, _ = s.db.DB()
	sqlDB.Close()
	s, err = Open(p)
	if err != nil {
		t.Fatal(err)
	}
	r, _ = s.Report(r.ID)
	if r.Status != model.ReportOpen {
		t.Fatal("interrupted report not recovered")
	}
}

// A caller may mount only a disposable consistent DB copy for actual migration rehearsal.
func TestDeploymentDatabaseCopy(t *testing.T) {
	p := os.Getenv("HWL_MIGRATION_COPY")
	if p == "" {
		t.Skip("no disposable copy provided")
	}
	if p != "/migration/oai-copy.db" {
		t.Fatal("unexpected migration target")
	}
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	d, _ := s.db.DB()
	defer d.Close()
	var result string
	if err = s.db.Raw("PRAGMA quick_check").Scan(&result).Error; err != nil || result != "ok" {
		t.Fatal("integrity", result, err)
	}
}
