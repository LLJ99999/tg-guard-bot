package guard

import (
	"testing"
	"time"

	"oai_bot/internal/model"
)

func TestReplyCooldown(t *testing.T) {
	c := &cooldown{d: 30 * time.Second}
	if !c.allowFor("a", time.Minute) || c.allowFor("a", time.Minute) {
		t.Fatal("一分钟内第二次应拦住")
	}
	if !c.allowFor("b", 0) || !c.allowFor("b", 0) {
		t.Fatal("0 表示不限")
	}
	if replyCooldown(0) != 30*time.Second || replyCooldown(-1) != 0 || replyCooldown(300) != 5*time.Minute {
		t.Fatal("replyCooldown")
	}
	for in, want := range map[[3]int]string{
		{0, 0, 0}:     "全群 30 秒内回 1 次",
		{300, 1, 0}:   "每人 5 分钟内回 1 次",
		{3600, 0, 50}: "全群 1 小时内回 1 次，50% 概率",
		{-1, 0, 0}:    "不限频率",
		{86400, 0, 0}: "全群 1 天内回 1 次",
	} {
		if got := freqText(in[0], in[1] == 1, in[2]); got != want {
			t.Errorf("%v: %q want %q", in, got, want)
		}
	}
	if noteIcon(&model.Note{Content: model.Content{Media: "document:x"}}) != "📄" || noteIcon(&model.Note{}) != "📝" {
		t.Fatal("noteIcon")
	}
}
