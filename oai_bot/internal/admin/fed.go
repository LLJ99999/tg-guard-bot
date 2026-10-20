package admin

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"oai_bot/internal/model"
)

func (a *api) routesV14(v *gin.RouterGroup) {
	v.GET("/feds", a.listFeds)
	v.POST("/feds", a.createFed)
	v.PATCH("/feds/:fid", a.renameFed)
	v.DELETE("/feds/:fid", a.deleteFed)
	v.POST("/feds/:fid/groups", a.fedAddGroup)
	v.DELETE("/feds/:fid/groups/:id", a.fedDelGroup)
	v.POST("/feds/:fid/admins", a.fedAddAdmin)
	v.DELETE("/feds/:fid/admins/:uid", a.fedDelAdmin)
	v.GET("/feds/:fid/bans", a.fedBans)
	v.POST("/feds/:fid/bans", a.fedBan)
	v.DELETE("/feds/:fid/bans/:uid", a.fedUnban)

	v.GET("/reports", a.listReports)
	v.POST("/reports/:rid", a.handleReport)

	v.POST("/groups/:id/copy", a.copyGroup)
	v.GET("/groups/:id/export", a.exportGroup)
	v.POST("/groups/:id/import", a.importGroup)
}

type fedOut struct {
	model.Fed
	Groups []gin.H `json:"groups"`
	Admins []int64 `json:"admins"`
	Bans   int64   `json:"bans"`
}

func (a *api) listFeds(c *gin.Context) {
	st := a.g.Store()
	fs, err := st.Feds()
	if err != nil {
		respond(c, nil, err)
		return
	}
	out := make([]fedOut, 0, len(fs))
	for _, f := range fs {
		gs, _ := st.FedGroups(f.ID)
		as, _ := st.FedAdmins(f.ID)
		o := fedOut{Fed: f, Groups: []gin.H{}, Admins: as, Bans: st.FedBanCount(f.ID)}
		if o.Admins == nil {
			o.Admins = []int64{}
		}
		for _, g := range gs {
			o.Groups = append(o.Groups, gin.H{"id": g.ID, "title": g.Title})
		}
		out = append(out, o)
	}
	c.JSON(http.StatusOK, out)
}

func (a *api) createFed(c *gin.Context) {
	var in struct {
		Name    string `json:"name"`
		OwnerID int64  `json:"owner_id"`
	}
	if c.ShouldBindJSON(&in) != nil || strings.TrimSpace(in.Name) == "" || len([]rune(in.Name)) > 64 {
		fail(c, http.StatusBadRequest, "联邦名称为空或过长")
		return
	}
	if in.OwnerID == 0 && len(a.cfg.Bot.OwnerIDs) > 0 {
		in.OwnerID = a.cfg.Bot.OwnerIDs[0]
	}
	f, err := a.g.Store().CreateFed(strings.TrimSpace(in.Name), in.OwnerID)
	respond(c, f, err)
}

func (a *api) renameFed(c *gin.Context) {
	var in struct {
		Name string `json:"name"`
	}
	if c.ShouldBindJSON(&in) != nil || strings.TrimSpace(in.Name) == "" {
		fail(c, http.StatusBadRequest, "需要 name")
		return
	}
	respond(c, gin.H{"ok": true}, a.g.Store().RenameFed(c.Param("fid"), strings.TrimSpace(in.Name)))
}

func (a *api) deleteFed(c *gin.Context) {
	respond(c, gin.H{"ok": true}, a.g.Store().DeleteFed(c.Param("fid")))
}

func (a *api) fedAddGroup(c *gin.Context) {
	var in struct {
		GroupID int64 `json:"group_id"`
	}
	if c.ShouldBindJSON(&in) != nil || in.GroupID == 0 {
		fail(c, http.StatusBadRequest, "需要 group_id")
		return
	}
	respond(c, gin.H{"ok": true}, a.g.JoinFed(c.Request.Context(), in.GroupID, c.Param("fid")))
}

func (a *api) fedDelGroup(c *gin.Context) {
	id, ok := gid(c)
	if !ok {
		return
	}
	respond(c, gin.H{"ok": true}, a.g.JoinFed(c.Request.Context(), id, ""))
}

func pathUID(c *gin.Context) (int64, bool) {
	uid, err := strconv.ParseInt(c.Param("uid"), 10, 64)
	if err != nil {
		fail(c, http.StatusBadRequest, "user_id 无效")
		return 0, false
	}
	return uid, true
}

func (a *api) fedAddAdmin(c *gin.Context) {
	var in userIn
	if c.ShouldBindJSON(&in) != nil || in.UserID == 0 {
		fail(c, http.StatusBadRequest, "需要 user_id")
		return
	}
	respond(c, gin.H{"ok": true}, a.g.Store().SetFedAdmin(c.Param("fid"), in.UserID, true))
}

func (a *api) fedDelAdmin(c *gin.Context) {
	uid, ok := pathUID(c)
	if !ok {
		return
	}
	respond(c, gin.H{"ok": true}, a.g.Store().SetFedAdmin(c.Param("fid"), uid, false))
}

func (a *api) fedBans(c *gin.Context) {
	bs, err := a.g.Store().FedBans(c.Param("fid"))
	respond(c, bs, err)
}

func (a *api) fedBan(c *gin.Context) {
	var in userIn
	if c.ShouldBindJSON(&in) != nil || in.UserID == 0 {
		fail(c, http.StatusBadRequest, "需要 user_id")
		return
	}
	n, err := a.g.FedBan(c.Request.Context(), c.Param("fid"), in.UserID, in.Reason, 0, true)
	respond(c, gin.H{"ok": true, "groups": n}, err)
}

