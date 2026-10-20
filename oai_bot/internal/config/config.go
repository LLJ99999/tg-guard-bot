// Package config 加载启动配置。各群的开关存在数据库里，用群内命令修改，见 guard 包。
package config

import (
	"fmt"
	"slices"
	"strings"
	"time"
	_ "time/tzdata" // 内置时区数据，精简镜像里也能用

	"github.com/spf13/viper"
)

type Config struct {
	Bot     Bot     `mapstructure:"bot"`
	Web     Web     `mapstructure:"web"`
	Admin   Admin   `mapstructure:"admin"`
	Captcha Captcha `mapstructure:"captcha"`
	CAS     CAS     `mapstructure:"cas"`
	AI      AI      `mapstructure:"ai"`
	DB      DB      `mapstructure:"db"`
}

type Bot struct {
	ObserveOnly bool    `mapstructure:"observe_only"`
	Token       string  `mapstructure:"token"`
	Mode        string  `mapstructure:"mode"`      // polling | webhook
	OwnerIDs    []int64 `mapstructure:"owner_ids"` // 机器人主人：可在任意群执行管理命令、全局封禁
	// AllowedGroups 允许使用的群 ID，留空表示任何群都可以拉机器人进去
	AllowedGroups []int64 `mapstructure:"allowed_groups"`
	// LogChatID 操作日志发往的频道 / 群，0 表示不记录
	LogChatID int64 `mapstructure:"log_chat_id"`
	// Timezone 夜间模式、定时消息、统计使用的时区
	Timezone string `mapstructure:"timezone"`
}

// Admin 内网管理接口（给 AstrBot 面板插件用），listen 为空则不开启
type Admin struct {
	Listen string `mapstructure:"listen"`
	Token  string `mapstructure:"token"`
}

type Web struct {
	Listen    string `mapstructure:"listen"`
	PublicURL string `mapstructure:"public_url"`
}

type Captcha struct {
	Turnstile Turnstile `mapstructure:"turnstile"`
	Cap       Cap       `mapstructure:"cap"`
}

type Turnstile struct {
	SiteKey string `mapstructure:"site_key"`
	Secret  string `mapstructure:"secret"`
}

type Cap struct {
	URL     string `mapstructure:"url"`
	SiteKey string `mapstructure:"site_key"`
	Secret  string `mapstructure:"secret"`
}

// CAS Combot Anti-Spam 共享黑名单 https://cas.chat
type CAS struct {
	Enabled bool   `mapstructure:"enabled"`
	API     string `mapstructure:"api"`
}

// AI 任意 OpenAI 兼容接口（可以和 AstrBot 用同一个模型 / 中转）
type AI struct {
	BaseURL string `mapstructure:"base_url"` // 如 https://api.openai.com/v1
	APIKey  string `mapstructure:"api_key"`
	Model   string `mapstructure:"model"`
	// Threshold 置信度达到多少判定为垃圾，0~1
	Threshold float64 `mapstructure:"threshold"`
	// TimeoutSec 单次请求超时
	TimeoutSec int `mapstructure:"timeout_sec"`
}

func (a AI) Enabled() bool { return a.BaseURL != "" && a.APIKey != "" && a.Model != "" }

type DB struct {
	Path string `mapstructure:"path"`
}

const (
	ModePolling = "polling"
	ModeWebhook = "webhook"
)

// Load 读取配置；环境变量（前缀 OAI_，如 OAI_BOT_TOKEN、OAI_AI_API_KEY）可覆盖
func Load(path string) (*Config, error) {
	v := viper.New()
	v.SetConfigFile(path)
	v.SetDefault("bot.mode", ModePolling)
	v.SetDefault("bot.timezone", "Asia/Shanghai")
	v.SetDefault("web.listen", ":8080")
	v.SetDefault("db.path", "data/oai_bot.db")
	v.SetDefault("cas.enabled", true)
	v.SetDefault("cas.api", "https://api.cas.chat/check")
	v.SetDefault("ai.threshold", 0.8)
	v.SetDefault("ai.timeout_sec", 15)
	v.SetEnvPrefix("OAI")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	if err := v.ReadInConfig(); err != nil {
		return nil, err
	}
	var c Config
	if err := v.Unmarshal(&c); err != nil {
		return nil, err
	}
	c.Web.PublicURL = strings.TrimRight(c.Web.PublicURL, "/")
	c.Captcha.Cap.URL = strings.TrimRight(c.Captcha.Cap.URL, "/")
	c.AI.BaseURL = strings.TrimRight(c.AI.BaseURL, "/")
	return &c, c.validate()
}

func (c *Config) validate() error {
	switch {
	case c.Bot.Token == "":
		return fmt.Errorf("bot.token 未配置")
	case c.Web.PublicURL != "" && !strings.HasPrefix(c.Web.PublicURL, "https://"):
		return fmt.Errorf("web.public_url 必须是 https:// 地址（Telegram Mini App 要求）")
	case c.Bot.Mode != ModePolling && c.Bot.Mode != ModeWebhook:
		return fmt.Errorf("bot.mode 只能是 polling 或 webhook")
	case c.Bot.Mode == ModeWebhook && c.Web.PublicURL == "":
		return fmt.Errorf("webhook 模式需要配置 web.public_url")
	}
	return nil
}

// Location 配置的时区，无效时回退 UTC+8
func (c *Config) Location() *time.Location {
	if loc, err := time.LoadLocation(c.Bot.Timezone); err == nil {
		return loc
	}
	return time.FixedZone("CST", 8*3600)
}

func (c *Config) IsOwner(id int64) bool { return slices.Contains(c.Bot.OwnerIDs, id) }

// GroupAllowed 群是否在白名单内（白名单为空时全部允许）
func (c *Config) GroupAllowed(id int64) bool {
	return len(c.Bot.AllowedGroups) == 0 || slices.Contains(c.Bot.AllowedGroups, id)
}
