package store

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"oai_bot/internal/model"
)

// ---------- 联邦 ----------

func (s *Store) CreateFed(name string, owner int64) (*model.Fed, error) {
	b := make([]byte, 6)
	rand.Read(b)
	f := &model.Fed{ID: hex.EncodeToString(b), Name: name, OwnerID: owner}
	return f, s.db.Create(f).Error
}

func (s *Store) Fed(id string) (*model.Fed, error) {
	return first[model.Fed](s.db.Where("id = ?", id))
}

func (s *Store) Feds() ([]model.Fed, error) {
	var fs []model.Fed
	return fs, s.db.Order("created_at").Find(&fs).Error
}

func (s *Store) RenameFed(id, name string) error {
	return s.db.Model(&model.Fed{}).Where("id = ?", id).Update("name", name).Error
}

// DeleteFed 删除联邦及其管理员、封禁名单，成员群退出
func (s *Store) DeleteFed(id string) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		for _, m := range []any{&model.FedAdmin{}, &model.FedBan{}} {
			if err := tx.Where("fed_id = ?", id).Delete(m).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&model.Group{}).Where("fed_id = ?", id).Update("fed_id", "").Error; err != nil {
			return err
		}
		return tx.Where("id = ?", id).Delete(&model.Fed{}).Error
	})
}

func (s *Store) FedGroups(id string) ([]model.Group, error) {
	var gs []model.Group
	return gs, s.db.Where("fed_id = ?", id).Order("title").Find(&gs).Error
}

func (s *Store) FedAdmins(id string) ([]int64, error) {
	var ids []int64
	return ids, s.db.Model(&model.FedAdmin{}).Where("fed_id = ?", id).Pluck("user_id", &ids).Error
}

func (s *Store) SetFedAdmin(id string, uid int64, on bool) error {
	if !on {
		return s.db.Where("fed_id = ? AND user_id = ?", id, uid).Delete(&model.FedAdmin{}).Error
	}
	return s.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.FedAdmin{FedID: id, UserID: uid}).Error
}

// IsFedAdmin 联邦主人或联邦管理员
func (s *Store) IsFedAdmin(id string, uid int64) bool {
	f, _ := s.Fed(id)
	if f == nil {
		return false
	}
	if f.OwnerID == uid {
		return true
	}
	a, _ := first[model.FedAdmin](s.db.Where("fed_id = ? AND user_id = ?", id, uid))
	return a != nil
}

func (s *Store) FedBanned(id string, uid int64) *model.FedBan {
	if id == "" {
		return nil
	}
	b, _ := first[model.FedBan](s.db.Where("fed_id = ? AND user_id = ?", id, uid))
	return b
}

func (s *Store) SetFedBan(b *model.FedBan, on bool) error {
	if !on {
		return s.db.Where("fed_id = ? AND user_id = ?", b.FedID, b.UserID).Delete(&model.FedBan{}).Error
	}
	return s.db.Clauses(clause.OnConflict{UpdateAll: true}).Create(b).Error
}

func (s *Store) FedBans(id string) ([]model.FedBan, error) {
	var bs []model.FedBan
	return bs, s.db.Where("fed_id = ?", id).Order("created_at desc").Find(&bs).Error
}

func (s *Store) FedBanCount(id string) int64 {
	var n int64
	s.db.Model(&model.FedBan{}).Where("fed_id = ?", id).Count(&n)
	return n
}

// ---------- 举报 ----------

// AddReport 记录举报；同一条消息已有未处理的举报时只累加次数，返回 (举报, 是否新建)
func (s *Store) AddReport(r *model.Report) (*model.Report, bool, error) {
	old, err := first[model.Report](s.db.Where("group_id = ? AND msg_id = ? AND status = ?", r.GroupID, r.MsgID, model.ReportOpen))
	if err != nil {
		return nil, false, err
	}
	if old != nil {
		old.Count++
		return old, false, s.db.Model(old).UpdateColumn("count", old.Count).Error
	}
	r.Status = model.ReportOpen
	return r, true, s.db.Create(r).Error
}

func (s *Store) Report(id uint) (*model.Report, error) {
	return first[model.Report](s.db.Where("id = ?", id))
}

func (s *Store) Reports(groupID int64, status string, limit int) ([]model.Report, error) {
	q := s.db.Order("id desc").Limit(limit)
	if groupID != 0 {
		q = q.Where("group_id = ?", groupID)
	}
	if status != "" {
		q = q.Where("status = ?", status)
	}
	var rs []model.Report
	return rs, q.Find(&rs).Error
}

func (s *Store) OpenReportCount() int64 {
	var n int64
	s.db.Model(&model.Report{}).Where("status = ?", model.ReportOpen).Count(&n)
	return n
}

// CloseReport 把一条未处理的举报标记为已处理；已被别人处理过返回 false
func (s *Store) CloseReport(id uint, status, by string) (bool, error) {
	res := s.db.Model(&model.Report{}).Where("id = ? AND status = ?", id, model.ReportOpen).
		Updates(map[string]any{"status": status, "handled_by": by})
	return res.RowsAffected > 0, res.Error
}

// BeginReport atomically claims a report; only one worker can act on it.
func (s *Store) BeginReport(id uint, by string) (bool, error) {
	res := s.db.Model(&model.Report{}).Where("id = ? AND status = ?", id, model.ReportOpen).
		Updates(map[string]any{"status": model.ReportProcessing, "handled_by": by})
	return res.RowsAffected > 0, res.Error
}

// FinishReport either records success or releases a failed action for retry.
func (s *Store) FinishReport(id uint, status, by string) error {
	res := s.db.Model(&model.Report{}).Where("id = ? AND status = ?", id, model.ReportProcessing).
		Updates(map[string]any{"status": status, "handled_by": by})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != 1 {
		return fmt.Errorf("report claim lost")
	}
	return nil
}

func (s *Store) SetReportNotify(id uint, msgID int) error {
	return s.db.Model(&model.Report{}).Where("id = ?", id).UpdateColumn("notify_msg", msgID).Error
}

// ---------- 设置复制 ----------

// CopyFilters 把 from 群的屏蔽词复制到 to 群（已存在的跳过）
func (s *Store) CopyFilters(from, to int64) (int, error) {
	src, err := s.Filters(from)
	if err != nil {
		return 0, err
	}
	dst, _ := s.Filters(to)
	have := map[string]bool{}
	for _, f := range dst {
		have[f.Pattern] = true
	}
	n := 0
	for _, f := range src {
		if have[f.Pattern] {
			continue
		}
		if err := s.db.Create(&model.Filter{GroupID: to, Pattern: f.Pattern, Regex: f.Regex, Action: f.Action}).Error; err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
