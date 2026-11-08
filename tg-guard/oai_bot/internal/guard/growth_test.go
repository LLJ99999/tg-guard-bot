package guard

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"

	"oai_bot/internal/model"
)

func TestEntitiesToMarkdown(t *testing.T) {
	// 「😀加粗 普通 🔥」：😀 是代理对（UTF-16 长度 2）
	text := "😀加粗 普通 🔥"
	ents := []models.MessageEntity{
		{Type: models.MessageEntityTypeCustomEmoji, Offset: 0, Length: 2, CustomEmojiID: "5368324170671202286"},
		{Type: models.MessageEntityTypeBold, Offset: 2, Length: 2},
		{Type: models.MessageEntityTypeTextLink, Offset: 5, Length: 2, URL: "https://a.com"},
		{Type: models.MessageEntityTypeCustomEmoji, Offset: 8, Length: 2, CustomEmojiID: "111"},
	}
	got := EntitiesToMarkdown(text, ents)
	want := "![😀](tg://emoji?id=5368324170671202286)**加粗** [普通](https://a.com) ![🔥](tg://emoji?id=111)"
	if got != want {
		t.Fatalf("\ngot  %s\nwant %s", got, want)
	}
	html := MarkdownToHTML(got)
	if !strings.Contains(html, `<tg-emoji emoji-id="5368324170671202286">😀</tg-emoji>`) || !strings.Contains(html, "<b>加粗</b>") || !strings.Contains(html, `<a href="https://a.com">普通</a>`) {
		t.Fatal(html)
	}
	q := EntitiesToMarkdown("第一行\n第二行", []models.MessageEntity{{Type: models.MessageEntityTypeBlockquote, Offset: 0, Length: 7}})
	if q != "> 第一行\n> 第二行" {
		t.Fatalf("quote: %q", q)
	}
}

func TestLotteryOrderDeterministic(t *testing.T) {
	es := []model.LotteryEntry{{UserID: 1}, {UserID: 2}, {UserID: 3}, {UserID: 4}}
	a := append([]model.LotteryEntry(nil), es...)
	b := []model.LotteryEntry{es[3], es[1], es[0], es[2]}
	LotteryOrder("seed", a)
	LotteryOrder("seed", b)
	for i := range a {
		if a[i].UserID != b[i].UserID {
			t.Fatal("同一个种子必须得到同样的顺序")
		}
	}
}

func TestCheckinAndMsgPoints(t *testing.T) {
	g, _ := newTestGuard(t)
	gain, p, ok := g.st.Checkin(-1, 7, "2026-11-01", "2026-10-31", 10, 2)
	if !ok || gain != 10 || p.Points != 10 || p.Streak != 1 {
		t.Fatalf("first: %d %+v", gain, p)
	}
	if _, _, ok := g.st.Checkin(-1, 7, "2026-11-01", "2026-10-31", 10, 2); ok {
		t.Fatal("同一天不能签两次")
	}
	gain, p, _ = g.st.Checkin(-1, 7, "2026-11-02", "2026-11-01", 10, 2)
	if gain != 12 || p.Streak != 2 || p.Points != 22 {
		t.Fatalf("streak: %d %+v", gain, p)
	}
	for i := 0; i < 10; i++ {
		g.st.AddMsgPoints(-1, 7, "2026-11-02", 2, 5)
	}
	if n := g.st.PointsOf(-1, 7).Points; n != 26 { // 上限 5：只能加两次 2 分
		t.Fatalf("msg cap: %d", n)
	}
	if !g.st.SpendPoints(-1, 7, 20) || g.st.SpendPoints(-1, 7, 20) {
		t.Fatal("扣积分")
	}
}

func TestInviteFlow(t *testing.T) {
	g, _ := newTestGuard(t)
	grp, _ := g.st.Group(-5, "G")
	g.st.UpdateGroup(-5, map[string]any{"points_on": true, "invite_points": 5})
	grp, _ = g.st.Group(-5, "")
	g.recordInvite(-5, 100, &models.ChatInviteLink{Name: "inv:9"})
	g.recordInvite(-5, 100, &models.ChatInviteLink{Name: "inv:8"}) // 只算第一次
	if g.st.InviteCount(-5, 9) != 0 {
		t.Fatal("未验证不算")
	}
	g.inviteVerified(context.Background(), grp, 100)
	if g.st.InviteCount(-5, 9) != 1 || g.st.PointsOf(-5, 9).Points != 5 {
		t.Fatal("验证后计数 + 积分")
	}
	g.st.UpdateInvite(-5, 100, map[string]any{"has_left": true})
	if g.st.InviteCount(-5, 9) != 0 {
		t.Fatal("退群不算")
	}
}

func TestLotteryTiersDraw(t *testing.T) {
	g, _ := newTestGuard(t)
	at := time.Now().Add(time.Hour)
	l := &model.Lottery{GroupID: -9, Title: "T", PrizeType: model.PrizeCode, DrawAt: &at}
	err := g.CreateLottery(context.Background(), l, nil, "", "",
		LotteryTier{Name: "一等奖", Winners: 1, Codes: []string{"A1"}},
		LotteryTier{Name: "二等奖", Winners: 2, Codes: []string{"B1", "B2"}})
	if err != nil {
		t.Fatal(err)
	}
	if l.Winners != 3 || l.SeedHash == "" {
		t.Fatalf("%+v", l)
	}
	for uid := int64(1); uid <= 5; uid++ {
		g.st.JoinLottery(&model.LotteryEntry{LotteryID: l.ID, UserID: uid, Name: "u", CreatedAt: time.Now()})
	}
	n, err := g.DrawLottery(context.Background(), l.ID)
	if err != nil || n != 3 {
		t.Fatal(n, err)
	}
	es, _ := g.st.LotteryEntries(l.ID)
	tiers := map[string]int{}
	codes := map[string]bool{}
	for _, e := range es {
		if e.Won {
			tiers[e.Tier]++
			codes[e.Code] = true
		}
	}
	if tiers["一等奖"] != 1 || tiers["二等奖"] != 2 || !codes["A1"] || !codes["B1"] || !codes["B2"] {
		t.Fatalf("%v %v", tiers, codes)
	}
	l2, _ := g.st.Lottery(l.ID)
	if l2.Seed == "" {
		t.Fatal("开奖后应公开种子")
	}
}

func TestReplyMatch(t *testing.T) {
	r := model.AutoReply{Keyword: "价格", Match: "exact"}
	if !replyMatch(r, "价格") || replyMatch(r, "价格多少") {
		t.Fatal("exact")
	}
	r.Match = "regex"
	r.Keyword = `^(hi|hello)$`
	if !replyMatch(r, "hello") || replyMatch(r, "hello world") {
		t.Fatal("regex")
	}
}

func TestReportDistinct(t *testing.T) {
	g, _ := newTestGuard(t)
	a, _, _ := g.st.AddReport(&model.Report{GroupID: -1, ReporterID: 1, MsgID: 5})
	b, _, _ := g.st.AddReport(&model.Report{GroupID: -1, ReporterID: 1, MsgID: 5})
	c, _, _ := g.st.AddReport(&model.Report{GroupID: -1, ReporterID: 2, MsgID: 5})
	if a.Count != 1 || b.Count != 1 || c.Count != 2 {
		t.Fatal(a.Count, b.Count, c.Count)
	}
}
