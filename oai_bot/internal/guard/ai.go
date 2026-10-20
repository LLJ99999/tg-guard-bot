package guard

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"oai_bot/internal/config"
)

// aiClient 调用 OpenAI 兼容接口判断消息是否为垃圾广告。只用于新人的前几条消息，控制成本
type aiClient struct {
	mu   sync.RWMutex
	cfg  config.AI
	http *http.Client
}

func (a *aiClient) conf() config.AI {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.cfg
}

func (a *aiClient) set(c config.AI) {
	a.mu.Lock()
	a.cfg = c
	a.mu.Unlock()
}

type aiVerdict struct {
	Spam       bool    `json:"spam"`
	Category   string  `json:"category"`
	Confidence float64 `json:"confidence"`
	Reason     string  `json:"reason"`
}

const aiSystemPrompt = `你是 Telegram 中文群的反垃圾审核员。判断一条群消息是否属于垃圾信息。

属于垃圾（spam=true）：
- 广告引流：拉人进群/频道、"私聊我""加我""看我简介/主页"、留微信/QQ/TG 号、推广机器人
- 博彩、色情、约炮、裸聊；诈骗、刷单兼职、日结高薪、跑分、洗钱、收卡
- 虚假投资：币圈喊单、带单、稳赚、USDT 搬砖、空投骗局
- 贩卖账号/会员/卡密/接码/代实名/翻墙节点（不是正常讨论）、恶意或钓鱼链接
- 用谐音、拆字、符号、表情、繁体规避审核的上述内容

不属于垃圾（spam=false）：正常聊天、提问、技术讨论、吐槽、骂人、表情、分享正常网站或教程、讨论价格。
拿不准时判 false，宁可放过不要误杀。新人第一条消息就推广、带联系方式的，置信度要高。

只输出一个 JSON，不要任何其它文字：
{"spam":true或false,"category":"广告/博彩/色情/诈骗/投资/贩卖/正常","confidence":0到1之间的小数,"reason":"不超过20字"}`

var thinkRe = regexp.MustCompile(`(?s)<think>.*?</think>`)

func newAIClient(c config.AI) *aiClient {
	t := c.TimeoutSec
	if t <= 0 {
		t = 15
	}
	c.BaseURL = normBaseURL(c.BaseURL)
	return &aiClient{cfg: c, http: &http.Client{Timeout: time.Duration(t) * time.Second}}
}

func (a *aiClient) enabled() bool { return a.conf().Enabled() }

