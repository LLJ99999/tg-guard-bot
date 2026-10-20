package guard

import (
	"bytes"
	"os"

	"github.com/dchest/captcha"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"
)

func TestParseCommand(t *testing.T) {
	cases := []struct{ in, name, arg string }{
		{"/ban @x spam", "ban", "@x spam"},
		{"/Mute@oai_bot 2h", "mute", "2h"},
		{"/ask@astrbot hi", "", ""},
		{"hello", "", ""},
	}
	for _, c := range cases {
		n, a := parseCommand(c.in, "oai_bot")
		if n != c.name || a != c.arg {
			t.Errorf("%q => %q %q", c.in, n, a)
		}
	}
}

func TestParseDur(t *testing.T) {
	if d, ok := parseDur("2h"); !ok || d != 2*time.Hour {
		t.Fatal("2h")
	}
	if _, ok := parseDur("abc"); ok {
		t.Fatal("abc should fail")
	}
}

func TestFlood(t *testing.T) {
	f := newFloodTracker()
	for i := 1; i <= 3; i++ {
		if ids := f.hit("k", i, 3, time.Second); ids != nil {
			t.Fatalf("triggered too early at %d", i)
		}
	}
	if ids := f.hit("k", 4, 3, time.Second); len(ids) != 4 {
		t.Fatalf("want 4 ids, got %v", ids)
	}
}

func TestHasLink(t *testing.T) {
	if !hasLink(&models.Message{Text: "加我 t.me/xxx"}) {
		t.Fatal("t.me link")
	}
	if hasLink(&models.Message{Text: "今天天气不错"}) {
		t.Fatal("plain text")
	}
	if !hasLink(&models.Message{Text: "@spam", Entities: []models.MessageEntity{{Type: models.MessageEntityTypeMention}}}) {
		t.Fatal("mention")
	}
}

func TestParseVerdict(t *testing.T) {
	v, err := parseVerdict("```json\n{\"spam\":true,\"category\":\"博彩\",\"confidence\":0.93,\"reason\":\"x\"}\n```")
	if err != nil || !v.Spam || v.Confidence != 0.93 {
		t.Fatalf("%+v %v", v, err)
	}
}

func TestInWindow(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 1, 1, h, m, 0, 0, time.UTC) }
	if !inWindow(at(23, 45), "23:30", "07:00") || !inWindow(at(3, 0), "23:30", "07:00") {
		t.Fatal("cross-midnight window")
	}
	if inWindow(at(12, 0), "23:30", "07:00") || inWindow(at(7, 0), "23:30", "07:00") {
		t.Fatal("outside window")
	}
	if !inWindow(at(13, 0), "12:00", "14:00") {
		t.Fatal("same-day window")
	}
}

func TestDupWave(t *testing.T) {
	d := newDupTracker()
	ad := "加我微信 abc123 日赚五百 稳赚不赔"
	if _, hit := d.hit(1, 10, 1, ad); hit {
		t.Fatal("first")
	}
	if _, hit := d.hit(1, 11, 2, "加我微信ABC123，日赚五百!!稳赚不赔"); hit {
		t.Fatal("second")
	}
	msgs, hit := d.hit(1, 12, 3, ad)
	if !hit || len(msgs) != 3 {
		t.Fatalf("third user should trigger, got %v %v", hit, msgs)
	}
	if _, hit := d.hit(1, 10, 4, "好的"); hit {
		t.Fatal("short text ignored")
	}
}

func TestRaid(t *testing.T) {
	r := newRaidTracker()
	for i := 0; i < 5; i++ {
		r.hit(1)
	}
	if n := r.hit(1); n != 6 {
		t.Fatalf("n=%d", n)
	}
}

func TestSpamName(t *testing.T) {
	if !spamName("免费VPN t.me/xxx", nil) || !spamName("找我 @abc", nil) || !spamName("博彩小王", []string{"博彩"}) {
		t.Fatal("should flag")
	}
	if spamName("张三", []string{"博彩"}) {
		t.Fatal("normal name")
	}
}

func TestCycle(t *testing.T) {
	if cycle(5, 0, 3, 5, 10) != 10 || cycle(10, 0, 3, 5, 10) != 0 || cycle(7, 0, 3) != 0 {
		t.Fatal("cycle")
	}
}

func TestCommandMenu(t *testing.T) {
	if len(adminMenu)+len(memberMenu) > 10 {
		t.Fatalf("菜单太长：%d", len(adminMenu)+len(memberMenu))
	}
	for _, c := range []string{"tban", "lock", "filter", "save", "schedule", "settings", "panel", "ban"} {
		if !adminOnly[c] {
			t.Fatalf("%s 应该仍然可用", c)
		}
	}
	if !addressedToMe("/help@oai_bot", "OAI_bot") || addressedToMe("/help", "oai_bot") || addressedToMe("/help@astr_bot x", "oai_bot") {
		t.Fatal("addressedToMe")
	}
}

func TestParseVerdictThink(t *testing.T) {
	v, err := parseVerdict("<think>用户说 {不是} 广告</think>\n{\"spam\":false,\"category\":\"正常\",\"confidence\":0.1,\"reason\":\"闲聊\"}")
	if err != nil || v.Spam {
		t.Fatal(v, err)
	}
}

func TestCaptchaImage(t *testing.T) {
	var buf bytes.Buffer
	if _, err := captcha.NewImage("x", []byte{1, 9, 4, 7}, 300, 100).WriteTo(&buf); err != nil || buf.Len() < 1000 {
		t.Fatal(err, buf.Len())
	}
	if p := os.Getenv("CAPTCHA_OUT"); p != "" {
		white, _ := captchaPNG([]byte{3, 8, 0, 6})
		os.WriteFile(p, white.Bytes(), 0o644)
	}
	if len(keypad(-100).InlineKeyboard) != 4 || imageCaption("5 分钟", "12") == "" {
		t.Fatal("keypad")
	}
}

func TestNewChecks(t *testing.T) {
	body, mk := parseButtons("欢迎 {name}\n看群规\n群规 - https://t.me/x/1 | 官网 - https://a.com\n频道 - https://t.me/c")
	if body != "欢迎 {name}\n看群规" || mk == nil || len(mk.InlineKeyboard) != 2 || len(mk.InlineKeyboard[0]) != 2 {
		t.Fatalf("%q %+v", body, mk)
	}
	if b, mk := parseButtons("普通 - 文字"); mk != nil || b != "普通 - 文字" {
		t.Fatal("误识别按钮")
	}
	if !foreignText("Привет, как дела? Заходи в канал") || foreignText("你好 hello 这是 Привет") || foreignText("hello world everyone") {
		t.Fatal("foreign")
	}
	if !hasInvite(&models.Message{Text: "来 https://t.me/+AbCdEf"}) || hasInvite(&models.Message{Text: "https://t.me/durov"}) {
		t.Fatal("invite")
	}
	if !spamWord("小美 加V 123") || spamWord("张三 Zhang") {
		t.Fatal("spamWord")
	}
}