func (a *api) fedUnban(c *gin.Context) {
	uid, ok := pathUID(c)
	if !ok {
		return
	}
	n, err := a.g.FedBan(c.Request.Context(), c.Param("fid"), uid, "", 0, false)
	respond(c, gin.H{"ok": true, "groups": n}, err)
}

func (a *api) listReports(c *gin.Context) {
	gidq, _ := strconv.ParseInt(c.Query("group"), 10, 64)
	status := c.DefaultQuery("status", model.ReportOpen)
	if status == "all" {
		status = ""
	}
	rs, err := a.g.Store().Reports(gidq, status, 200)
	if rs == nil {
		rs = []model.Report{}
	}
	respond(c, rs, err)
}

func (a *api) handleReport(c *gin.Context) {
	rid, err := strconv.ParseUint(c.Param("rid"), 10, 64)
	var in struct {
		Action string `json:"action"`
	}
	if err != nil || c.ShouldBindJSON(&in) != nil {
		fail(c, http.StatusBadRequest, "参数无效")
		return
	}
	text, err := a.g.HandleReport(c.Request.Context(), uint(rid), in.Action, "后台面板")
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "text": text})
}

// groupSettings 群设置里可以复制 / 导出的字段（跟面板可编辑字段一致）
func (a *api) groupSettings(id int64, withText bool) (map[string]any, error) {
	grp, err := a.g.Store().Group(id, "")
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(grp)
	var all map[string]any
	json.Unmarshal(raw, &all)
	out := map[string]any{}
	for k := range editable {
		if !withText && (k == "welcome" || k == "rules") {
			continue
		}
		if v, ok := all[k]; ok {
			out[k] = v
		}
	}
	return out, nil
}

func (a *api) copyGroup(c *gin.Context) {
	id, ok := gid(c)
	if !ok {
		return
	}
	var in struct {
		Targets []int64 `json:"targets"`
		Text    bool    `json:"text"`    // 同时复制欢迎语和群规
		Filters bool    `json:"filters"` // 同时复制屏蔽词
	}
	if c.ShouldBindJSON(&in) != nil || len(in.Targets) == 0 {
		fail(c, http.StatusBadRequest, "请选择要复制到的群")
		return
	}
	set, err := a.groupSettings(id, in.Text)
	if err != nil {
		respond(c, nil, err)
		return
	}
	st := a.g.Store()
	known := map[int64]bool{}
	gs, _ := st.Groups()
	for _, g := range gs {
		known[g.ID] = true
	}
	done, filters := 0, 0
	for _, t := range in.Targets {
		if t == id || !known[t] || !a.cfg.GroupAllowed(t) {
			continue
		}
		if err := st.UpdateGroup(t, set); err != nil {
			respond(c, nil, err)
			return
		}
		if in.Filters {
			n, _ := st.CopyFilters(id, t)
			filters += n
		}
		done++
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "groups": done, "filters": filters})
}

type exportData struct {
	Version  int              `json:"version"`
	Settings map[string]any   `json:"settings"`
	Filters  []map[string]any `json:"filters"`
}

func (a *api) exportGroup(c *gin.Context) {
	id, ok := gid(c)
	if !ok {
		return
	}
	set, err := a.groupSettings(id, true)
	if err != nil {
		respond(c, nil, err)
		return
	}
	fs, _ := a.g.Store().Filters(id)
	out := exportData{Version: 1, Settings: set, Filters: []map[string]any{}}
	for _, f := range fs {
		out.Filters = append(out.Filters, map[string]any{"pattern": f.Pattern, "regex": f.Regex, "action": f.Action})
	}
	c.JSON(http.StatusOK, out)
}

func (a *api) importGroup(c *gin.Context) {
	id, ok := gid(c)
	if !ok {
		return
	}
	var in exportData
	if c.ShouldBindJSON(&in) != nil || in.Settings == nil {
		fail(c, http.StatusBadRequest, "不是有效的设置备份文件")
		return
	}
	// 跳过本版本不认识的字段，其余照常校验
	clean := map[string]any{}
	for k, v := range in.Settings {
		if _, ok := editable[k]; ok {
			clean[k] = v
		}
	}
	if !a.g.Info()["ai"].(bool) {
		delete(clean, "ai")
	}
	if !a.g.Info()["captcha"].(bool) && clean["captcha_type"] == model.CaptchaWeb {
		delete(clean, "captcha_type")
	}
	fields, err := validate(clean)
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	st := a.g.Store()
	if err := st.UpdateGroup(id, fields); err != nil {
		respond(c, nil, err)
		return
	}
	have := map[string]bool{}
	old, _ := st.Filters(id)
	for _, f := range old {
		have[f.Pattern] = true
	}
	added := 0
	for _, f := range in.Filters {
		p, _ := f["pattern"].(string)
		if p == "" || have[p] || len(p) > 500 {
			continue
		}
		rx, _ := f["regex"].(bool)
		act, _ := f["action"].(string)
		if st.AddFilter(&model.Filter{GroupID: id, Pattern: p, Regex: rx, Action: filterAct(act)}) == nil {
			added++
			have[p] = true
		}
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "settings": len(fields), "filters": added})
}

func filterAct(a string) string {
	switch a {
	case "delete", "warn", "mute", "ban":
		return a
	}
	return "warn"
}
