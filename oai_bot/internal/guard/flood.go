package guard

import (
	"context"
	"sync"
	"time"
)

// floodTracker 滑动窗口刷屏检测。只保存最近的消息，过期的 key 定期清理，不会无限增长
type floodTracker struct {
	mu sync.Mutex
	m  map[string][]floodHit
}

type floodHit struct {
	at    time.Time
	msgID int
}

func newFloodTracker() *floodTracker { return &floodTracker{m: map[string][]floodHit{}} }

// hit 记录一条消息；窗口内超过 count 条时返回窗口内全部消息 ID（用于批量删除）并清空
func (f *floodTracker) hit(key string, msgID, count int, window time.Duration) []int {
	if count <= 0 || window <= 0 {
		return nil
	}
	now := time.Now()
	f.mu.Lock()
	defer f.mu.Unlock()
	hits := f.m[key]
	keep := hits[:0]
	for _, h := range hits {
		if now.Sub(h.at) <= window {
			keep = append(keep, h)
		}
	}
	keep = append(keep, floodHit{at: now, msgID: msgID})
	if len(keep) > count {
		ids := make([]int, len(keep))
		for i, h := range keep {
			ids[i] = h.msgID
		}
		delete(f.m, key)
		return ids
	}
	f.m[key] = keep
	return nil
}

func (f *floodTracker) cleanupLoop(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			f.mu.Lock()
			now := time.Now()
			for k, hits := range f.m {
				if len(hits) == 0 || now.Sub(hits[len(hits)-1].at) > 5*time.Minute {
					delete(f.m, k)
				}
			}
			f.mu.Unlock()
		}
	}
}
