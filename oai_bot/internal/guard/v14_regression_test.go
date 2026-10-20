package guard

import (
	"context"
	"oai_bot/internal/model"
	"strings"
	"testing"
	"time"
)

func TestCaptchaKeypadLabels(t *testing.T) {
	for _, row := range keypad(-1001).InlineKeyboard {
		for _, b := range row {
			if strings.TrimSpace(b.Text) == "" {
				t.Fatal("empty captcha label")
			}
		}
	}
}

func TestReportFailureCanRetry(t *testing.T) {
	for _, method := range []string{"deleteMessage", "banChatMember", "restrictChatMember"} {
		t.Run(method, func(t *testing.T) {
			g, f := newTestGuard(t)
			ctx := context.Background()
			g.st.Group(-1001, "synthetic")
			r, _, err := g.st.AddReport(&model.Report{GroupID: -1001, TargetID: 7, MsgID: 12})
			if err != nil {
				t.Fatal(err)
			}
			f.mu.Lock()
			f.fail = map[string]bool{method: true}
			f.mu.Unlock()
			action := "ban"
			if method == "restrictChatMember" {
				action = "mute"
			}
			if _, err = g.HandleReport(ctx, r.ID, action, "test"); err == nil {
				t.Fatal("failure reported success")
			}
			after, _ := g.st.Report(r.ID)
			if after.Status != model.ReportOpen {
				t.Fatal("failed report must stay retryable", after.Status)
			}
			stats, _ := g.st.Stats(-1001, []string{time.Now().Format("2006-01-02")})
			for _, s := range stats {
				if s.Banned != 0 || s.Muted != 0 {
					t.Fatal("failed action counted")
				}
			}
			f.mu.Lock()
			f.fail = nil
			f.mu.Unlock()
			if _, err = g.HandleReport(ctx, r.ID, action, "test"); err != nil {
				t.Fatal("retry", err)
			}
			after, _ = g.st.Report(r.ID)
			if after.Status == model.ReportOpen || after.Status == model.ReportProcessing {
				t.Fatal("success not finalized")
			}
		})
	}
}

func TestFedFailuresAndAllowlist(t *testing.T) {
	g, f := newTestGuard(t)
	ctx := context.Background()
	g.cfg.Bot.AllowedGroups = []int64{-1}
	g.st.Group(-1, "allowed")
	g.st.Group(-2, "outside")
	fed, err := g.st.CreateFed("test", 1)
	if err != nil {
		t.Fatal(err)
	}
	g.st.UpdateGroup(-1, map[string]any{"fed_id": fed.ID})
	g.st.UpdateGroup(-2, map[string]any{"fed_id": fed.ID})
	if err = g.JoinFed(ctx, -2, fed.ID); err == nil {
		t.Fatal("join outside allowlist")
	}
	f.mu.Lock()
	f.fail = map[string]bool{"banChatMember": true}
	f.mu.Unlock()
	n, err := g.FedBan(ctx, fed.ID, 77, "test", 1, true)
	if n != 0 || err == nil || f.count("banChatMember") != 1 {
		t.Fatal("failure count/allowlist", n, err)
	}
	f.mu.Lock()
	f.fail = nil
	f.mu.Unlock()
	n, err = g.FedBan(ctx, fed.ID, 77, "test", 1, true)
	if n != 1 || err != nil || f.count("banChatMember") != 2 {
		t.Fatal("retry count", n, err)
	}
	r, _, _ := g.st.AddReport(&model.Report{GroupID: -2, TargetID: 77, MsgID: 1})
	if _, err = g.HandleReport(ctx, r.ID, "ban", "test"); err == nil {
		t.Fatal("report outside allowlist")
	}
}
