package model

import "time"

// 抽奖奖品类型
const (
	PrizeCode = "code" // 卡密：每行一条，每个中奖者一条；只填一条时所有中奖者共用
	PrizeFile = "file" // 文件：每个中奖者都收到同一个文件
)

// 抽奖状态
const (
	LotteryOpen      = "open"
	LotteryDrawn     = "drawn"
	LotteryCancelled = "cancelled"
)

// Lottery 群抽奖：群里发带「参与」按钮的消息，到时间或满人数自动开奖，奖品私聊发给中奖者
type Lottery struct {
	ID        uint       `gorm:"primaryKey" json:"id"`
	GroupID   int64      `gorm:"index" json:"group_id"`
	Title     string     `json:"title"`
	Desc      string     `json:"desc"`
	PrizeType string     `gorm:"default:code" json:"prize_type"`
	Winners   int        `gorm:"default:1" json:"winners"` // 中奖人数
	DrawAt    *time.Time `json:"draw_at"`                  // 到这个时间开奖，空表示不按时间
	DrawCount int        `json:"draw_count"`               // 参与人数达到多少自动开奖，0 表示不按人数
	Status    string     `gorm:"index;default:open" json:"status"`
	FileName  string     `json:"file_name"`
	FilePath  string     `json:"-"` // 本地文件名（data/lottery/ 下）
	FileID    string     `json:"-"` // 第一次发出后 Telegram 返回的 file_id，复用
	FileSize  int64      `json:"file_size"`
	MsgID     int        `json:"msg_id"` // 群里的抽奖消息
	ResultMsg int        `json:"result_msg"`
	Entries   int        `json:"entries"` // 参与人数（冗余计数）

	// 参与条件（防薅羊毛）
	RequireChat string     `json:"require_chat"`  // 必须同时是这个频道 / 群的成员（@用户名或 -100 开头的 ID），空表示不限
	MinMsgs     int        `json:"min_msgs"`      // 在本群至少发过多少条消息，0 不限
	OldOnly     bool       `json:"old_only"`      // 只允许抽奖发起前就在群里的成员参与（防止临时拉小号）
	MinPoints   int        `json:"min_points"`    // 积分至少多少
	CostPoints  int        `json:"cost_points"`   // 参与扣多少积分
	MinInvites  int        `json:"min_invites"`   // 至少邀请（且通过验证）多少人
	ThreadID    int        `json:"thread_id"`     // 话题群：抽奖消息发到哪个话题
	Tiers       string     `json:"tiers"`         // 多奖项 JSON：[{"name":"一等奖","winners":1}]，空表示单一奖项
	Repeat      string     `json:"repeat"`        // 开奖后自动再发一期：daily / weekly，空不重复
	PinMsg      bool       `json:"pin_msg"`       // 抽奖消息置顶，开奖后取消置顶
	JoinInGroup bool       `json:"join_in_group"` // 群里直接点按钮参与（不跳私聊；中奖后需私聊机器人领奖）
	SeedHash    string     `json:"seed_hash"`     // 公平性：发起时公布种子的 SHA-256
	Seed        string     `json:"seed"`          // 开奖后公开种子，任何人可复算
	ParentID    uint       `json:"parent_id"`     // 周期抽奖的上一期
	CreatedBy   string     `json:"created_by"`
	CreatedAt   time.Time  `json:"created_at"`
	DrawnAt     *time.Time `json:"drawn_at"`
}

// LotteryCode 卡密奖品
type LotteryCode struct {
	ID        uint   `gorm:"primaryKey" json:"id"`
	LotteryID uint   `gorm:"index" json:"lottery_id"`
	Code      string `json:"code"`
	UserID    int64  `json:"user_id"` // 分给了谁，0 表示未分配
	Tier      int    `json:"tier"`    // 多奖项：属于第几个奖项（0 起）
}

// LotteryEntry 参与记录
type LotteryEntry struct {
	LotteryID  uint      `gorm:"primaryKey;autoIncrement:false" json:"lottery_id"`
	UserID     int64     `gorm:"primaryKey;autoIncrement:false" json:"user_id"`
	Name       string    `json:"name"`
	Username   string    `json:"username"`
	Won        bool      `gorm:"index" json:"won"`
	Code       string    `json:"code"`
	Tier       string    `json:"tier"` // 中的哪个奖项
	Delivered  bool      `json:"delivered"`
	DeliverErr string    `json:"deliver_err"`
	CreatedAt  time.Time `json:"created_at"`
}

// DelayedDelete 定时删除的消息（欢迎语等），落库后重启也不会漏删
type DelayedDelete struct {
	ID     uint  `gorm:"primaryKey"`
	ChatID int64 `gorm:"index"`
	MsgID  int
	DueAt  time.Time `gorm:"index"`
}
