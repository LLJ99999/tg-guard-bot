package guard

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-telegram/bot/models"
)

// U 价查询：群里 @机器人 发「100u」「500r」或「u价」。
// 价格取币安 C2C（USDT / CNY）：
//   - 当前最低购买价：「购买 USDT」页里最便宜的广告价（买 U 要付多少元）
//   - 当前最高出售价：「出售 USDT」页里出价最高的广告价（卖 U 能拿多少元）

const (
	usdtAPI      = "https://p2p.binance.com/bapi/c2c/v2/friendly/c2c/adv/search"
	usdtCacheTTL = time.Minute
	usdtRows     = 10
	usdtBuyURL   = "https://p2p.binance.com/zh-CN/trade/all-payments/USDT?fiat=CNY"
	usdtSellURL  = "https://p2p.binance.com/zh-CN/trade/sell/USDT?fiat=CNY"
)

// usdtQueryRe 金额 + 单位。u / usdt 换成元；r / rmb / cny / 元 / 块 换成 U
var usdtQueryRe = regexp.MustCompile(`(?i)^(\d+(?:\.\d+)?)\s*(usdt|u|rmb|cny|r|元|块|人民币)$`)

// usdtPriceWords 只问价格、不带金额
var usdtPriceWords = map[string]bool{"u价": true, "汇率": true, "价格": true, "u": true, "usdt": true, "币价": true}

type usdtQuote struct {
	Buy  float64   // 当前最低购买价
	Sell float64   // 当前最高出售价
	At   time.Time // 获取时间
}

type usdtClient struct {
	mu   sync.Mutex
	last *usdtQuote
	http *http.Client
	url  string
}

func newUSDTClient() *usdtClient {
	return &usdtClient{http: &http.Client{Timeout: 8 * time.Second}, url: usdtAPI}
}

// quote 返回缓存 1 分钟内的价格；请求失败时退回 10 分钟内的旧价格
func (c *usdtClient) quote(ctx context.Context) (*usdtQuote, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.last != nil && time.Since(c.last.At) < usdtCacheTTL {
		return c.last, nil
	}
	q, err := c.fetchBoth(ctx)
	if err != nil {
		if c.last != nil && time.Since(c.last.At) < 10*time.Minute {
			slog.Warn("usdt price, using cached", "err", err)
			return c.last, nil
		}
		return nil, err
	}
	c.last = q
	return q, nil
}

func (c *usdtClient) fetchBoth(ctx context.Context) (*usdtQuote, error) {
	var (
		wg              sync.WaitGroup
		buy, sell       []float64
		buyErr, sellErr error
	)
	wg.Add(2)
	go func() { defer wg.Done(); buy, buyErr = c.fetch(ctx, "BUY") }()
	go func() { defer wg.Done(); sell, sellErr = c.fetch(ctx, "SELL") }()
	wg.Wait()
	if buyErr != nil {
		return nil, buyErr
	}
	if sellErr != nil {
		return nil, sellErr
	}
	q := &usdtQuote{At: time.Now()}
	for _, p := range buy {
		if q.Buy == 0 || p < q.Buy {
			q.Buy = p
		}
	}
	for _, p := range sell {
		if p > q.Sell {
			q.Sell = p
		}
	}
	return q, nil
}

