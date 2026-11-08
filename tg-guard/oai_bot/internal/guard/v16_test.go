package guard

import (
	"testing"
	"time"

	"github.com/go-telegram/bot/models"
)

func TestCommandPerm(t *testing.T) {
	cases := map[string]perm{"ban": permRestrict, "mute": permRestrict, "lock": permRestrict, "del": permDelete, "purge": permDelete,
		"pin": permPin, "warns": permAny, "stats": permAny, "fban": permAny, "joinfed": permAny}
	for cmd, want := range cases {
		if got := commandPerm(cmd); got != want {
			t.Errorf("%s: got %v want %v", cmd, got, want)
		}
	}
}

func TestDupMultiOnlyForPromo(t *testing.T) {
	d := newDupTracker()
	thanks := "谢谢群主大大分享的资源太棒了"
	// 三个老成员发同一句感谢，不是广告波
	for uid := int64(1); uid <= 3; uid++ {
		if _, hit := d.hit(-1, uid, int(uid), thanks, false); hit {
			t.Fatalf("普通感谢被判成广告波（第 %d 人）", uid)
		}
	}
	// 带联系方式的内容，三人即触发
	ad := "加我微信abc12345日结五百稳赚不赔"
	var hit bool
	for uid := int64(10); uid <= 12; uid++ {
		_, hit = d.hit(-1, uid, int(uid), ad, promoLike(&models.Message{Text: ad}))
	}
	if !hit {
		t.Fatal("带联系方式的广告波没有触发")
	}
	// 同一人重复 3 次总会触发
	for i := 0; i < 3; i++ {
		_, hit = d.hit(-2, 99, 100+i, thanks, false)
	}
	if !hit {
		t.Fatal("同一人刷屏没有触发")
	}
}

func TestDueHM(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	at := func(h, m int) time.Time { return time.Date(2026, 11, 1, h, m, 0, 0, loc) }
	if !dueHM(at(9, 0), "09:00") || !dueHM(at(9, 9), "09:00") {
		t.Fatal("窗口内应该到期")
	}
	if dueHM(at(8, 59), "09:00") || dueHM(at(9, 10), "09:00") {
		t.Fatal("窗口外不应到期")
	}
}
