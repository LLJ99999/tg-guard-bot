// Package store 数据库访问（SQLite，单文件）。
package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"

	"oai_bot/internal/model"
)

type Store struct{ db *gorm.DB }

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := gorm.Open(sqlite.Open(path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Warn)})
	if err != nil {
		return nil, err
	}
	// Existing groups must not gain new enforcement rules as a side effect of upgrade.
	legacy := db.Migrator().HasTable(&model.Group{})
	defaults := map[string]any{}
	if legacy {
		for k, v := range map[string]any{"profile_check": false, "lock_invite": false, "lock_contact": false, "clean_welcome": false, "welcome_ttl": 0, "captcha_type": model.CaptchaWeb} {
			if !db.Migrator().HasColumn(&model.Group{}, k) {
				defaults[k] = v
			}
		}
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.AutoMigrate(model.All()...); err != nil {
			return err
		}
		if len(defaults) > 0 {
			if err := tx.Model(&model.Group{}).Where("1 = 1").UpdateColumns(defaults).Error; err != nil {
				return err
			}
		}
		// There is only one service process for this DB. Recover interrupted claims on restart.
		return tx.Model(&model.Report{}).Where("status = ?", model.ReportProcessing).
			Updates(map[string]any{"status": model.ReportOpen, "handled_by": ""}).Error
	}); err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

func first[T any](q *gorm.DB) (*T, error) {
	var rows []T
	if err := q.Limit(1).Find(&rows).Error; err != nil || len(rows) == 0 {
		return nil, err
	}
	return &rows[0], nil
}

// ---------- 群 ----------

// Group 取群设置，不存在时按默认值创建
func (s *Store) Group(id int64, title string) (*model.Group, error) {
	g, err := first[model.Group](s.db.Where("id = ?", id))
	if err != nil {
		return nil, err
	}
	if g != nil {
		if title != "" && g.Title != title {
			g.Title = title
			s.db.Model(g).Update("title", title)
		}
		return g, nil
	}
	g = &model.Group{ID: id, Title: title}
	if err := s.db.Create(g).Error; err != nil {
		return nil, err
	}
	return first[model.Group](s.db.Where("id = ?", id)) // 读回默认值
}

func (s *Store) UpdateGroup(id int64, fields map[string]any) error {
	return s.db.Model(&model.Group{}).Where("id = ?", id).Updates(fields).Error
}

// ---------- 成员 ----------

func (s *Store) Member(groupID, userID int64) (*model.Member, error) {
	m, err := first[model.Member](s.db.Where("group_id = ? AND user_id = ?", groupID, userID))
	if err != nil || m != nil {
		return m, err
	}
	m = &model.Member{GroupID: groupID, UserID: userID, JoinedAt: time.Now()}
	return m, s.db.Clauses(clause.OnConflict{DoNothing: true}).Create(m).Error
}

func (s *Store) UpdateMember(groupID, userID int64, fields map[string]any) error {
	return s.db.Model(&model.Member{}).Where("group_id = ? AND user_id = ?", groupID, userID).Updates(fields).Error
}

// IncMsgCount 消息计数 +1（原子）
func (s *Store) IncMsgCount(groupID, userID int64) error {
	return s.db.Model(&model.Member{}).Where("group_id = ? AND user_id = ?", groupID, userID).
		UpdateColumn("msg_count", gorm.Expr("msg_count + 1")).Error
}

// AddWarn 警告 +1 并返回最新次数（delta 可为负）。expire>0 时，距上次警告超过 expire 的旧警告先清零
func (s *Store) AddWarn(groupID, userID int64, delta int, expire ...time.Duration) (int, error) {
	m0, err := s.Member(groupID, userID)
	if err != nil {
		return 0, err
	}
	q := s.db.Model(&model.Member{}).Where("group_id = ? AND user_id = ?", groupID, userID)
	if len(expire) > 0 && expire[0] > 0 && m0.WarnAt != nil && time.Since(*m0.WarnAt) > expire[0] {
		q.UpdateColumn("warns", 0)
		q = s.db.Model(&model.Member{}).Where("group_id = ? AND user_id = ?", groupID, userID)
	}
	fields := map[string]any{"warns": gorm.Expr("MAX(warns + ?, 0)", delta)}
	if delta > 0 {
		fields["warn_at"] = time.Now()
	}
	if err := q.UpdateColumns(fields).Error; err != nil {
		return 0, err
	}
	m, err := s.Member(groupID, userID)
	if err != nil {
		return 0, err
	}
	return m.Warns, nil
}

