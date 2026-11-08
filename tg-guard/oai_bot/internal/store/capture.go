package store

import (
	"time"

	"oai_bot/internal/model"
)

func (s *Store) CreateCapture(c *model.Capture) error { return s.db.Create(c).Error }

func (s *Store) Capture(token string) *model.Capture {
	c, _ := first[model.Capture](s.db.Where("token = ?", token))
	return c
}

func (s *Store) SaveCapture(c *model.Capture) error { return s.db.Save(c).Error }

// PruneCaptures 清理一天前的抓取记录
func (s *Store) PruneCaptures() {
	s.db.Where("created_at < ?", time.Now().Add(-24*time.Hour)).Delete(&model.Capture{})
}

// ---------- 投票踢人 ----------
