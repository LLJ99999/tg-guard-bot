package guard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseUSDTQuery(t *testing.T) {
	cases := []struct {
		in     string
		amount float64
		toCNY  bool
		ok     bool
	}{
		{"@GuardBot 100u", 100, true, true},
		{"@guardbot 1U", 1, true, true},
		{"@guardbot 2.5 usdt", 2.5, true, true},
		{"100u @guardbot", 100, true, true},
		{"@guardbot 500r", 500, false, true},
		{"@guardbot 1,000元", 1000, false, true},
		{"@guardbot 300块", 300, false, true},
		{"@guardbot u价", 0, false, true},
		{"@guardbot 汇率", 0, false, true},
		{"100u", 0, false, false},
		{"@guardbot 你好", 0, false, false},
		{"@otherbot 100u", 0, false, false},
	}
	for _, c := range cases {
		a, to, ok := parseUSDTQuery(c.in, "GuardBot")
		if ok != c.ok || a != c.amount || to != c.toCNY {
			t.Errorf("%q => %v %v %v, want %v %v %v", c.in, a, to, ok, c.amount, c.toCNY, c.ok)
		}
	}
}

func TestUSDTQuoteAndText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ TradeType string }
		json.NewDecoder(r.Body).Decode(&req)
		if req.TradeType == "BUY" {
			w.Write([]byte(`{"data":[{"adv":{"price":"6.60"}},{"adv":{"price":"6.56"}}]}`))
			return
		}
		w.Write([]byte(`{"data":[{"adv":{"price":"6.61"}},{"adv":{"price":"6.58"}}]}`))
	}))
	defer srv.Close()
	c := newUSDTClient()
	c.url = srv.URL
	q, err := c.quote(context.Background())
	if err != nil || q.Buy != 6.56 || q.Sell != 6.61 {
		t.Fatalf("quote %+v %v", q, err)
	}
	s := usdtReplyText(q, 1000, true)
	for _, want := range []string{"最低购买价：<b>6.56</b>", "最高出售价：<b>6.61</b>", "买入约需 <b>6,560.00</b> 元", "卖出约得 <b>6,610.00</b> 元", "币安 C2C"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in %s", want, s)
		}
	}
	s = usdtReplyText(q, 661, false)
	if !strings.Contains(s, "需卖出约 <b>100.00</b> U") {
		t.Error(s)
	}
}