// ---------- 用户 ----------

func (s *Store) SaveUser(id int64, username, first, last string) error {
	return s.db.Clauses(clause.OnConflict{UpdateAll: true}).
		Create(&model.User{ID: id, Username: username, FirstName: first, LastName: last}).Error
}

func (s *Store) UserByUsername(username string) (*model.User, error) {
	return first[model.User](s.db.Where("LOWER(username) = ?", strings.ToLower(strings.TrimPrefix(username, "@"))))
}

func (s *Store) UserByID(id int64) (*model.User, error) {
	return first[model.User](s.db.Where("id = ?", id))
}

// ---------- 待验证 ----------

func (s *Store) SavePending(p *model.Pending) error {
	return s.db.Clauses(clause.OnConflict{UpdateAll: true}).Create(p).Error
}

func (s *Store) Pending(groupID, userID int64) (*model.Pending, error) {
	return first[model.Pending](s.db.Where("group_id = ? AND user_id = ?", groupID, userID))
}

// UpdatePending 只更新还存在的待验证记录（不会把已经通过 / 超时的记录写回来），返回是否更新成功
func (s *Store) UpdatePending(groupID, userID int64, fields map[string]any) bool {
	res := s.db.Model(&model.Pending{}).Where("group_id = ? AND user_id = ?", groupID, userID).Updates(fields)
	return res.Error == nil && res.RowsAffected > 0
}

// TakePending 取出并删除（保证一个待验证只被处理一次：验证通过和超时不会同时生效）
func (s *Store) TakePending(groupID, userID int64) (*model.Pending, error) {
	var p *model.Pending
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var err error
		if p, err = first[model.Pending](tx.Where("group_id = ? AND user_id = ?", groupID, userID)); err != nil || p == nil {
			return err
		}
		return tx.Where("group_id = ? AND user_id = ?", groupID, userID).Delete(&model.Pending{}).Error
	})
	return p, err
}

func (s *Store) ExpiredPendings(now time.Time) ([]model.Pending, error) {
	var ps []model.Pending
	return ps, s.db.Where("expires_at <= ?", now).Find(&ps).Error
}

// PendingsOfUser 某用户所有待验证的群（私聊 /start 时用于找到要验证哪个群）
func (s *Store) PendingsOfUser(userID int64) ([]model.Pending, error) {
	var ps []model.Pending
	return ps, s.db.Where("user_id = ?", userID).Find(&ps).Error
}

// ---------- 屏蔽词 ----------

func (s *Store) Filters(groupID int64) ([]model.Filter, error) {
	var fs []model.Filter
	return fs, s.db.Where("group_id = ?", groupID).Order("id").Find(&fs).Error
}

func (s *Store) AddFilter(f *model.Filter) error { return s.db.Create(f).Error }

func (s *Store) DelFilter(groupID int64, pattern string) (bool, error) {
	res := s.db.Where("group_id = ? AND pattern = ?", groupID, pattern).Delete(&model.Filter{})
	return res.RowsAffected > 0, res.Error
}

// ---------- 全局封禁 ----------

func (s *Store) GBanned(userID int64) bool {
	g, _ := first[model.GBan](s.db.Where("user_id = ?", userID))
	return g != nil
}

func (s *Store) SetGBan(userID int64, reason string, on bool) error {
	if !on {
		return s.db.Where("user_id = ?", userID).Delete(&model.GBan{}).Error
	}
	return s.db.Clauses(clause.OnConflict{UpdateAll: true}).Create(&model.GBan{UserID: userID, Reason: reason}).Error
}

var ErrNotFound = errors.New("not found")

// ---------- 验证失败 ----------

// IncFails 待验证记录失败次数 +1，返回最新次数（没有记录返回 -1）
func (s *Store) IncFails(groupID, userID int64) int {
	res := s.db.Model(&model.Pending{}).Where("group_id = ? AND user_id = ?", groupID, userID).
		UpdateColumn("fails", gorm.Expr("fails + 1"))
	if res.RowsAffected == 0 {
		return -1
	}
	p, _ := s.Pending(groupID, userID)
	if p == nil {
		return -1
	}
	return p.Fails
}

