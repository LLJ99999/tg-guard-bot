package queue

import (
	"sync"
	"testing"
	"time"
)

func TestOrderPerKeyAndParallelAcrossKeys(t *testing.T) {
	q := New(100)
	var mu sync.Mutex
	got := map[string][]int{}
	var wg sync.WaitGroup
	for i := range 50 {
		for _, k := range []string{"a", "b"} {
			wg.Add(1)
			q.Do(k, func() {
				defer wg.Done()
				mu.Lock()
				got[k] = append(got[k], i)
				mu.Unlock()
			})
		}
	}
	wg.Wait()
	for _, k := range []string{"a", "b"} {
		for i, v := range got[k] {
			if v != i {
				t.Fatalf("key %s out of order: %v", k, got[k])
			}
		}
	}

	// 一个 key 卡住不影响另一个 key
	block := make(chan struct{})
	q.Do("slow", func() { <-block })
	done := make(chan struct{})
	q.Do("fast", func() { close(done) })
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("fast key blocked by slow key")
	}
	close(block)
}

func TestFullQueueDrops(t *testing.T) {
	q := New(1)
	block := make(chan struct{})
	started := make(chan struct{})
	q.Do("k", func() { close(started); <-block })
	<-started
	if !q.Do("k", func() {}) {
		t.Fatal("first queued task should fit")
	}
	if q.Do("k", func() {}) {
		t.Fatal("queue over capacity should drop")
	}
	close(block)
}
