// Package queue 按 key 串行执行任务：同一 key 的任务按提交顺序依次执行，不同 key 之间并发。
//
// 用途：同一访客的消息、相册、验证后的补发都在同一条队列里执行，
// 既保证顺序、避免并发写同一用户的状态，又不会因为某个用户触发 429 重试而卡住所有人。
package queue

import (
	"log/slog"
	"strconv"
	"sync"
)

// DefaultSize 每个 key 最多排队的任务数，超出直接丢弃（本身也是一种防刷屏）
const DefaultSize = 64

type Keyed struct {
	mu     sync.Mutex
	queues map[string]chan func()
	size   int
}

func New(size int) *Keyed {
	if size <= 0 {
		size = DefaultSize
	}
	return &Keyed{queues: map[string]chan func(){}, size: size}
}

// Do 把任务放入 key 对应的队列，立即返回。队列已满返回 false。
func (k *Keyed) Do(key string, f func()) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	q, ok := k.queues[key]
	if !ok {
		q = make(chan func(), k.size)
		k.queues[key] = q
		go k.worker(key, q)
	}
	select {
	case q <- f:
		return true
	default:
		slog.Warn("queue full, task dropped", "key", key)
		return false
	}
}

// worker 依次执行任务；队列空了就退出并删除自己，下次有任务再新建
func (k *Keyed) worker(key string, q chan func()) {
	for {
		k.mu.Lock()
		select {
		case f := <-q:
			k.mu.Unlock()
			run(key, f)
		default:
			delete(k.queues, key)
			k.mu.Unlock()
			return
		}
	}
}

func run(key string, f func()) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("queue task panic", "key", key, "panic", r)
		}
	}()
	f()
}

// UserKey 访客私聊侧队列的 key
func UserKey(id int64) string { return "u:" + strconv.FormatInt(id, 10) }

// TopicKey 管理群话题侧队列的 key
func TopicKey(thread int) string { return "t:" + strconv.Itoa(thread) }
