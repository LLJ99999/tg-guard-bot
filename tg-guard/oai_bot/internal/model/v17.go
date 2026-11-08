package model

import "time"

// ---------- v1.7：话题、积分、邀请、抓取、投票踢人 ----------

// ForumTopic 话题群里机器人见过的话题（Bot API 没有列出话题的接口，只能从消息里记下来）
type ForumTopic struct {
	GroupID   int64     `gorm:"primaryKey;autoIncrement:false" json:"group_id"`
	ThreadID  int       `gorm:"primaryKey;autoIncrement:false" json:"thread_id"`
	Name      string    `json:"name"`
	Closed    bool      `json:"closed"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Points 群积分 / 签到
type Points struct {
	GroupID     int64     `gorm:"primaryKey;autoIncrement:false" json:"group_id"`
	UserID      int64     `gorm:"primaryKey;autoIncrement:false" json:"user_id"`
	Points      int       `gorm:"index" json:"points"`
	Streak      int       `json:"streak"`       // 连续签到天数
	LastCheckin string    `json:"last_checkin"` // YYYY-MM-DD
	Checkins    int       `json:"checkins"`     // 累计签到次数
	MsgDay      string    `json:"-"`            // 发言积分：当天日期
	MsgPts      int       `json:"-"`            // 发言积分：当天已得
	UpdatedAt   time.Time `json:"updated_at"`
}

// UserDay 每人每天发言数（活跃榜）
type UserDay struct {
	GroupID int64  `gorm:"primaryKey;autoIncrement:false" json:"group_id"`
	Day     string `gorm:"primaryKey" json:"day"`
	UserID  int64  `gorm:"primaryKey;autoIncrement:false" json:"user_id"`
	Msgs    int    `json:"msgs"`
}

// InviteLink 成员专属邀请链接
type InviteLink struct {
	GroupID   int64     `gorm:"primaryKey;autoIncrement:false" json:"group_id"`
	UserID    int64     `gorm:"primaryKey;autoIncrement:false" json:"user_id"`
	Link      string    `gorm:"index" json:"link"`
	CreatedAt time.Time `json:"created_at"`
}

// Invite 一次邀请：谁通过谁的链接进群，是否通过验证
type Invite struct {
	GroupID   int64     `gorm:"primaryKey;autoIncrement:false" json:"group_id"`
	UserID    int64     `gorm:"primaryKey;autoIncrement:false" json:"user_id"` // 被邀请人（每人每群只算第一次）
	InviterID int64     `gorm:"index" json:"inviter_id"`
	Verified  bool      `json:"verified"`
	Left      bool      `gorm:"column:has_left" json:"left"`
	CreatedAt time.Time `json:"created_at"`
}

// Capture 面板发起的「去 Telegram 私聊发给机器人」抓取
type Capture struct {
	Token     string    `gorm:"primaryKey" json:"token"`
	Purpose   string    `json:"purpose"` // text / media / any
	UserID    int64     `json:"user_id"` // 谁发的
	Done      bool      `json:"done"`
	Markdown  string    `json:"markdown"` // 文字（带格式、动态表情，转成面板用的 Markdown）
	Media     string    `json:"media"`    // photo:<file_id> / animation:<id> / video:<id> / document:<id> / sticker:<id>
	FromChat  int64     `json:"from_chat"`
	MsgID     int       `json:"msg_id"`
	Preview   string    `json:"preview"` // 给面板看的简述
	CreatedAt time.Time `json:"created_at"`
}

// VoteKick 投票踢人
type VoteKick struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	GroupID   int64     `gorm:"index" json:"group_id"`
	TargetID  int64     `json:"target_id"`
	Target    string    `json:"target"`
	MsgID     int       `json:"msg_id"`   // 投票消息
	Reported  int       `json:"reported"` // 被投票的那条消息
	Voters    string    `json:"voters"`   // 空格分隔的用户 ID
	Need      int       `json:"need"`
	Status    string    `json:"status"` // open / done / expired
	CreatedAt time.Time `json:"created_at"`
}