// ---------- 群列表 ----------

func (s *Store) Groups() ([]model.Group, error) {
	var gs []model.Group
	return gs, s.db.Order("title").Find(&gs).Error
}

// ---------- 笔记 / 自动回复 ----------

func (s *Store) SaveNote(n *model.Note) error {
	return s.db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "group_id"}, {Name: "name"}},
		DoUpdates: clause.AssignmentColumns([]string{"text", "from_chat", "msg_id", "markdown", "media", "buttons", "file_name", "created_by", "updated_at"})}).Create(n).Error
}

// NoteByID 目录按钮用 ID 取笔记（按钮数据有 64 字节限制）
func (s *Store) NoteByID(groupID int64, id uint) (*model.Note, error) {
	return first[model.Note](s.db.Where("group_id = ? AND id = ?", groupID, id))
}

// NoteByIDAny 私聊里按笔记 ID 取（不知道是哪个群）
func (s *Store) NoteByIDAny(id uint) (*model.Note, error) {
	return first[model.Note](s.db.Where("id = ?", id))
}

// AddNoteLog 记一次私聊取笔记
func (s *Store) AddNoteLog(l *model.NoteLog) error { return s.db.Create(l).Error }

// NoteLogs 本群取笔记记录，新的在前；noteID 为 0 表示全部笔记
func (s *Store) NoteLogs(groupID int64, noteID uint, limit int) ([]model.NoteLog, error) {
	q := s.db.Where("group_id = ?", groupID)
	if noteID > 0 {
		q = q.Where("note_id = ?", noteID)
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	var ls []model.NoteLog
	return ls, q.Order("id DESC").Limit(limit).Find(&ls).Error
}

// NoteViewed 查看次数 +1
func (s *Store) NoteViewed(id uint) {
	s.db.Model(&model.Note{}).Where("id = ?", id).UpdateColumn("views", gorm.Expr("views + 1"))
}

func (s *Store) Note(groupID int64, name string) (*model.Note, error) {
	return first[model.Note](s.db.Where("group_id = ? AND name = ?", groupID, strings.ToLower(name)))
}

func (s *Store) Notes(groupID int64) ([]model.Note, error) {
	var ns []model.Note
	return ns, s.db.Where("group_id = ?", groupID).Order("name").Find(&ns).Error
}

func (s *Store) DelNote(groupID int64, name string) (bool, error) {
	res := s.db.Where("group_id = ? AND name = ?", groupID, strings.ToLower(name)).Delete(&model.Note{})
	return res.RowsAffected > 0, res.Error
}

func (s *Store) SaveReply(r *model.AutoReply) error {
	return s.db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "group_id"}, {Name: "keyword"}},
		DoUpdates: clause.AssignmentColumns([]string{"text", "from_chat", "msg_id", "markdown", "media", "buttons", "match", "delete", "cooldown", "per_user", "chance"})}).Create(r).Error
}

// SetReplyFreq 只改自动回复的频率
func (s *Store) SetReplyFreq(groupID int64, kw string, cooldown int, perUser bool, chance int) (bool, error) {
	res := s.db.Model(&model.AutoReply{}).Where("group_id = ? AND keyword = ?", groupID, strings.ToLower(kw)).
		Updates(map[string]any{"cooldown": cooldown, "per_user": perUser, "chance": chance})
	return res.RowsAffected > 0, res.Error
}

func (s *Store) Replies(groupID int64) ([]model.AutoReply, error) {
	var rs []model.AutoReply
	return rs, s.db.Where("group_id = ?", groupID).Order("keyword").Find(&rs).Error
}

func (s *Store) DelReply(groupID int64, kw string) (bool, error) {
	res := s.db.Where("group_id = ? AND keyword = ?", groupID, strings.ToLower(kw)).Delete(&model.AutoReply{})
	return res.RowsAffected > 0, res.Error
}

// ---------- 白名单 ----------

func (s *Store) Approved(groupID, userID int64) bool {
	a, _ := first[model.Approved](s.db.Where("group_id = ? AND user_id = ?", groupID, userID))
	return a != nil
}