// check 返回判定结果；接口出错时返回 nil（按正常消息处理）
func (a *aiClient) check(ctx context.Context, name, text string, newbie bool, examples string) (*aiVerdict, error) {
	cfg := a.conf()
	if !cfg.Enabled() {
		return nil, nil
	}
	if r := []rune(text); len(r) > 1500 {
		text = string(r[:1500])
	}
	who := "老成员"
	if newbie {
		who = "刚进群的新人"
	}
	body, _ := json.Marshal(map[string]any{
		"model":       cfg.Model,
		"temperature": 0,
		"max_tokens":  300,
		"messages": []map[string]string{
			{"role": "system", "content": aiSystemPrompt + examples},
			{"role": "user", "content": fmt.Sprintf("发送者：%s（%s）\n消息：\n%s", name, who, text)},
		},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	resp, err := a.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ai http %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}
	var r struct {
		Choices []struct {
			Message struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &r); err != nil || len(r.Choices) == 0 {
		return nil, fmt.Errorf("ai bad response: %s", truncate(string(raw), 200))
	}
	out := r.Choices[0].Message.Content
	if strings.TrimSpace(out) == "" { // 部分推理模型把结果放在 reasoning_content
		out = r.Choices[0].Message.ReasoningContent
	}
	return parseVerdict(out)
}

// parseVerdict 从模型输出里取出第一个 JSON 对象（兼容带 ```json 包裹或前后有废话的输出）
func parseVerdict(s string) (*aiVerdict, error) {
	s = thinkRe.ReplaceAllString(s, "")
	i, j := strings.Index(s, "{"), strings.LastIndex(s, "}")
	if i < 0 || j <= i {
		return nil, fmt.Errorf("ai: no json in %q", truncate(s, 100))
	}
	var v aiVerdict
	if err := json.Unmarshal([]byte(s[i:j+1]), &v); err != nil {
		return nil, err
	}
	return &v, nil
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// AITest 面板里测试 AI 审核：返回模型的判定或错误
func (g *Guard) AITest(ctx context.Context, text string) (map[string]any, error) {
	start := time.Now()
	if x, sc := g.matchSample(text, "spam"); x != nil && sc >= 0.8 {
		return map[string]any{"spam": true, "category": "已学习样本", "confidence": sc, "reason": fmt.Sprintf("和广告样本 #%d 相似，不调用 AI 直接处理", x.ID),
			"threshold": 0.8, "hit": true, "model": "样本匹配", "ms": time.Since(start).Milliseconds()}, nil
	}
	if x, sc := g.matchSample(text, "ham"); x != nil && sc >= 0.9 {
		return map[string]any{"spam": false, "category": "已学习样本", "confidence": sc, "reason": fmt.Sprintf("和误封样本 #%d 相似，直接放行", x.ID),
			"threshold": 0.9, "hit": false, "model": "样本匹配", "ms": time.Since(start).Milliseconds()}, nil
	}
	if !g.ai.enabled() {
		return nil, fmt.Errorf("未配置 ai（base_url / api_key / model）")
	}
	v, err := g.ai.check(ctx, "测试用户", text, true, g.aiExamples())
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"spam": v.Spam, "category": v.Category, "confidence": v.Confidence, "reason": v.Reason,
		"threshold": g.ai.conf().Threshold, "hit": v.Spam && v.Confidence >= g.ai.conf().Threshold,
		"model": g.ai.conf().Model, "ms": time.Since(start).Milliseconds(),
	}, nil
}

// normBaseURL 兼容用户填 https://x/v1/、https://x/v1/chat/completions
func normBaseURL(u string) string {
	u = strings.TrimSpace(u)
	u = strings.TrimSuffix(u, "/")
	u = strings.TrimSuffix(u, "/chat/completions")
	return strings.TrimSuffix(u, "/")
}

const aiSettingKey = "ai"

// aiSetting 面板里保存的 AI 配置（存数据库，优先于配置文件）
type aiSetting struct {
	BaseURL   string  `json:"base_url"`
	APIKey    string  `json:"api_key"`
	Model     string  `json:"model"`
	Threshold float64 `json:"threshold"`
}

// loadAISetting 启动时用数据库里的设置覆盖配置文件
func (g *Guard) loadAISetting() {
	raw := g.st.Setting(aiSettingKey)
	if raw == "" {
		return
	}
	var s aiSetting
	if json.Unmarshal([]byte(raw), &s) != nil {
		return
	}
	c := g.ai.conf()
	c.BaseURL, c.APIKey, c.Model = normBaseURL(s.BaseURL), s.APIKey, s.Model
	if s.Threshold > 0 {
		c.Threshold = s.Threshold
	}
	g.ai.set(c)
}

func maskKey(k string) string {
	if k == "" {
		return ""
	}
	if r := []rune(k); len(r) > 8 {
		return string(r[:3]) + "…" + string(r[len(r)-4:])
	}
	return "已设置"
}

// AIConfig 面板读取当前 AI 配置（密钥打码）
func (g *Guard) AIConfig() map[string]any {
	c := g.ai.conf()
	src := "config"
	if g.st.Setting(aiSettingKey) != "" {
		src = "panel"
	}
	return map[string]any{"base_url": c.BaseURL, "api_key": maskKey(c.APIKey), "has_key": c.APIKey != "",
		"model": c.Model, "threshold": c.Threshold, "enabled": c.Enabled(), "source": src}
}

// SetAIConfig 面板保存 AI 配置，立即生效；apiKey 为 nil 表示不改密钥，空字符串表示清空
func (g *Guard) SetAIConfig(baseURL string, apiKey *string, model string, threshold float64) error {
	baseURL = normBaseURL(baseURL)
	if baseURL != "" && !strings.HasPrefix(baseURL, "https://") && !strings.HasPrefix(baseURL, "http://") {
		return fmt.Errorf("接口地址要以 http:// 或 https:// 开头")
	}
	if threshold < 0.5 || threshold > 1 {
		return fmt.Errorf("判定阈值需在 0.5 ~ 1 之间")
	}
	c := g.ai.conf()
	key := c.APIKey
	if apiKey != nil {
		key = strings.TrimSpace(*apiKey)
	}
	raw, _ := json.Marshal(aiSetting{BaseURL: baseURL, APIKey: key, Model: strings.TrimSpace(model), Threshold: threshold})
	if err := g.st.SetSetting(aiSettingKey, string(raw)); err != nil {
		return err
	}
	c.BaseURL, c.APIKey, c.Model, c.Threshold = baseURL, key, strings.TrimSpace(model), threshold
	g.ai.set(c)
	return nil
}

// AIModels 读取接口的模型列表（/models），方便面板里选
func (g *Guard) AIModels(ctx context.Context) ([]string, error) {
	c := g.ai.conf()
	if c.BaseURL == "" || c.APIKey == "" {
		return nil, fmt.Errorf("先填写接口地址和密钥并保存")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	resp, err := g.ai.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}
	var r struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("返回格式不对：%s", truncate(string(raw), 100))
	}
	out := make([]string, 0, len(r.Data))
	for _, m := range r.Data {
		out = append(out, m.ID)
	}
	return out, nil
}
