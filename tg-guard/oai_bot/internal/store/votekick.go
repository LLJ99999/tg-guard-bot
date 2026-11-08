package store

import "oai_bot/internal/model"

func (s *Store) CreateVoteKick(v *model.VoteKick) error { return s.db.Create(v).Error }

func (s *Store) VoteKick(id uint) *model.VoteKick {
	v, _ := first[model.VoteKick](s.db.Where("id = ?", id))
	return v
}

func (s *Store) OpenVoteKick(groupID, target int64) *model.VoteKick {
	v, _ := first[model.VoteKick](s.db.Where("group_id = ? AND target_id = ? AND status = ?", groupID, target, "open"))
	return v
}

func (s *Store) UpdateVoteKick(id uint, fields map[string]any) {
	s.db.Model(&model.VoteKick{}).Where("id = ?", id).Updates(fields)
}

// ---------- 其它 ----------