func (s *Store) SetApproved(groupID, userID int64, on bool) error {
	if !on {
		return s.db.Where("group_id = ? AND user_id = ?", groupID, userID).Delete(&model.Approved{}).Error
	}
	return s.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.Approved{GroupID: groupID, UserID: userID}).Error
}

func (s *Store) ApprovedList(groupID int64) ([]model.Approved, error) {
	var as []model.Approved
	return as, s.db.Where("group_id = ?", groupID).Find(&as).Error
}

// ---------- 定时消息 ----------

func (s *Store) AddSchedule(sc *model.Schedule) error { return s.db.Create(sc).Error }

func (s *Store) Schedules(groupID int64) ([]model.Schedule, error) {
	var ss []model.Schedule
	q := s.db.Order("at")
	if groupID != 0 {
		q = q.Where("group_id = ?", groupID)
	}
	return ss, q.Find(&ss).Error
}

func (s *Store) DelSchedule(groupID int64, id uint) (bool, error) {
	res := s.db.Where("group_id = ? AND id = ?", groupID, id).Delete(&model.Schedule{})
	return res.RowsAffected > 0, res.Error
}

func (s *Store) MarkScheduleRun(id uint, day string) error {
	return s.db.Model(&model.Schedule{}).Where("id = ?", id).Update("last_run", day).Error
}

// ---------- 统计 ----------

// Count 当日计数 +1，field 为 Stat 的列名（messages / joins / verified / deleted / banned / muted）
func (s *Store) Count(groupID int64, day, field string) {
	s.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.Stat{GroupID: groupID, Day: day})
	s.db.Model(&model.Stat{}).Where("group_id = ? AND day = ?", groupID, day).UpdateColumn(field, gorm.Expr(field+" + 1"))
}

func (s *Store) Stats(groupID int64, days []string) ([]model.Stat, error) {
	var ss []model.Stat
	return ss, s.db.Where("group_id = ? AND day IN ?", groupID, days).Order("day").Find(&ss).Error
}

// ---------- 操作日志 ----------

const maxLogs = 5000

func (s *Store) AddLog(text string) {
	s.db.Create(&model.ActionLog{Text: text})
	// 偶尔清理，保留最近 maxLogs 条
	var id uint
	if s.db.Model(&model.ActionLog{}).Select("id").Order("id desc").Offset(maxLogs).Limit(1).Scan(&id); id > 0 {
		s.db.Where("id <= ?", id).Delete(&model.ActionLog{})
	}
}

func (s *Store) Logs(limit int, q string) ([]model.ActionLog, error) {
	var ls []model.ActionLog
	db := s.db.Order("id desc").Limit(limit)
	if q != "" {
		db = db.Where("text LIKE ?", "%"+q+"%")
	}
	return ls, db.Find(&ls).Error
}

func (s *Store) GBans() ([]model.GBan, error) {
	var gs []model.GBan
	return gs, s.db.Order("created_at desc").Find(&gs).Error
}

func (s *Store) DelFilterByID(groupID int64, id uint) (bool, error) {
	res := s.db.Where("group_id = ? AND id = ?", groupID, id).Delete(&model.Filter{})
	return res.RowsAffected > 0, res.Error
}

func (s *Store) Warned(groupID int64) ([]model.Member, error) {
	var ms []model.Member
	return ms, s.db.Where("group_id = ? AND warns > 0", groupID).Order("warns desc").Limit(200).Find(&ms).Error
}

// ActiveMembers 机器人见过、且未退群的成员
func (s *Store) ActiveMembers(groupID int64) ([]model.Member, error) {
	var ms []model.Member
	return ms, s.db.Where("group_id = ? AND has_left = ?", groupID, false).Find(&ms).Error
}

func (s *Store) UserBio(userID int64) (*model.UserBio, error) {
	return first[model.UserBio](s.db.Where("user_id = ?", userID))
}

func (s *Store) SaveUserBio(b *model.UserBio) error { return s.db.Save(b).Error }

