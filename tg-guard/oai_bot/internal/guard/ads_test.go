package guard

import (
	"testing"
	"time"

	"oai_bot/internal/model"
)

func TestMarkdownToHTML(t *testing.T) {
	cases := map[string]string{
		"**粗** *斜* ~~删~~ ||剧透||":    "<b>粗</b> <i>斜</i> <s>删</s> <tg-spoiler>剧透</tg-spoiler>",
		"[官网](https://a.com/x_y_z)": `<a href="https://a.com/x_y_z">官网</a>`,
		"`a<b>` 和 snake_case_name":  "<code>a&lt;b&gt;</code> 和 snake_case_name",
		"# 标题\n> 引用1\n> 引用2\n正文":    "<b>标题</b>\n<blockquote>引用1\n引用2</blockquote>\n正文",
		"1 < 2 & 3":                 "1 &lt; 2 &amp; 3",
	}
	for in, want := range cases {
		if got := MarkdownToHTML(in); got != want {
			t.Errorf("%q\n got %q\nwant %q", in, got, want)
		}
	}
}

func TestParseButtons(t *testing.T) {
	kb, err := ParseButtons("官网 - https://a.com | 客服 - https://t.me/x\n\n频道 - tg://resolve?domain=x")
	if err != nil || len(kb.InlineKeyboard) != 2 || len(kb.InlineKeyboard[0]) != 2 || kb.InlineKeyboard[0][1].Text != "客服" {
		t.Fatalf("%v %v", kb, err)
	}
	if _, err := ParseButtons("坏按钮 https://a.com"); err == nil {
		t.Fatal("want error")
	}
	if _, err := ParseButtons("x - javascript:alert(1)"); err == nil {
		t.Fatal("want error")
	}
}

func TestAdDue(t *testing.T) {
	now := time.Date(2026, 10, 18, 9, 0, 10, 0, time.Local)
	a := &model.Ad{Enabled: true, Times: "09:00,21:00"}
	if ok, key := adDue(a, now); !ok || key != "2026-10-18 09:00" {
		t.Fatal("times due")
	}
	a.LastKey = "2026-10-18 09:00"
	if ok, _ := adDue(a, now); ok {
		t.Fatal("dup")
	}
	last := now.Add(-119 * time.Minute)
	b := &model.Ad{Enabled: true, Interval: 120, LastRunAt: &last}
	if ok, _ := adDue(b, now); ok {
		t.Fatal("early")
	}
	last = now.Add(-120 * time.Minute)
	if ok, _ := adDue(b, now); !ok {
		t.Fatal("interval due")
	}
	if _, i, err := parseAdWhen("2h"); err != nil || i != 120 {
		t.Fatal("2h")
	}
	if _, _, err := parseAdWhen("5m"); err == nil {
		t.Fatal("too short")
	}
}

func TestMarkdownUnderline(t *testing.T) {
	if got := MarkdownToHTML("++下划线++ **粗**"); got != "<u>下划线</u> <b>粗</b>" {
		t.Fatal(got)
	}
}

func TestAdDueCatchUp(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	created := time.Date(2026, 10, 1, 0, 0, 0, 0, loc)
	a := &model.Ad{Enabled: true, Times: "09:00", CreatedAt: created}
	// 定时循环被卡住，09:03 才跑到：仍然要补发
	if ok, key := adDue(a, time.Date(2026, 10, 18, 9, 3, 0, 0, loc)); !ok || key != "2026-10-18 09:00" {
		t.Fatalf("catch-up: %v %s", ok, key)
	}
	// 超过补发窗口不再发
	if ok, _ := adDue(a, time.Date(2026, 10, 18, 9, 30, 0, 0, loc)); ok {
		t.Fatal("too late")
	}
	// 09:05 新建的 09:00 广告当天不补发
	b := &model.Ad{Enabled: true, Times: "09:00", CreatedAt: time.Date(2026, 10, 18, 9, 5, 0, 0, loc)}
	if ok, _ := adDue(b, time.Date(2026, 10, 18, 9, 6, 0, 0, loc)); ok {
		t.Fatal("created after slot")
	}
}
