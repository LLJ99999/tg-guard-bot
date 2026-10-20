package guard

import "testing"

func TestSimilarity(t *testing.T) {
	cases := []struct {
		a, b string
		hit  bool
	}{
		{"洗米日赚八千", "洗 米 日 赚 八 千！！加我", true},
		{"洗米日赚八千", "兄弟们 洗米日赚八千 私聊", true},
		{"高价收卡日结私聊我飞机号abc", "高价收卡 日结 私聊我 飞机号 abc123", true},
		{"洗米日赚八千", "今天中午吃米饭还是面条", false},
		{"请问中转站怎么充值", "洗米日赚八千", false},
		{"洗米日赚八千，私聊带你", "兄弟们 洗 米 日 赚 八 千！！", true},
		{"今天天气不错适合出去玩", "今天天气不错，我在家写代码修中转站的bug", false},
		{"ＵＳＤＴ搬砖稳赚不赔", "usdt搬砖 稳赚不赔", true},
	}
	for _, c := range cases {
		s := similarity(normText(c.a), normText(c.b))
		if (s >= 0.8) != c.hit {
			t.Errorf("%q vs %q = %.2f", c.a, c.b, s)
		}
	}
}
