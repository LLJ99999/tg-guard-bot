package store

import (
	"time"

	"oai_bot/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *Store) PointsOf(groupID, userID int64) *model.Points {
	p, _ := first[model.Points](s.db.Where("group_id = ? AND user_id = ?", groupID, userID))
	if p == nil {
		p = &model.Points{GroupID: groupID, UserID: userID}
	}
	return p
}

func (s *Store) SavePoints(p *model.Points) error {
	p.UpdatedAt = time.Now()
	return s.db.Save(p).Error
}

// AddPoints 原子加减积分（可为负，最低 0），返回最新积分
func (s *Store) AddPoints(groupID, userID int64, delta int) (int, error) {
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.Points{GroupID: groupID, UserID: userID, UpdatedAt: time.Now()}).Error; err != nil {
			return err
		}
		return tx.Model(&model.Points{}).Where("group_id = ? AND user_id = ?", groupID, userID).
			Updates(map[string]any{"points": gorm.Expr("MAX(points + ?, 0)", delta), "updated_at": time.Now()}).Error
	})
	if err != nil {
		return 0, err
	}
	return s.PointsOf(groupID, userID).Points, nil
}

// SpendPoints 扣积分，不够返回 false
func (s *Store) SpendPoints(groupID, userID int64, n int) bool {
	r := s.db.Model(&model.Points{}).Where("group_id = ? AND user_id = ? AND points >= ?", groupID, userID, n).
		UpdateColumn("points", gorm.Expr("points - ?", n))
	return r.Error == nil && r.RowsAffected == 1
}

type RankRow struct {
	UserID    int64  `json:"user_id"`
	Value     int    `json:"value"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

// PointsRank 积分榜
func (s *Store) PointsRank(groupID int64, limit, offset int) ([]RankRow, int64, error) {
	var out []RankRow
	var total int64
	s.db.Model(&model.Points{}).Where("group_id = ? AND points > 0", groupID).Count(&total)
	err := s.db.Table("points p").Select("p.user_id, p.points AS value, u.username, u.first_name, u.last_name").
		Joins("LEFT JOIN users u ON u.id = p.user_id").Where("p.group_id = ? AND p.points > 0", groupID).
		Order("p.points DESC").Limit(limit).Offset(offset).Scan(&out).Error
	return out, total, err
}

// IncUserDay 每人每天发言 +1
func (s *Store) IncUserDay(groupID, userID int64, day string) {
	s.db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "group_id"}, {Name: "day"}, {Name: "user_id"}},
		DoUpdates: clause.Assignments(map[string]any{"msgs": gorm.Expr("user_days.msgs + 1")})}).
		Create(&model.UserDay{GroupID: groupID, Day: day, UserID: userID, Msgs: 1})
}

// ActivityRank 发言榜（days 内）
func (s *Store) ActivityRank(groupID int64, days []string, limit int) ([]RankRow, error) {
	var out []RankRow
	err := s.db.Table("user_days d").Select("d.user_id, SUM(d.msgs) AS value, u.username, u.first_name, u.last_name").
		Joins("LEFT JOIN users u ON u.id = d.user_id").Where("d.group_id = ? AND d.day IN ?", groupID, days).
		Group("d.user_id, u.username, u.first_name, u.last_name").Order("value DESC").Limit(limit).Scan(&out).Error
	return out, err
}

// PruneUserDays 删掉 keepFrom 之前的每日发言记录
func (s *Store) PruneUserDays(keepFrom string) {
	s.db.Where("day < ?", keepFrom).Delete(&model.UserDay{})
}

// ---------- 邀请 ----------

// AddMsgPoints 发言积分：当天累计不超过 cap
func (s *Store) AddMsgPoints(groupID, userID int64, day string, n, cap int) {
	s.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.Points{GroupID: groupID, UserID: userID, UpdatedAt: time.Now()})
	s.db.Exec(`UPDATE points SET points = points + ?, msg_pts = CASE WHEN msg_day = ? THEN msg_pts + ? ELSE ? END, msg_day = ?, updated_at = ?
		WHERE group_id = ? AND user_id = ? AND (msg_day <> ? OR msg_day IS NULL OR msg_pts + ? <= ?)`,
		n, day, n, n, day, time.Now(), groupID, userID, day, n, cap)
}

// Checkin 签到：返回本次得分、最新记录、是否成功（今天已签返回 false）
func (s *Store) Checkin(groupID, userID int64, today, yesterday string, base, bonus int) (int, *model.Points, bool) {
	var gain int
	ok := false
	var p *model.Points
	s.db.Transaction(func(tx *gorm.DB) error {
		tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.Points{GroupID: groupID, UserID: userID, UpdatedAt: time.Now()})
		var err error
		if p, err = first[model.Points](tx.Where("group_id = ? AND user_id = ?", groupID, userID)); err != nil || p == nil {
			return err
		}
		if p.LastCheckin == today {
			return nil
		}
		if p.LastCheckin == yesterday {
			p.Streak++
		} else {
			p.Streak = 1
		}
		gain = base + bonus*min(p.Streak-1, 7)
		p.Points += gain
		p.Checkins++
		p.LastCheckin = today
		ok = true
		return tx.Model(&model.Points{}).Where("group_id = ? AND user_id = ?", groupID, userID).Updates(map[string]any{
			"points": p.Points, "streak": p.Streak, "checkins": p.Checkins, "last_checkin": today, "updated_at": time.Now()}).Error
	})
	if p == nil {
		p = &model.Points{GroupID: groupID, UserID: userID}
	}
	return gain, p, ok
}
