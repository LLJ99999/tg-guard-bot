package admin

import (
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"oai_bot/internal/config"
	"oai_bot/internal/guard"
	"oai_bot/internal/webapp"
	"oai_bot/pkg/tginit"
)

// Telegram Mini App 管理面板：和 AstrBot 后台同一套前端，挂在 web.public_url/app/ 下。
// 用 Telegram 的 initData 识别是谁打开的：群相关接口按「谁能在 Telegram 里管理」检查，
// 全局设置（AI 接口、全局封禁、联邦、日志、学习样本）只有机器人主人能用。

const tmaUIDKey = "tma_uid"

func tmaUser(c *gin.Context) (int64, bool) {
	v, ok := c.Get(tmaUIDKey)
	if !ok {
		return 0, false
	}
	id, ok := v.(int64)
	return id, ok
}

// 非机器人主人也能调用的全局接口
var tmaPublic = map[string]bool{
	"GET /status": true, "GET /groups": true, "POST /captures": true, "GET /captures/:token": true,
	"GET /reports": true, "POST /reports/:rid": true, // 接口内部再按群过滤
}

// MountMiniApp 把面板挂到对外的 web 服务上（需要配置 web.public_url）
func MountMiniApp(r *gin.Engine, cfg *config.Config, g *guard.Guard) {
	a := &api{cfg: cfg, g: g}
	app := r.Group("/app")
	static, _ := fs.Sub(webapp.Files, "static")
	serve := func(name string) gin.HandlerFunc {
		return func(c *gin.Context) {
			c.Header("Cache-Control", "no-cache")
			c.FileFromFS(name, http.FS(static))
		}
	}
	app.GET("/", serve("/"))
	for _, f := range []string{"app.js", "styles.css", "miniapp.js"} {
		app.GET("/"+f, serve(f))
	}
	a.register(app.Group("/api", a.tmaAuth))
}

func (a *api) tmaAuth(c *gin.Context) {
	u, err := tginit.Validate(c.GetHeader("X-Telegram-Init-Data"), a.cfg.Bot.Token, 24*time.Hour, time.Now())
	if err != nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "登录已过期，请从机器人私聊里重新打开面板"})
		return
	}
	c.Set(tmaUIDKey, u.ID)
	full := c.FullPath()
	if raw := c.Param("id"); raw != "" && strings.Contains(full, "/groups/:id") {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || !a.cfg.GroupAllowed(id) || !a.g.Store().HasGroup(id) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "群不存在"})
			return
		}
		grp, err := a.g.Store().Group(id, "")
		if err != nil || !a.g.CanPanel(c.Request.Context(), grp, u.ID) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "你没有管理这个群的权限"})
			return
		}
		return
	}
	if a.cfg.IsOwner(u.ID) {
		return
	}
	if !tmaPublic[c.Request.Method+" "+strings.TrimPrefix(full, "/app/api")] {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "只有机器人主人可以使用全局设置"})
	}
}