// fetch 取一页广告价格。tradeType：BUY 购买 USDT 页，SELL 出售 USDT 页
func (c *usdtClient) fetch(ctx context.Context, tradeType string) ([]float64, error) {
	body, _ := json.Marshal(map[string]any{
		"asset": "USDT", "fiat": "CNY", "tradeType": tradeType, "page": 1, "rows": usdtRows,
		"payTypes": []string{}, "publisherType": nil,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("binance %s status %d", tradeType, resp.StatusCode)
	}
	var r struct {
		Data []struct {
			Adv struct {
				Price string `json:"price"`
			} `json:"adv"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	var ps []float64
	for _, d := range r.Data {
		if p, err := strconv.ParseFloat(d.Adv.Price, 64); err == nil && p > 0 {
			ps = append(ps, p)
		}
	}
	if len(ps) == 0 {
		return nil, fmt.Errorf("binance %s: no ads", tradeType)
	}
	return ps, nil
}

// parseUSDTQuery 从 @机器人 的消息里取出查询。ok=false 表示不是 U 价查询。
// amount=0 表示只问价格；toCNY=true 表示金额是 U，要换成元
func parseUSDTQuery(text, botName string) (amount float64, toCNY bool, ok bool) {
	if botName == "" {
		return 0, false, false
	}
	at := "@" + strings.ToLower(botName)
	low := strings.ToLower(strings.TrimSpace(text))
	if !strings.Contains(low, at) {
		return 0, false, false
	}
	s := strings.TrimSpace(strings.ReplaceAll(low, at, ""))
	s = strings.ReplaceAll(s, ",", "")
	s = strings.ReplaceAll(s, "，", "")
	s = strings.Join(strings.Fields(s), "")
	if usdtPriceWords[s] {
		return 0, false, true
	}
	mm := usdtQueryRe.FindStringSubmatch(s)
	if mm == nil {
		return 0, false, false
	}
	v, err := strconv.ParseFloat(mm[1], 64)
	if err != nil || v <= 0 || v > 1e10 {
		return 0, false, false
	}
	switch mm[2] {
	case "u", "usdt":
		return v, true, true
	default:
		return v, false, true
	}
}

// fmtMoney 千分位，保留两位小数
func fmtMoney(v float64) string {
	s := strconv.FormatFloat(v, 'f', 2, 64)
	intPart, frac, _ := strings.Cut(s, ".")
	var b strings.Builder
	for i, ch := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(ch)
	}
	return b.String() + "." + frac
}

func fmtAmount(v float64) string {
	if v == float64(int64(v)) {
		return fmtMoney(v)[:len(fmtMoney(v))-3]
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func usdtReplyText(q *usdtQuote, amount float64, toCNY bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "💱 <b>币安 C2C 实时 U 价</b>\n🟢 当前最低购买价：<b>%.2f</b> 元\n🔴 当前最高出售价：<b>%.2f</b> 元\n", q.Buy, q.Sell)
	switch {
	case amount == 0:
	case toCNY:
		fmt.Fprintf(&b, "\n<b>%s U</b>\n买入约需 <b>%s</b> 元\n卖出约得 <b>%s</b> 元\n", fmtAmount(amount), fmtMoney(amount*q.Buy), fmtMoney(amount*q.Sell))
	default:
		fmt.Fprintf(&b, "\n<b>%s 元</b>\n可买入约 <b>%s</b> U\n需卖出约 <b>%s</b> U\n", fmtAmount(amount), fmtMoney(amount/q.Buy), fmtMoney(amount/q.Sell))
	}
	fmt.Fprintf(&b, "\n数据来源：<a href=\"%s\">币安 C2C</a>（USDT/CNY）\n更新时间：%s", usdtSellURL, q.At.In(cst).Format("01-02 15:04"))
	return b.String()
}

var cst = time.FixedZone("CST", 8*3600)

// usdtQuery 处理 U 价查询，返回 true 表示已处理（不再走自动回复）
func (g *Guard) usdtQuery(ctx context.Context, m *models.Message) bool {
	if g.usdt == nil {
		return false
	}
	amount, toCNY, ok := parseUSDTQuery(msgText(m), g.tg.Me().Username)
	if !ok {
		return false
	}
	grp, err := g.st.Group(m.Chat.ID, m.Chat.Title)
	if err != nil || !grp.UsdtOn {
		return false
	}
	if !g.usdtCD.allow(fmt.Sprintf("usdt:%d:%d", m.Chat.ID, senderID(m))) {
		return true
	}
	q, err := g.usdt.quote(ctx)
	text := "暂时查不到币安价格，稍后再试。"
	if err != nil {
		slog.Warn("usdt price", "err", err)
	} else {
		text = usdtReplyText(q, amount, toCNY)
	}
	g.tg.ReplyHTML(ctx, m, text)
	return true
}
