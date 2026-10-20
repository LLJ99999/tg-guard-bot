package guard

import (
	"context"
	"crypto/sha1"
	"strings"
	"sync"
	"time"
	"unicode"
)

const (
	dupWindow    = 10 * time.Minute
	dupUsers     = 3 // 不同人发同一段文字达到此人数，判定为广告波
	dupSelf      = 3 // 同一人重复发同一段文字达到此次数
	dupMinLength = 12
)

// dupTracker 按群记录最近出现的文字指纹
type dupTracker struct {
	mu sync.Mutex
	m  map[int64]map[[20]byte]*dupEntry
}

type dupEntry struct {
	first time.Time
	users map[int64]int // 发送者 -> 次数
	msgs  []dupMsg
}

type dupMsg struct {
	userID int64
	msgID  int
}

func newDupTracker() *dupTracker { return &dupTracker{m: map[int64]map[[20]byte]*dupEntry{}} }

// normalize 去掉空白、标点、表情、大小写差异，让「加我vx：abc」「加 我 V X abc!!」得到同一指纹
func normalize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// hit 记录一条文字消息。触发时返回需要删除的消息（之前的同文消息也一起删）和涉及的发送者
func (d *dupTracker) hit(chatID, userID int64, msgID int, text string) (msgs []dupMsg, triggered bool) {
	n := normalize(text)
	if len([]rune(n)) < dupMinLength {
		return nil, false
	}
	key := sha1.Sum([]byte(n))
	now := time.Now()
	d.mu.Lock()
	defer d.mu.Unlock()
	byKey := d.m[chatID]
	if byKey == nil {
		byKey = map[[20]byte]*dupEntry{}
		d.m[chatID] = byKey
	}
	e := byKey[key]
	if e == nil || now.Sub(e.first) > dupWindow {
		e = &dupEntry{first: now, users: map[int64]int{}}
		byKey[key] = e
	}
	e.users[userID]++
	e.msgs = append(e.msgs, dupMsg{userID: userID, msgID: msgID})
	if len(e.users) >= dupUsers || e.users[userID] >= dupSelf {
		msgs = e.msgs
		delete(byKey, key)
		return msgs, true
	}
	return nil, false
}

func (d *dupTracker) cleanupLoop(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			now := time.Now()
			d.mu.Lock()
			for chat, byKey := range d.m {
				for k, e := range byKey {
					if now.Sub(e.first) > dupWindow {
						delete(byKey, k)
					}
				}
				if len(byKey) == 0 {
					delete(d.m, chat)
				}
			}
			d.mu.Unlock()
		}
	}
}
