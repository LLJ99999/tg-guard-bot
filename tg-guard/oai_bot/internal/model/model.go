// Package model 持久化数据模型。
package model

import "time"

// 入群验证方式
const (
	VerifyOff         = "off"      // 不验证
	VerifyJoinRequest = "join"     // 群开启「入群需审核」，验证通过才批准（推荐，人进不来就不会打扰）
	VerifyRestrict    = "restrict" // 开放群：新人进群先禁言，验证通过解除，超时踢出
)

// 验证码类型（入群验证时让新人完成的题目）
const (
	CaptchaImage  = "image"  // 图片数字验证码 + 数字键盘（默认，类似方丈 / nmbot 的动态验证码）
	CaptchaMath   = "math"   // 算术题，选正确答案
	CaptchaEmoji  = "emoji"  // 按文字点对应的表情
	CaptchaButton = "button" // 点一下按钮（最简单，类似 Rose 默认）
	CaptchaWeb    = "web"    // Mini App 网页 + Turnstile / Cap（需要 web.public_url）
)

var CaptchaTypes = []string{CaptchaImage, CaptchaMath, CaptchaEmoji, CaptchaButton, CaptchaWeb}

// 警告达到上限后的处罚
const (
	ActionMute = "mute"
	ActionKick = "kick"
	ActionBan  = "ban"
)

