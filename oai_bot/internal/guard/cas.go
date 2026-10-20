package guard

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"oai_bot/internal/config"
)

// casClient 查询 CAS（cas.chat）共享黑名单，结果缓存 1 小时。查询失败按未封禁处理，不影响正常用户
type casClient struct {
	cfg   config.CAS
	http  *http.Client
	mu    sync.Mutex
	cache map[int64]casEntry
}

type casEntry struct {
	banned bool
	exp    time.Time
}

func newCASClient(c config.CAS) *casClient {
	return &casClient{cfg: c, http: &http.Client{Timeout: 3 * time.Second}, cache: map[int64]casEntry{}}
}

func (c *casClient) banned(ctx context.Context, userID int64) bool {
	if !c.cfg.Enabled || c.cfg.API == "" {
		return false
	}
	c.mu.Lock()
	if e, ok := c.cache[userID]; ok && time.Now().Before(e.exp) {
		c.mu.Unlock()
		return e.banned
	}
	c.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s?user_id=%d", c.cfg.API, userID), nil)
	if err != nil {
		return false
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var r struct {
		OK bool `json:"ok"` // ok=true 表示在黑名单里
	}
	if json.NewDecoder(resp.Body).Decode(&r) != nil {
		return false
	}
	c.mu.Lock()
	if len(c.cache) > 50000 {
		c.cache = map[int64]casEntry{}
	}
	c.cache[userID] = casEntry{banned: r.OK, exp: time.Now().Add(time.Hour)}
	c.mu.Unlock()
	return r.OK
}
