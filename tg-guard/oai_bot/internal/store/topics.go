package store

import (
	"oai_bot/internal/model"

	"gorm.io/gorm/clause"
)

func (s *Store) SaveTopic(t *model.ForumTopic) {
	cols := []string{"updated_at"}
	if t.Name != "" {
		cols = append(cols, "name")
	}
	s.db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "group_id"}, {Name: "thread_id"}},
		DoUpdates: clause.AssignmentColumns(cols)}).Create(t)
}

func (s *Store) SetTopicClosed(groupID int64, thread int, closed bool) {
	s.db.Model(&model.ForumTopic{}).Where("group_id = ? AND thread_id = ?", groupID, thread).Update("closed", closed)
}

func (s *Store) Topics(groupID int64) ([]model.ForumTopic, error) {
	var ts []model.ForumTopic
	return ts, s.db.Where("group_id = ?", groupID).Order("thread_id").Find(&ts).Error
}

// ---------- 积分 ----------