// Group 每个群的设置
type Group struct {
	ID    int64  `gorm:"primaryKey;autoIncrement:false" json:"id"`
	Title string `json:"title"`
	// BotLeft 机器人已被移出该群（定时任务跳过，面板标灰，可删除）
	BotLeft bool `json:"bot_left"`

	VerifyMode    string `gorm:"default:join" json:"verify_mode"`
	VerifyTimeout int    `gorm:"default:300" json:"verify_timeout"` // 秒
	CaptchaType   string `gorm:"default:image" json:"captcha_type"`
	Welcome       string `json:"welcome"` // 验证通过后的欢迎语，支持 {name} {group}；空表示不发
	Rules         string `json:"rules"`

	FloodCount  int `gorm:"default:6" json:"flood_count"` // FloodSeconds 秒内超过 FloodCount 条判定刷屏，0 关闭
	FloodSecond int `gorm:"default:10" json:"flood_second"`

	WarnLimit  int    `gorm:"default:3" json:"warn_limit"`
	WarnAction string `gorm:"default:mute" json:"warn_action"`
	WarnExpire int    `gorm:"default:0" json:"warn_expire"` // 警告多少小时后自动失效，0 表示永久

	// 入群资料检查：昵称 / 简介里带链接、引流词、屏蔽词，直接拒绝
	ProfileCheck bool `gorm:"default:true" json:"profile_check"`

	// FedID 加入的联邦（联合封禁组），空表示未加入
	FedID string `gorm:"index" json:"fed_id"`

	// 欢迎消息
	WelcomeTTL   int  `gorm:"default:120" json:"welcome_ttl"`    // 秒，多久后自动删除，0 不删
	CleanWelcome bool `gorm:"default:true" json:"clean_welcome"` // 新欢迎发出时删除上一条
	LastWelcome  int  `json:"-"`                                 // 上一条欢迎消息，clean_welcome 时删除

	// 锁：true 表示普通成员禁止
	LockLink    bool `gorm:"default:false" json:"lock_link"`
	LockForward bool `gorm:"default:true" json:"lock_forward"` // 转发自外部频道 / 群
	LockButton  bool `gorm:"default:true" json:"lock_button"`  // 带按钮的消息（常见于广告 inline bot）
	LockViaBot  bool `gorm:"default:true" json:"lock_via_bot"`
	LockStory   bool `gorm:"default:true" json:"lock_story"`
	LockSticker bool `gorm:"default:false" json:"lock_sticker"`
	LockGIF     bool `gorm:"default:false" json:"lock_gif"`
	LockMedia   bool `gorm:"default:false" json:"lock_media"`   // 图片 / 视频 / 文件 / 音频
	LockVoice   bool `gorm:"default:false" json:"lock_voice"`   // 语音 / 圆形视频
	LockInvite  bool `gorm:"default:true" json:"lock_invite"`   // 其它群 / 频道的邀请链接（t.me/+xxx、joinchat）
	LockContact bool `gorm:"default:true" json:"lock_contact"`  // 名片 / 位置
	LockPoll    bool `gorm:"default:false" json:"lock_poll"`    // 投票
	LockGame    bool `gorm:"default:false" json:"lock_game"`    // 骰子 / 游戏
	LockForeign bool `gorm:"default:false" json:"lock_foreign"` // 外文为主的消息（阿拉伯文、西里尔文、天城文等，常见于境外广告）

	CAS bool `gorm:"default:true" json:"cas"`
	AI  bool `gorm:"default:true" json:"ai"`
	// AIAll AI 审核所有普通成员的消息（默认只审新人前几条，省钱）
	AIAll bool `gorm:"default:false" json:"ai_all"`
	// AIAction AI 判定为垃圾后的处理：ban 封禁 | mute 禁言 24 小时 | warn 删除并警告
	AIAction string `gorm:"default:ban" json:"ai_action"`
	// NewbieMsgs 新人前多少条消息走严格检查（AI 审核 + 禁止链接），0 关闭
	NewbieMsgs int `gorm:"default:5" json:"newbie_msgs"`

	// DupCheck 重复内容检测：同一段文字被多人或同一人反复发送（广告波）
	DupCheck bool `gorm:"default:true" json:"dup_check"`
	// RaidLimit 1 分钟内进群 / 申请超过多少人触发防爆群（15 分钟内拒绝所有新人），0 关闭
	RaidLimit int        `gorm:"default:15" json:"raid_limit"`
	RaidUntil *time.Time `json:"raid_until"`

	// 夜间模式：时间段内全员禁言（管理员除外），HH:MM，按配置时区
	NightOn     bool   `json:"night_on"`
	NightStart  string `gorm:"default:'00:00'" json:"night_start"`
	NightEnd    string `gorm:"default:'07:00'" json:"night_end"`
	NightActive bool   `json:"night_active"`
	NightPerms  string `json:"night_perms"` // 开启前的群默认权限（JSON），结束时恢复

	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	BotAdmins    string     `json:"bot_admins"` // 管理员白名单：空格分隔的用户 ID，视同管理员
	BioScan      bool       `gorm:"default:false" json:"bio_scan"`
	BioScanHours int        `gorm:"default:24" json:"bio_scan_hours"` // 扫描间隔（小时）
	BioScanMin   int        `gorm:"default:3" json:"bio_scan_min"`    // 同一链接至少多少人才报告
	BioScanAt    *time.Time `json:"bio_scan_at"`                      // 上次扫描时间

	// ---------- v1.7 ----------
	WelcomeMedia  string `json:"welcome_media"`  // 欢迎语配的图片 / 动图 / 视频：photo:<file_id> 等，空表示纯文字
	WelcomeThread int    `json:"welcome_thread"` // 话题群：欢迎语发到哪个话题，0 表示 General
	BotThread     int    `json:"bot_thread"`     // 话题群：机器人提示（警告、处罚通知）发到哪个话题，0 跟随原消息
	NewbieHours   int    `json:"newbie_hours"`   // 新人进群后多少小时内不能发图片视频文件贴纸、不能发链接，0 关闭

	PointsOn      bool   `json:"points_on"` // 签到积分
	CheckinPoints int    `gorm:"default:10" json:"checkin_points"`
	StreakBonus   int    `gorm:"default:2" json:"streak_bonus"`    // 连续签到每天额外加，封顶 7 天
	MsgPoints     int    `json:"msg_points"`                       // 每条有效发言加几分（同一人 1 分钟内只算一次）
	MsgPointsCap  int    `gorm:"default:50" json:"msg_points_cap"` // 每天发言积分上限
	RankPost      string `json:"rank_post"`                        // 自动发活跃榜：off / daily / weekly
	RankAt        string `gorm:"default:'21:00'" json:"rank_at"`
	RankLast      string `json:"-"`

	UsdtOn bool `gorm:"default:true" json:"usdt_on"` // 群里 @机器人 查 U 价（币安 C2C 出售价）

	InviteOn     bool   `json:"invite_on"`                         // 邀请统计（成员 /link 领取专属邀请链接）
	InvitePoints int    `json:"invite_points"`                     // 邀请的人通过验证后，给邀请人加几分
	InviteNotify bool   `gorm:"default:true" json:"invite_notify"` // 被邀请的人通过验证时私聊通知邀请人
	InviteText   string `json:"invite_text"`                       // 发链接时的文案，空用默认；{link} {count} {group} {name}

	ReportAuto int `json:"report_auto"` // 同一条消息被多少个不同的人举报就先自动禁言 24 小时，0 关闭
	VoteKick   int `json:"vote_kick"`   // /votekick 需要多少票，0 关闭

	LinkRotate   int        `json:"link_rotate"` // 每多少小时换一次群主邀请链接，0 关闭
	LinkRotateAt *time.Time `json:"link_rotate_at"`
	AIImage      bool       `json:"ai_image"` // 新人发的图片也交给 AI 看（需要模型支持图片）

	// 谁能在 Telegram 私聊里管理这个群：空 = 所有有封禁权限的管理员；list = 只有 PanelAdmins 里的人（机器人主人总是可以）
	PanelMode   string `json:"panel_mode"`
	PanelAdmins string `json:"panel_admins"` // 空格分隔的用户 ID
}

