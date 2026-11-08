package store

import (
	"strings"
	"time"

	"oai_bot/internal/model"

	"gorm.io/gorm/clause"
)

func (s *Store) InviteLinkOf(groupID, userID int64) *model.InviteLink {
	l, _ := first[model.InviteLink](s.db.Where("group_id = ? AND user_id = ?", groupID, userID))
	return l
}

func (s *Store) SaveInviteLink(l *model.InviteLink) error { return s.db.Save(l).Error }

// AddInvite 记录邀请（每人每群只记第一次），返回是否新记录
func (s *Store) AddInvite(inv *model.Invite) bool {
	r := s.db.Clauses(clause.OnConflict{DoNothing: true}).Create(inv)
	return r.Error == nil && r.RowsAffected == 1
}

func (s *Store) InviteOf(groupID, userID int64) *model.Invite {
	i, _ := first[model.Invite](s.db.Where("group_id = ? AND user_id = ?", groupID, userID))
	return i
}

func (s *Store) UpdateInvite(groupID, userID int64, fields map[string]any) {
	s.db.Model(&model.Invite{}).Where("group_id = ? AND user_id = ?", groupID, userID).Updates(fields)
}

// InviteCount 某人邀请且通过验证、未退群的人数
func (s *Store) InviteCount(groupID, inviterID int64) int {
	var n int64
	s.db.Model(&model.Invite{}).Where("group_id = ? AND inviter_id = ? AND verified = ? AND has_left = ?", groupID, inviterID, true, false).Count(&n)
	return int(n)
}

// InviteRank 邀请榜
func (s *Store) InviteRank(groupID int64, limit int) ([]RankRow, error) {
	var out []RankRow
	err := s.db.Table("invites i").Select("i.inviter_id AS user_id, COUNT(*) AS value, u.username, u.first_name, u.last_name").
		Joins("LEFT JOIN users u ON u.id = i.inviter_id").Where("i.group_id = ? AND i.verified = ? AND i.has_left = ?", groupID, true, false).
		Group("i.inviter_id, u.username, u.first_name, u.last_name").Order("value DESC").Limit(limit).Scan(&out).Error
	return out, err
}

// ---------- 抓取 ----------

// InviteRow 被邀请人
type InviteRow struct {
	UserID    int64     `json:"user_id"`
	Username  string    `json:"username"`
	FirstName string    `json:"first_name"`
	LastName  string    `json:"last_name"`
	Verified  bool      `json:"verified"`
	Left      bool      `gorm:"column:has_left" json:"left"`
	CreatedAt time.Time `json:"created_at"`
}

func (r InviteRow) Name() string {
	n := strings.TrimSpace(r.FirstName + " " + r.LastName)
	if n == "" && r.Username != "" {
		n = "@" + r.Username
	}
	return n
}

// InvitesBy 某个邀请人拉来的人，最新的在前
func (s *Store) InvitesBy(groupID, inviterID int64, limit, offset int) ([]InviteRow, int64) {
	var total int64
	s.db.Model(&model.Invite{}).Where("group_id = ? AND inviter_id = ?", groupID, inviterID).Count(&total)
	var out []InviteRow
	s.db.Table("invites i").Select("i.user_id, i.verified, i.has_left, i.created_at, u.username, u.first_name, u.last_name").
		Joins("LEFT JOIN users u ON u.id = i.user_id").Where("i.group_id = ? AND i.inviter_id = ?", groupID, inviterID).
		Order("i.created_at DESC").Limit(limit).Offset(offset).Scan(&out)
	return out, total
}

// InviterRow 邀请人汇总
type InviterRow struct {
	UserID    int64  `json:"user_id"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Total     int    `json:"total"`   // 通过链接进来的总人数
	Valid     int    `json:"valid"`   // 通过验证且还在群里
	Pending   int    `json:"pending"` // 还没通过验证
	Left      int    `json:"left"`    // 已退群
	Link      string `json:"link"`
}

// Inviters 所有领过链接或拉过人的成员，按有效人数排序
func (s *Store) Inviters(groupID int64, limit, offset int) ([]InviterRow, int64) {
	var rows []InviterRow
	s.db.Raw(`SELECT l.user_id, u.username, u.first_name, u.last_name, l.link,
		COUNT(i.user_id) AS total,
		COALESCE(SUM(CASE WHEN i.verified AND NOT i.has_left THEN 1 ELSE 0 END), 0) AS valid,
		COALESCE(SUM(CASE WHEN i.user_id IS NOT NULL AND NOT i.verified AND NOT i.has_left THEN 1 ELSE 0 END), 0) AS pending,
		COALESCE(SUM(CASE WHEN i.has_left THEN 1 ELSE 0 END), 0) AS "left"
		FROM invite_links l
		LEFT JOIN invites i ON i.group_id = l.group_id AND i.inviter_id = l.user_id
		LEFT JOIN users u ON u.id = l.user_id
		WHERE l.group_id = ?
		GROUP BY l.user_id, u.username, u.first_name, u.last_name, l.link
		ORDER BY valid DESC, total DESC, l.user_id
		LIMIT ? OFFSET ?`, groupID, limit, offset).Scan(&rows)
	var total int64
	s.db.Model(&model.InviteLink{}).Where("group_id = ?", groupID).Count(&total)
	return rows, total
}

// UserName 用户显示名（没见过返回空）
func (s *Store) UserName(id int64) string {
	var r InviteRow
	s.db.Table("users").Select("username, first_name, last_name").Where("id = ?", id).Limit(1).Scan(&r)
	return r.Name()
}
