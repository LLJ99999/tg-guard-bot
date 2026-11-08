package admin

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"oai_bot/internal/model"
	"oai_bot/internal/store"
)

func (a *api) routesGrowth(v *gin.RouterGroup) {
	v.GET("/groups/:id/topics", a.topics)
	v.GET("/groups/:id/admins", a.groupAdmins)
	v.GET("/groups/:id/points", a.points)
	v.POST("/groups/:id/points", a.addPoints)
	v.GET("/groups/:id/ranks", a.ranks)
	v.GET("/groups/:id/invites", a.inviters)
	v.GET("/groups/:id/invites/:uid", a.invitees)
	v.GET("/groups/:id/tools", a.toolsStatus)
	v.POST("/groups/:id/zombies", a.zombies)
	v.POST("/groups/:id/rotate-link", a.rotateLink)
	v.GET("/groups/:id/userinfo/:uid", a.userInfo)
	v.POST("/groups/:id/welcome-preview", a.welcomePreview)
	v.POST("/captures", a.newCapture)
	v.GET("/captures/:token", a.getCapture)
}

func (a *api) topics(c *gin.Context) {
	id, ok := gid(c)
	if !ok {
		return
	}
	ts := a.g.TopicsOf(id)
	if ts == nil {
		ts = []model.ForumTopic{}
	}
	c.JSON(http.StatusOK, ts)
}

func (a *api) groupAdmins(c *gin.Context) {
	id, ok := gid(c)
	if !ok {
		return
	}
	out := a.g.AdminsOf(c.Request.Context(), id)
	if out == nil {
		out = []map[string]any{}
	}
	c.JSON(http.StatusOK, out)
}

func (a *api) points(c *gin.Context) {
	id, ok := gid(c)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.Query("offset"))
	rows, total, err := a.g.Store().PointsRank(id, min(max(limit, 1), 200), max(offset, 0))
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": rows, "total": total})
}

func (a *api) addPoints(c *gin.Context) {
	id, ok := gid(c)
	if !ok {
		return
	}
	var in struct {
		UserID int64 `json:"user_id"`
		Delta  int   `json:"delta"`
	}
	if c.ShouldBindJSON(&in) != nil || in.UserID == 0 || in.Delta == 0 || in.Delta > 1000000 || in.Delta < -1000000 {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	n, err := a.g.Store().AddPoints(id, in.UserID, in.Delta)
	respond(c, gin.H{"points": n}, err)
}

func (a *api) ranks(c *gin.Context) {
	id, ok := gid(c)
	if !ok {
		return
	}
	var text string
	switch c.Query("kind") {
	case "week":
		text = a.g.ActivityRankText(id, true)
	case "points":
		text = a.g.PointsRankText(id)
	case "invites":
		text = a.g.InviteRankText(id)
	default:
		text = a.g.ActivityRankText(id, false)
	}
	c.JSON(http.StatusOK, gin.H{"text": stripTags(text)})
}

func stripTags(s string) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		switch {
		case r == '<':
			in = true
		case r == '>':
			in = false
		case !in:
			b.WriteRune(r)
		}
	}
	return strings.NewReplacer("&lt;", "<", "&gt;", ">", "&amp;", "&").Replace(b.String())
}

func (a *api) toolsStatus(c *gin.Context) {
	id, ok := gid(c)
	if !ok {
		return
	}
	running, last := a.g.ZombieStatus(id)
	c.JSON(http.StatusOK, gin.H{"zombie_running": running, "zombie_last": last})
}

func (a *api) zombies(c *gin.Context) {
	id, ok := gid(c)
	if !ok {
		return
	}
	if !a.g.StartZombieScan(id, false) {
		fail(c, http.StatusBadRequest, "正在清理中")
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (a *api) rotateLink(c *gin.Context) {
	id, ok := gid(c)
	if !ok {
		return
	}
	link, err := a.g.RotateLink(c.Request.Context(), id)
	if err != nil {
		fail(c, http.StatusBadRequest, "更换失败，机器人需要「邀请用户」权限："+err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"link": link})
}

func (a *api) userInfo(c *gin.Context) {
	id, ok := gid(c)
	if !ok {
		return
	}
	uid, _ := strconv.ParseInt(c.Param("uid"), 10, 64)
	c.JSON(http.StatusOK, gin.H{"text": stripTags(a.g.UserInfo(id, uid))})
}

func (a *api) newCapture(c *gin.Context) {
	var in struct {
		Purpose string `json:"purpose"`
	}
	c.ShouldBindJSON(&in)
	switch in.Purpose {
	case "text", "media", "any":
	default:
		in.Purpose = "any"
	}
	cp, link, err := a.g.NewCapture(in.Purpose)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"token": cp.Token, "link": link})
}

func (a *api) getCapture(c *gin.Context) {
	cp := a.g.CaptureOf(c.Param("token"))
	if cp == nil {
		fail(c, http.StatusNotFound, "已失效")
		return
	}
	c.JSON(http.StatusOK, cp)
}

func (a *api) welcomePreview(c *gin.Context) {
	id, ok := gid(c)
	if !ok {
		return
	}
	sent, failed := a.g.PreviewWelcome(c.Request.Context(), id)
	if failed == nil {
		failed = []string{}
	}
	c.JSON(http.StatusOK, gin.H{"sent": sent, "failed": failed})
}

// inviters 邀请人列表：每人的专属链接、拉来多少人、有效 / 待验证 / 已退群
func (a *api) inviters(c *gin.Context) {
	id, ok := gid(c)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.Query("offset"))
	rows, total := a.g.Store().Inviters(id, min(max(limit, 1), 200), max(offset, 0))
	if rows == nil {
		rows = []store.InviterRow{}
	}
	c.JSON(http.StatusOK, gin.H{"items": rows, "total": total})
}

// invitees 某个成员通过自己链接拉来的人
func (a *api) invitees(c *gin.Context) {
	id, ok := gid(c)
	if !ok {
		return
	}
	uid, err := strconv.ParseInt(c.Param("uid"), 10, 64)
	if err != nil {
		fail(c, http.StatusBadRequest, "成员 ID 不对")
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	offset, _ := strconv.Atoi(c.Query("offset"))
	rows, total := a.g.Store().InvitesBy(id, uid, min(max(limit, 1), 500), max(offset, 0))
	if rows == nil {
		rows = []store.InviteRow{}
	}
	c.JSON(http.StatusOK, gin.H{"items": rows, "total": total})
}