// Member 某人在某群的状态
type Member struct {
	GroupID    int64      `gorm:"primaryKey;autoIncrement:false" json:"group_id"`
	UserID     int64      `gorm:"primaryKey;autoIncrement:false" json:"user_id"`
	Verified   bool       `json:"verified"`
	Newcomer   bool       `json:"newcomer"` // 机器人在群里时进群的新人（之前就在群里的老成员不算新人）
	MsgCount   int        `json:"msg_count"`
	Warns      int        `json:"warns"`
	WarnAt     *time.Time `json:"warn_at"` // 最近一次警告时间（用于警告过期）
	JoinedAt   time.Time  `json:"joined_at"`
	VerifiedAt *time.Time `json:"verified_at"`
	Left       bool       `gorm:"column:has_left" json:"left"` // 已退群（查重时跳过）
}

// User 见过的用户，用于 @username 解析
type User struct {
	ID        int64     `gorm:"primaryKey;autoIncrement:false" json:"id"`
	Username  string    `gorm:"index" json:"username"`
	FirstName string    `json:"first_name"`
	LastName  string    `json:"last_name"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Pending 等待验证的入群
type Pending struct {
	GroupID   int64     `gorm:"primaryKey;autoIncrement:false" json:"group_id"`
	UserID    int64     `gorm:"primaryKey;autoIncrement:false" json:"user_id"`
	Mode      string    `json:"mode"`
	PromptMsg int       `json:"prompt_msg"` // restrict 模式下群里的提示消息，验证后删除
	Fails     int       `json:"fails"`      // 验证失败次数
	Answer    string    `json:"-"`          // 当前题目的答案
	Input     string    `json:"-"`          // 图片验证码已输入的数字
	Refresh   int       `json:"-"`          // 换题次数
	ChalMsg   int       `json:"-"`          // 私聊里的题目消息，验证结束后删除
	ExpiresAt time.Time `gorm:"index" json:"expires_at"`
}

// Filter 屏蔽词 / 正则。命中即删除并警告
type Filter struct {
	ID      uint   `gorm:"primaryKey" json:"id"`
	GroupID int64  `gorm:"index" json:"group_id"`
	Pattern string `json:"pattern"`
	Regex   bool   `json:"regex"`
	Action  string `gorm:"default:warn" json:"action"` // 命中后：delete 只删除 | warn 删除并警告 | mute 禁言 | ban 封禁
}

// GBan 机器人主人的全局封禁（所有群生效，类似 Rose 的联合封禁）
type GBan struct {
	UserID    int64     `gorm:"primaryKey;autoIncrement:false" json:"user_id"`
	Reason    string    `json:"reason"`
	CreatedAt time.Time `json:"created_at"`
}

// Content 可复用的消息内容：优先复制原消息（支持图片、按钮、格式），失败回退文字
type Content struct {
	Text     string `json:"text"`
	FromChat int64  `json:"from_chat"`
	MsgID    int    `json:"msg_id"`
	Markdown bool   `json:"markdown"` // Text 按 Markdown 解析（面板编辑的内容）
	Media    string `json:"media"`    // 配图：photo:<file_id> 等
	Buttons  string `json:"buttons"`  // 按钮：每行「文字 - 链接」
}

// Note 笔记：#名字 或 /get 名字 取出；群里发 /notes 或「笔记目录」看目录
type Note struct {
	ID        uint   `gorm:"primaryKey" json:"id"`
	GroupID   int64  `gorm:"uniqueIndex:idx_note" json:"group_id"`
	Name      string `gorm:"uniqueIndex:idx_note" json:"name"`
	Content   `gorm:"embedded"`
	FileName  string    `json:"file_name"`  // 文件 / 音频的原文件名
	CreatedBy string    `json:"created_by"` // 谁保存的
	Views     int       `json:"views"`      // 被查看次数
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// NoteLog 谁在私聊里取过哪条笔记（笔记只在私聊发，后台能看到记录）
type NoteLog struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	GroupID   int64     `gorm:"index:idx_notelog" json:"group_id"`
	NoteID    uint      `gorm:"index" json:"note_id"`
	NoteName  string    `json:"note_name"`
	UserID    int64     `json:"user_id"`
	Name      string    `json:"name"`
	Username  string    `json:"username"`
	CreatedAt time.Time `gorm:"index:idx_notelog" json:"created_at"`
}

// AutoReply 关键词自动回复
type AutoReply struct {
	ID      uint              `gorm:"primaryKey" json:"id"`
	GroupID int64             `gorm:"uniqueIndex:idx_reply" json:"group_id"`
	Keyword string            `gorm:"uniqueIndex:idx_reply" json:"keyword"`
	Match   string            `json:"match"`        // contains（默认）/ exact / prefix / regex
	Delete  int               `json:"delete_after"` // 回复多少秒后自动删除，0 不删
	Content `gorm:"embedded"` // Text 里用单独一行 --- 分隔多条，随机回一条
	// 频率：Cooldown 秒内只回一次（0 = 默认 30 秒，-1 = 不限）；PerUser 按每个人分别计算；Chance 触发概率 1-100（0 = 100%）
	Cooldown int  `json:"cooldown"`
	PerUser  bool `json:"per_user"`
	Chance   int  `json:"chance"`
}

// Approved 白名单成员：不做任何反垃圾检查
type Approved struct {
	GroupID int64 `gorm:"primaryKey;autoIncrement:false" json:"group_id"`
	UserID  int64 `gorm:"primaryKey;autoIncrement:false" json:"user_id"`
}

// Schedule 每日定时消息
type Schedule struct {
	ID       uint   `gorm:"primaryKey" json:"id"`
	GroupID  int64  `gorm:"index" json:"group_id"`
	ThreadID int    `json:"thread_id"` // 话题群：发到哪个话题
	At       string `json:"at"`        // HH:MM
	LastRun  string `json:"last_run"`  // 最近一次发送的日期 YYYY-MM-DD
	Content  `gorm:"embedded"`
}

// ActionLog 操作日志（纯文本），面板查看用
type ActionLog struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Text      string    `json:"text"`
	CreatedAt time.Time `gorm:"index" json:"created_at"`
}

// Stat 每群每日计数
type Stat struct {
	GroupID  int64  `gorm:"primaryKey;autoIncrement:false" json:"group_id"`
	Day      string `gorm:"primaryKey" json:"day"`
	Messages int    `json:"messages"`
	Joins    int    `json:"joins"`
	Verified int    `json:"verified"`
	Deleted  int    `json:"deleted"`
	Banned   int    `json:"banned"`
	Muted    int    `json:"muted"`
}

func All() []any {
	return []any{&Group{}, &Member{}, &User{}, &Pending{}, &Filter{}, &GBan{},
		&Note{}, &AutoReply{}, &Approved{}, &Schedule{}, &Stat{}, &ActionLog{},
		&Fed{}, &FedAdmin{}, &FedBan{}, &Report{}, &UserBio{}, &BioReport{}, &Ad{}, &Setting{}, &Punishment{}, &SpamSample{},
		&Lottery{}, &LotteryCode{}, &LotteryEntry{}, &DelayedDelete{},
		&ForumTopic{}, &Points{}, &UserDay{}, &InviteLink{}, &Invite{}, &Capture{}, &VoteKick{}, &NoteLog{}}
}

// Fed 联邦（联合封禁组）：多个群共用一份封禁名单，联邦管理员在任意成员群封禁即全部生效
type Fed struct {
	ID        string    `gorm:"primaryKey" json:"id"`
	Name      string    `json:"name"`
	OwnerID   int64     `json:"owner_id"`
	CreatedAt time.Time `json:"created_at"`
}

// FedAdmin 联邦管理员（联邦主人不在此表里，也拥有全部权限）
type FedAdmin struct {
	FedID  string `gorm:"primaryKey" json:"fed_id"`
	UserID int64  `gorm:"primaryKey;autoIncrement:false" json:"user_id"`
}

// FedBan 联邦封禁记录
type FedBan struct {
	FedID     string    `gorm:"primaryKey" json:"fed_id"`
	UserID    int64     `gorm:"primaryKey;autoIncrement:false" json:"user_id"`
	Reason    string    `json:"reason"`
	By        int64     `json:"by"`
	CreatedAt time.Time `json:"created_at"`
}

// 举报状态
const (
	ReportOpen       = "open"
	ReportProcessing = "processing"
	ReportDeleted    = "deleted" // 删除了消息
	ReportMuted      = "muted"
	ReportBanned     = "banned"
	ReportIgnored    = "ignored"
)

// Report 群友举报，管理员在群里按钮或后台面板处理
type Report struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	GroupID    int64     `gorm:"index" json:"group_id"`
	ReporterID int64     `json:"reporter_id"`
	Reporter   string    `json:"reporter"`
	TargetID   int64     `json:"target_id"`
	Target     string    `json:"target"`
	MsgID      int       `json:"msg_id"`
	Text       string    `json:"text"`
	Count      int       `gorm:"default:1" json:"count"` // 同一条消息被多少个不同的人举报
	Reporters  string    `json:"-"`                      // 举报人 ID，空格分隔（去重用）
	NotifyMsg  int       `json:"-"`                      // 群里的提醒消息，处理后删除
	Status     string    `gorm:"index;default:open" json:"status"`
	HandledBy  string    `json:"handled_by"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// Ad 定时广告
type Ad struct {
	ID       uint   `gorm:"primaryKey" json:"id"`
	GroupID  int64  `gorm:"index" json:"group_id"`
	Name     string `json:"name"`
	Text     string `json:"text"`      // Markdown（面板创建）
	Photo    string `json:"photo"`     // 图片：http(s) 链接 / file_id / local:文件名
	PhotoID  string `json:"-"`         // 本地图片上传后 Telegram 返回的 file_id，复用
	Buttons  string `json:"buttons"`   // 每行一排，「文字 - 链接」，同排多个用 | 分隔
	FromChat int64  `json:"from_chat"` // 群命令创建：复制这条原消息（保留图片和格式）
	MsgID    int    `json:"msg_id"`

	ThreadID   int        `json:"thread_id"` // 话题群：发到哪个话题
	Times      string     `json:"times"`     // 每天固定时间 "09:00,21:00"；为空则按间隔
	Interval   int        `json:"interval"`  // 间隔分钟
	Enabled    bool       `gorm:"default:true" json:"enabled"`
	DeletePrev bool       `gorm:"default:true" json:"delete_prev"` // 发新的前删除上一条
	Pin        bool       `json:"pin"`                             // 发送后静默置顶
	LastMsgID  int        `json:"last_msg_id"`
	LastKey    string     `json:"last_key"` // 固定时间模式：最近一次发送的 日期+时间
	LastRunAt  *time.Time `json:"last_run_at"`
	Sent       int        `json:"sent"`
	CreatedAt  time.Time  `json:"created_at"`
}

// UserBio 用户主页缓存（查重用）
type UserBio struct {
	UserID    int64     `gorm:"primaryKey;autoIncrement:false" json:"user_id"`
	Bio       string    `json:"bio"`
	Channel   string    `json:"channel"` // 个人频道用户名
	Links     string    `json:"links"`   // 归一化后的链接，空格分隔
	Err       string    `json:"err"`
	CheckedAt time.Time `json:"checked_at"`
}

// BioReport 一次查重结果
type BioReport struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	GroupID   int64     `gorm:"index" json:"group_id"`
	Scanned   int       `json:"scanned"`  // 扫描人数
	Failed    int       `json:"failed"`   // 读取失败人数
	Clusters  string    `json:"clusters"` // JSON：[]BioCluster
	CreatedAt time.Time `json:"created_at"`
}