func (s *Store) SaveBioReport(r *model.BioReport) error {
	if err := s.db.Create(r).Error; err != nil {
		return err
	}
	// 每个群只留最近 20 份
	return s.db.Where("group_id = ? AND id NOT IN (?)", r.GroupID,
		s.db.Model(&model.BioReport{}).Select("id").Where("group_id = ?", r.GroupID).Order("id desc").Limit(20)).
		Delete(&model.BioReport{}).Error
}

func (s *Store) BioReport(id uint) (*model.BioReport, error) {
	return first[model.BioReport](s.db.Where("id = ?", id))
}

func (s *Store) LatestBioReport(groupID int64) (*model.BioReport, error) {
	return first[model.BioReport](s.db.Where("group_id = ?", groupID).Order("id desc"))
}

// ---------- 定时广告 ----------

func (s *Store) Ads(groupID int64) ([]model.Ad, error) {
	var as []model.Ad
	q := s.db.Order("id")
	if groupID != 0 {
		q = q.Where("group_id = ?", groupID)
	}
	return as, q.Find(&as).Error
}

func (s *Store) Ad(groupID int64, id uint) (*model.Ad, error) {
	return first[model.Ad](s.db.Where("group_id = ? AND id = ?", groupID, id))
}

// SaveAd 新建或整条保存。新建时 gorm 会跳过带 default 标签的零值字段（enabled / delete_prev 默认 true），
// 导致面板里取消勾选「启用」「删掉上一条」不生效，所以新建后再把这两个布尔值明确写一次。
func (s *Store) SaveAd(a *model.Ad) error {
	if a.ID != 0 {
		return s.db.Save(a).Error
	}
	enabled, delPrev := a.Enabled, a.DeletePrev // Create 可能用 RETURNING 把默认值写回结构体，先记下
	if err := s.db.Create(a).Error; err != nil {
		return err
	}
	a.Enabled, a.DeletePrev = enabled, delPrev
	return s.db.Model(&model.Ad{}).Where("id = ?", a.ID).
		UpdateColumns(map[string]any{"enabled": enabled, "delete_prev": delPrev}).Error
}

func (s *Store) UpdateAd(id uint, fields map[string]any) error {
	return s.db.Model(&model.Ad{}).Where("id = ?", id).Updates(fields).Error
}

func (s *Store) DelAd(groupID int64, id uint) (bool, error) {
	res := s.db.Where("group_id = ? AND id = ?", groupID, id).Delete(&model.Ad{})
	return res.RowsAffected > 0, res.Error
}

// ---------- 运行时设置 ----------

func (s *Store) Setting(key string) string {
	var v model.Setting
	if s.db.Where("key = ?", key).Limit(1).Find(&v).Error != nil {
		return ""
	}
	return v.Value
}

func (s *Store) SetSetting(key, value string) error {
	return s.db.Save(&model.Setting{Key: key, Value: value}).Error
}

// DeleteGroup 忘掉一个群：删除设置和群内容（屏蔽词、笔记、自动回复、定时、广告、白名单、待验证）。
// 处罚记录、日志、统计保留。
func (s *Store) DeleteGroup(id int64) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		for _, m := range []any{&model.Filter{}, &model.Note{}, &model.AutoReply{}, &model.Schedule{}, &model.Ad{}, &model.Approved{}, &model.Pending{}} {
			if err := tx.Where("group_id = ?", id).Delete(m).Error; err != nil {
				return err
			}
		}
		return tx.Where("id = ?", id).Delete(&model.Group{}).Error
	})
}

// ReportDistinct 同一条消息有多少个不同的人举报（Report 表按消息去重，Count 已经是不同人数）
func (s *Store) DB() *gorm.DB { return s.db }

// Backup 用 VACUUM INTO 生成一份完整备份
func (s *Store) Backup(path string) error {
	return s.db.Exec("VACUUM INTO ?", path).Error
}

// MembersPage 成员分页（清理已注销账号用）
func (s *Store) AllActiveMemberIDs(groupID int64) []int64 {
	var ids []int64
	s.db.Model(&model.Member{}).Where("group_id = ? AND has_left = ?", groupID, false).Pluck("user_id", &ids)
	return ids
}

// HasGroup 群是否存在（不会像 Group 那样自动创建）
func (s *Store) HasGroup(id int64) bool {
	var n int64
	s.db.Model(&model.Group{}).Where("id = ?", id).Count(&n)
	return n > 0
}
