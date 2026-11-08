package store

import (
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"oai_bot/internal/model"
)

// ---------- 处罚记录 ----------

func (s *Store) AddPunishment(p *model.Punishment) error { return s.db.Create(p).Error }

// LastPunishment 某人最近一条未撤销的处罚（actions 为空表示任意类型）
func (s *Store) LastPunishment(groupID, userID int64, actions ...string) (*model.Punishment, error) {
	q := s.db.Where("group_id = ? AND user_id = ? AND undone = ?", groupID, userID, false)
	if len(actions) > 0 {
		q = q.Where("action IN ?", actions)
	}
	return first[model.Punishment](q.Order("id DESC"))
}

func (s *Store) Punishment(groupID int64, id uint) (*model.Punishment, error) {
	return first[model.Punishment](s.db.Where("group_id = ? AND id = ?", groupID, id))
}

func (s *Store) UndoPunishment(id uint, by string) error {
	now := time.Now()
	return s.db.Model(&model.Punishment{}).Where("id = ?", id).Updates(map[string]any{"undone": true, "undone_by": by, "undone_at": &now}).Error
}

// Punishments 处罚记录列表；status: active 只看未撤销，undone 只看已撤销
func (s *Store) Punishments(groupID int64, q, status string, limit int) ([]model.Punishment, error) {
	var ps []model.Punishment
	db := s.db.Where("group_id = ?", groupID).Order("id DESC").Limit(limit)
	switch status {
	case "active":
		db = db.Where("undone = ?", false)
	case "undone":
		db = db.Where("undone = ?", true)
	}
	if q = strings.TrimSpace(q); q != "" {
		if id, err := strconv.ParseInt(q, 10, 64); err == nil {
			db = db.Where("user_id = ?", id)
		} else {
			like := "%" + q + "%"
			db = db.Where("name LIKE ? OR reason LIKE ? OR text LIKE ?", like, like, like)
		}
	}
	return ps, db.Find(&ps).Error
}

// ---------- 成员列表 ----------

type MemberRow struct {
	model.Member
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

// MemberList 机器人见过的成员（Bot API 拿不到完整成员列表）
func (s *Store) MemberList(groupID int64, q, filter string, limit, offset int) ([]MemberRow, int64, error) {
	db := s.db.Table("members").Joins("LEFT JOIN users ON users.id = members.user_id").Where("members.group_id = ?", groupID)
	switch filter {
	case "in":
		db = db.Where("members.has_left = ?", false)
	case "left":
		db = db.Where("members.has_left = ?", true)
	case "warned":
		db = db.Where("members.warns > 0")
	case "new":
		db = db.Where("members.newcomer = ?", true)
	}
	if q = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(q), "@")); q != "" {
		if id, err := strconv.ParseInt(q, 10, 64); err == nil {
			db = db.Where("members.user_id = ?", id)
		} else {
			like := "%" + q + "%"
			db = db.Where("users.username LIKE ? OR users.first_name LIKE ? OR users.last_name LIKE ?", like, like, like)
		}
	}
	var n int64
	if err := db.Count(&n).Error; err != nil {
		return nil, 0, err
	}
	rows := []MemberRow{}
	err := db.Select("members.*, users.username, users.first_name, users.last_name").
		Order("members.joined_at DESC").Limit(limit).Offset(offset).Scan(&rows).Error
	return rows, n, err
}

// ---------- 学习样本 ----------

func (s *Store) AddSample(x *model.SpamSample) error { return s.db.Create(x).Error }

func (s *Store) Samples(kind string) ([]model.SpamSample, error) {
	var xs []model.SpamSample
	db := s.db.Order("id DESC")
	if kind != "" {
		db = db.Where("kind = ?", kind)
	}
	return xs, db.Find(&xs).Error
}

func (s *Store) DelSample(id uint) (bool, error) {
	r := s.db.Delete(&model.SpamSample{}, id)
	return r.RowsAffected > 0, r.Error
}

func (s *Store) HitSample(id uint) {
	s.db.Model(&model.SpamSample{}).Where("id = ?", id).UpdateColumn("hits", gorm.Expr("hits + 1"))
}