type BioCluster struct {
	Link  string       `json:"link"`
	Users []BioUserRef `json:"users"`
}

type BioUserRef struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Username string `json:"username"`
}

// Setting 运行时设置（面板里改的，优先于配置文件）
type Setting struct {
	Key   string `gorm:"primaryKey" json:"key"`
	Value string `json:"value"`
}

// Punishment 处罚记录：机器人执行的封禁 / 禁言 / 踢出，面板可一键撤销
type Punishment struct {
	ID        uint       `gorm:"primaryKey" json:"id"`
	GroupID   int64      `gorm:"index" json:"group_id"`
	UserID    int64      `gorm:"index" json:"user_id"`
	Name      string     `json:"name"`
	Action    string     `json:"action"`  // ban / mute / kick
	Minutes   int        `json:"minutes"` // 0 表示永久
	Source    string     `json:"source"`  // ai / learned / rule / warn / flood / admin / report / join / other
	Reason    string     `json:"reason"`
	Text      string     `json:"text"` // 触发处罚的消息
	Operator  string     `json:"operator"`
	Undone    bool       `gorm:"index" json:"undone"`
	UndoneBy  string     `json:"undone_by"`
	UndoneAt  *time.Time `json:"undone_at"`
	CreatedAt time.Time  `gorm:"index" json:"created_at"`
}

// SpamSample AI 学习样本：管理员确认的广告（spam）和误封的正常消息（ham）
type SpamSample struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	GroupID   int64     `json:"group_id"`
	Kind      string    `gorm:"index" json:"kind"` // spam / ham
	Text      string    `json:"text"`
	Norm      string    `json:"-"`
	Source    string    `json:"source"` // report / command / panel / unban
	By        string    `json:"by"`
	Hits      int       `json:"hits"`
	CreatedAt time.Time `json:"created_at"`
}
