package admin

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

func (a *api) punishments(c *gin.Context) {
	id, ok := gid(c)
	if !ok {
		return
	}
	n, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	if n < 1 || n > 500 {
		n = 100
	}
	ps, err := a.g.Store().Punishments(id, c.Query("q"), c.Query("status"), n)
	respond(c, ps, err)
}

func (a *api) undoPunishment(c *gin.Context) {
	id, ok := gid(c)
	if !ok {
		return
	}
	pid, _ := strconv.Atoi(c.Param("pid"))
	var in struct {
		Learn   bool `json:"learn"`
		Approve bool `json:"approve"`
	}
	c.ShouldBindJSON(&in)
	if err := a.g.UndoPunishment(c.Request.Context(), id, uint(pid), "面板", in.Learn, in.Approve); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (a *api) members(c *gin.Context) {
	id, ok := gid(c)
	if !ok {
		return
	}
	n, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if n < 1 || n > 200 {
		n = 50
	}
	off, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	rows, total, err := a.g.Store().MemberList(id, c.Query("q"), c.Query("filter"), n, max(off, 0))
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": rows, "total": total})
}

func (a *api) memberAction(c *gin.Context) {
	id, ok := gid(c)
	if !ok {
		return
	}
	uid, err := strconv.ParseInt(c.Param("uid"), 10, 64)
	if err != nil || uid <= 0 {
		fail(c, http.StatusBadRequest, "用户 ID 无效")
		return
	}
	var in struct {
		Action  string `json:"action"`
		Minutes int    `json:"minutes"`
	}
	if c.ShouldBindJSON(&in) != nil || in.Minutes < 0 || in.Minutes > 525600 {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	text, err := a.g.MemberAction(c.Request.Context(), id, uid, in.Action, in.Minutes)
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "text": text})
}

func (a *api) samples(c *gin.Context) {
	xs, err := a.g.Store().Samples(c.Query("kind"))
	respond(c, xs, err)
}

func (a *api) addSample(c *gin.Context) {
	var in struct {
		Kind    string `json:"kind"`
		Text    string `json:"text"`
		GroupID int64  `json:"group_id"`
	}
	if c.ShouldBindJSON(&in) != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	x, err := a.g.AddSample(in.GroupID, in.Kind, in.Text, "panel", "面板")
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	c.JSON(http.StatusOK, x)
}

func (a *api) delSample(c *gin.Context) {
	sid, _ := strconv.Atoi(c.Param("sid"))
	ok, err := a.g.DelSample(uint(sid))
	deleted(c, ok, err)
}
