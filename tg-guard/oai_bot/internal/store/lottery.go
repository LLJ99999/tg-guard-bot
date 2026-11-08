package store

import (
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"oai_bot/internal/model"
)

// ---------- 抽奖 ----------

// CreateLottery 新建抽奖及其卡密
func (s *Store) CreateLottery(l *model.Lottery, codes []string) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(l).Error; err != nil {
			return err
		}
		for _, c := range codes {
			if err := tx.Create(&model.LotteryCode{LotteryID: l.ID, Code: c}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) Lottery(id uint) (*model.Lottery, error) {
	return first[model.Lottery](s.db.Where("id = ?", id))
}

func (s *Store) Lotteries(groupID int64) ([]model.Lottery, error) {
	var ls []model.Lottery
	return ls, s.db.Where("group_id = ?", groupID).Order("id desc").Limit(100).Find(&ls).Error
}

// OpenLotteries 所有未开奖的抽奖（定时检查用）
func (s *Store) OpenLotteries() ([]model.Lottery, error) {
	var ls []model.Lottery
	return ls, s.db.Where("status = ?", model.LotteryOpen).Find(&ls).Error
}

func (s *Store) UpdateLottery(id uint, fields map[string]any) error {
	return s.db.Model(&model.Lottery{}).Where("id = ?", id).Updates(fields).Error
}

// ClaimLottery 把状态从 open 改成 to，只有一个调用方能成功（防止重复开奖）
func (s *Store) ClaimLottery(id uint, to string) bool {
	now := time.Now()
	fields := map[string]any{"status": to}
	if to == model.LotteryDrawn {
		fields["drawn_at"] = now
	}
	r := s.db.Model(&model.Lottery{}).Where("id = ? AND status = ?", id, model.LotteryOpen).Updates(fields)
	return r.Error == nil && r.RowsAffected == 1
}

func (s *Store) LotteryCodes(id uint) ([]model.LotteryCode, error) {
	var cs []model.LotteryCode
	return cs, s.db.Where("lottery_id = ?", id).Order("id").Find(&cs).Error
}

func (s *Store) CountLotteryCodes(id uint) int64 {
	var n int64
	s.db.Model(&model.LotteryCode{}).Where("lottery_id = ?", id).Count(&n)
	return n
}

func (s *Store) AssignCode(codeID uint, userID int64) error {
	return s.db.Model(&model.LotteryCode{}).Where("id = ?", codeID).Update("user_id", userID).Error
}

// ErrLotteryClosed 参与时抽奖已经开奖 / 取消
var ErrLotteryClosed = errors.New("lottery closed")

// JoinLottery 参与，返回是否新加入以及当前人数。抽奖已不是 open 时返回 ErrLotteryClosed（防止开奖瞬间加入）
func (s *Store) JoinLottery(e *model.LotteryEntry) (bool, int, error) {
	var added bool
	var n int64
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var open int64
		if err := tx.Model(&model.Lottery{}).Where("id = ? AND status = ?", e.LotteryID, model.LotteryOpen).Count(&open).Error; err != nil {
			return err
		}
		if open == 0 {
			return ErrLotteryClosed
		}
		r := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(e)
		if r.Error != nil {
			return r.Error
		}
		added = r.RowsAffected == 1
		if err := tx.Model(&model.LotteryEntry{}).Where("lottery_id = ?", e.LotteryID).Count(&n).Error; err != nil {
			return err
		}
		return tx.Model(&model.Lottery{}).Where("id = ?", e.LotteryID).Update("entries", n).Error
	})
	return added, int(n), err
}

func (s *Store) LotteryEntries(id uint) ([]model.LotteryEntry, error) {
	var es []model.LotteryEntry
	return es, s.db.Where("lottery_id = ?", id).Order("created_at").Find(&es).Error
}

func (s *Store) LotteryEntry(id uint, userID int64) (*model.LotteryEntry, error) {
	return first[model.LotteryEntry](s.db.Where("lottery_id = ? AND user_id = ?", id, userID))
}

func (s *Store) UpdateEntry(id uint, userID int64, fields map[string]any) error {
	return s.db.Model(&model.LotteryEntry{}).Where("lottery_id = ? AND user_id = ?", id, userID).Updates(fields).Error
}

// ---------- 定时删除 ----------

func (s *Store) AddDelayedDelete(chatID int64, msgID int, due time.Time) uint {
	d := &model.DelayedDelete{ChatID: chatID, MsgID: msgID, DueAt: due}
	if s.db.Create(d).Error != nil {
		return 0
	}
	return d.ID
}

func (s *Store) DueDeletes(now time.Time) ([]model.DelayedDelete, error) {
	var ds []model.DelayedDelete
	return ds, s.db.Where("due_at <= ?", now).Limit(200).Find(&ds).Error
}

func (s *Store) DoneDelete(id uint) {
	s.db.Delete(&model.DelayedDelete{}, id)
}

// DelLotteryFile 取消抽奖后清掉 file_path，方便调用方删除本地文件
func (s *Store) ClearLotteryFile(id uint) {
	s.db.Model(&model.Lottery{}).Where("id = ?", id).Updates(map[string]any{"file_path": ""})
}

// CreateLotteryRows 新建抽奖及其卡密（带奖项）
func (s *Store) CreateLotteryRows(l *model.Lottery, codes []model.LotteryCode) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(l).Error; err != nil {
			return err
		}
		for i := range codes {
			codes[i].LotteryID = l.ID
			if err := tx.Create(&codes[i]).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// 开奖前的种子单独存在 settings 表里，不随抽奖列表返回给面板
func (s *Store) SetLotterySeed(id uint, seed string) {
	s.SetSetting(fmt.Sprintf("lottery_seed_%d", id), seed)
}

func (s *Store) LotterySeed(id uint) string {
	return s.Setting(fmt.Sprintf("lottery_seed_%d", id))
}

// LotteryFileUsers 有多少期抽奖共用同一个奖品文件
func (s *Store) LotteryFileUsers(path string) int64 {
	var n int64
	s.db.Model(&model.Lottery{}).Where("file_path = ? AND status = ?", path, model.LotteryOpen).Count(&n)
	return n + 1
}
