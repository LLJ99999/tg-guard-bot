// Package admin 内网管理接口，供 AstrBot 面板插件调用。只应监听在 Docker 内网，且必须带 Bearer token。
package admin

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"oai_bot/internal/config"
	"oai_bot/internal/guard"
	"oai_bot/internal/model"
)

type api struct {
	cfg *config.Config
	g   *guard.Guard
}

// New 返回管理接口 http.Server；未配置 listen 或 token 时返回 nil
func New(cfg *config.Config, g *guard.Guard) *http.Server {
	if cfg.Admin.Listen == "" || len(cfg.Admin.Token) < 16 {
		return nil
	}
	a := &api{cfg: cfg, g: g}
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery(), a.auth)
	v := r.Group("/api")
	v.GET("/status", a.status)
	v.GET("/groups", a.groups)
	v.GET("/groups/:id", a.group)
	v.PATCH("/groups/:id", a.patchGroup)
	v.POST("/groups/:id/send", a.send)
	v.POST("/groups/:id/unban", a.unban)
	v.GET("/groups/:id/warns", a.warns)
	v.GET("/groups/:id/stats", a.stats)

	v.GET("/groups/:id/filters", a.listFilters)
	v.POST("/groups/:id/filters", a.addFilter)
	v.DELETE("/groups/:id/filters/:fid", a.delFilter)
	v.GET("/groups/:id/notes", a.listNotes)
	v.POST("/groups/:id/notes", a.addNote)
	v.DELETE("/groups/:id/notes", a.delNote)
	v.GET("/groups/:id/replies", a.listReplies)
	v.POST("/groups/:id/replies", a.addReply)
	v.DELETE("/groups/:id/replies", a.delReply)
	v.GET("/groups/:id/schedules", a.listSchedules)
	v.POST("/groups/:id/schedules", a.addSchedule)
	v.DELETE("/groups/:id/schedules/:sid", a.delSchedule)
	v.GET("/groups/:id/approved", a.listApproved)
	v.POST("/groups/:id/approved", a.addApproved)
	v.DELETE("/groups/:id/approved/:uid", a.delApproved)

	v.GET("/groups/:id/bioscan", a.bioReport)
	v.POST("/groups/:id/bioscan", a.bioScanNow)
	v.GET("/groups/:id/ads", a.listAds)
	v.POST("/groups/:id/ads", a.saveAd)
	v.PATCH("/groups/:id/ads/:aid", a.saveAd)
	v.DELETE("/groups/:id/ads/:aid", a.delAd)
	v.POST("/groups/:id/ads/:aid/send", a.sendAd)
	v.POST("/groups/:id/ad-preview", a.previewAd)
	v.GET("/groups/:id/punishments", a.punishments)
	v.POST("/groups/:id/punishments/:pid/undo", a.undoPunishment)
	v.GET("/groups/:id/members", a.members)
	v.POST("/groups/:id/members/:uid/action", a.memberAction)
	v.GET("/samples", a.samples)
	v.POST("/samples", a.addSample)
	v.DELETE("/samples/:sid", a.delSample)
	v.GET("/ai", a.getAI)
	v.PUT("/ai", a.putAI)
	v.POST("/ai", a.putAI) // AstrBot 插件代理不放行 PUT
	v.GET("/ai/models", a.aiModels)

	v.GET("/gbans", a.listGBans)
	v.POST("/gbans", a.addGBan)
	v.DELETE("/gbans/:uid", a.delGBan)
	v.GET("/logs", a.logs)
	v.POST("/ai/test", a.aiTest)
	a.routesV14(v)
	return &http.Server{Addr: cfg.Admin.Listen, Handler: r, ReadHeaderTimeout: 10 * time.Second}
}

func (a *api) auth(c *gin.Context) {
	got := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
	if subtle.ConstantTimeCompare([]byte(got), []byte(a.cfg.Admin.Token)) != 1 {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	// Gin has matched the route before middleware runs. Guard every group-scoped API.
	if raw := c.Param("id"); raw != "" && strings.Contains(c.FullPath(), "/groups/:id") {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || !a.cfg.GroupAllowed(id) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "group not allowed"})
			return
		}
	}
}

func fail(c *gin.Context, code int, msg string) { c.JSON(code, gin.H{"error": msg}) }

func gid(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		fail(c, http.StatusBadRequest, "群 ID 无效")
		return 0, false
	}
	return id, true
}

func (a *api) status(c *gin.Context) {
	info := a.g.Info()
	gs, _ := a.g.Store().Groups()
	info["groups"] = len(gs)
	c.JSON(http.StatusOK, info)
}

func (a *api) groups(c *gin.Context) {
	gs, err := a.g.Store().Groups()
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, gs)
}

func (a *api) group(c *gin.Context) {
	id, ok := gid(c)
	if !ok {
		return
	}
	grp, err := a.g.Store().Group(id, "")
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, grp)
}

// 面板可改的字段及校验
type fieldRule struct {
	kind     string // bool | int | string | enum | hm
	min, max int
	enum     []string
}

var hmRe = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

var editable = map[string]fieldRule{
	"verify_mode":    {kind: "enum", enum: []string{model.VerifyOff, model.VerifyJoinRequest, model.VerifyRestrict}},
	"verify_timeout": {kind: "int", min: 30, max: 86400},
	"captcha_type":   {kind: "enum", enum: model.CaptchaTypes},
	"ai_all":         {kind: "bool"},
	"ai_action":      {kind: "enum", enum: []string{model.ActionBan, model.ActionMute, "warn"}},
	"welcome":        {kind: "string", max: 4000},
	"rules":          {kind: "string", max: 4000},
	"flood_count":    {kind: "int", min: 0, max: 100},
	"flood_second":   {kind: "int", min: 1, max: 600},
	"warn_limit":     {kind: "int", min: 1, max: 20},
	"warn_action":    {kind: "enum", enum: []string{model.ActionMute, model.ActionKick, model.ActionBan}},
	"newbie_msgs":    {kind: "int", min: 0, max: 100},
	"cas":            {kind: "bool"},
	"ai":             {kind: "bool"},
	"dup_check":      {kind: "bool"},
	"raid_limit":     {kind: "int", min: 0, max: 500},
	"night_on":       {kind: "bool"},
	"bio_scan":       {kind: "bool"},
	"bio_scan_hours": {kind: "int", min: 1, max: 168},
	"bio_scan_min":   {kind: "int", min: 2, max: 100},
	"night_start":    {kind: "hm"},
	"night_end":      {kind: "hm"},
	"lock_link":      {kind: "bool"},
	"lock_forward":   {kind: "bool"},
	"lock_button":    {kind: "bool"},
	"lock_via_bot":   {kind: "bool"},
	"lock_story":     {kind: "bool"},
	"lock_sticker":   {kind: "bool"},
	"lock_gif":       {kind: "bool"},
	"lock_media":     {kind: "bool"},
	"lock_voice":     {kind: "bool"},
	"lock_invite":    {kind: "bool"},
	"lock_contact":   {kind: "bool"},
	"lock_poll":      {kind: "bool"},
	"lock_game":      {kind: "bool"},
	"lock_foreign":   {kind: "bool"},
	"warn_expire":    {kind: "int", min: 0, max: 8760},
	"profile_check":  {kind: "bool"},
	"welcome_ttl":    {kind: "int", min: 0, max: 86400},
	"clean_welcome":  {kind: "bool"},
	"bot_admins":     {kind: "string", max: 2000},
}

// validate 校验面板提交的字段，返回可直接写库的 map
func validate(in map[string]any) (map[string]any, error) {
	out := map[string]any{}
	for k, v := range in {
		r, ok := editable[k]
		if !ok {
			return nil, fmt.Errorf("不允许修改字段 %s", k)
		}
		switch r.kind {
		case "bool":
			b, ok := v.(bool)
			if !ok {
				return nil, fmt.Errorf("%s 需要布尔值", k)
			}
			out[k] = b
		case "int":
			f, ok := v.(float64)
			if !ok || f != float64(int(f)) || int(f) < r.min || int(f) > r.max {
				return nil, fmt.Errorf("%s 需要 %d~%d 的整数", k, r.min, r.max)
			}
			out[k] = int(f)
		case "string":
			s, ok := v.(string)
			if !ok || len([]rune(s)) > r.max {
				return nil, fmt.Errorf("%s 需要不超过 %d 字的文字", k, r.max)
			}
			out[k] = s
		case "enum":
			s, _ := v.(string)
			valid := false
			for _, e := range r.enum {
				valid = valid || s == e
			}
			if !valid {
				return nil, fmt.Errorf("%s 只能是 %s", k, strings.Join(r.enum, " / "))
			}
			out[k] = s
		case "hm":
			s, _ := v.(string)
			if !hmRe.MatchString(s) {
				return nil, fmt.Errorf("%s 需要 HH:MM 格式", k)
			}
			out[k] = s
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("没有要修改的字段")
	}
	return out, nil
}

func (a *api) patchGroup(c *gin.Context) {
	id, ok := gid(c)
	if !ok {
		return
	}
	var in map[string]any
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, "JSON 无效")
		return
	}
	fields, err := validate(in)
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	if t, _ := fields["captcha_type"].(string); t == model.CaptchaWeb && !a.g.Info()["captcha"].(bool) {
		fail(c, http.StatusBadRequest, "网页验证需要配置 captcha 和 web.public_url；图片验证码、算术题、点表情、点按钮不需要")
		return
	}
	if on, _ := fields["ai"].(bool); on && !a.g.Info()["ai"].(bool) {
		fail(c, http.StatusBadRequest, "未配置 ai，无法开启 AI 审核")
		return
	}
	if v, ok := fields["bot_admins"].(string); ok {
		norm, err := a.g.NormalizeWhitelist(v)
		if err != nil {
			fail(c, http.StatusBadRequest, err.Error())
			return
		}
		fields["bot_admins"] = norm
		defer a.g.InvalidateWhitelist(id)
	}
	if err := a.g.Store().UpdateGroup(id, fields); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	a.group(c)
}

func (a *api) send(c *gin.Context) {
	id, ok := gid(c)
	if !ok {
		return
	}
	var in struct {
		Text string `json:"text"`
	}
	if c.ShouldBindJSON(&in) != nil || strings.TrimSpace(in.Text) == "" || len([]rune(in.Text)) > 4000 {
		fail(c, http.StatusBadRequest, "内容为空或超过 4000 字")
		return
	}
	if err := a.g.PanelSend(c.Request.Context(), id, in.Text); err != nil {
		fail(c, http.StatusBadGateway, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

type userIn struct {
	UserID int64  `json:"user_id"`
	Reason string `json:"reason"`
}

func (a *api) unban(c *gin.Context) {
	id, ok := gid(c)
	if !ok {
		return
	}
	var in userIn
	if c.ShouldBindJSON(&in) != nil || in.UserID == 0 {
		fail(c, http.StatusBadRequest, "需要 user_id")
		return
	}
	if err := a.g.PanelUnban(c.Request.Context(), id, in.UserID); err != nil {
		fail(c, http.StatusBadGateway, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (a *api) warns(c *gin.Context) {
	id, ok := gid(c)
	if !ok {
		return
	}
	ms, err := a.g.Store().Warned(id)
	respond(c, ms, err)
}

func (a *api) stats(c *gin.Context) {
	id, ok := gid(c)
	if !ok {
		return
	}
	n, _ := strconv.Atoi(c.DefaultQuery("days", "7"))
	if n < 1 || n > 90 {
		n = 7
	}
	loc := a.cfg.Location()
	var days []string
	for i := n - 1; i >= 0; i-- {
		days = append(days, time.Now().In(loc).AddDate(0, 0, -i).Format("2006-01-02"))
	}
	ss, err := a.g.Store().Stats(id, days)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	by := map[string]model.Stat{}
	for _, s := range ss {
		by[s.Day] = s
	}
	out := make([]model.Stat, 0, n)
	for _, d := range days {
		s := by[d]
		s.GroupID, s.Day = id, d
		out = append(out, s)
	}
	c.JSON(http.StatusOK, out)
}

func respond(c *gin.Context, v any, err error) {
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, v)
}

func deleted(c *gin.Context, ok bool, err error) {
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		fail(c, http.StatusNotFound, "不存在")
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ---------- 屏蔽词 ----------

func (a *api) listFilters(c *gin.Context) {
	id, ok := gid(c)
	if !ok {
		return
	}
	fs, err := a.g.Store().Filters(id)
	respond(c, fs, err)
}

func (a *api) addFilter(c *gin.Context) {
	id, ok := gid(c)
	if !ok {
		return
	}
	var in struct {
		Pattern string `json:"pattern"`
		Regex   bool   `json:"regex"`
		Action  string `json:"action"`
	}
	if c.ShouldBindJSON(&in) != nil || strings.TrimSpace(in.Pattern) == "" || len(in.Pattern) > 500 {
		fail(c, http.StatusBadRequest, "屏蔽词为空或过长")
		return
	}
	if in.Regex {
		if _, err := regexp.Compile(in.Pattern); err != nil {
			fail(c, http.StatusBadRequest, "正则无效："+err.Error())
			return
		}
	} else {
		in.Pattern = strings.ToLower(strings.TrimSpace(in.Pattern))
	}
	switch in.Action {
	case "delete", "warn", "mute", "ban":
	default:
		in.Action = "warn"
	}
	f := &model.Filter{GroupID: id, Pattern: in.Pattern, Regex: in.Regex, Action: in.Action}
	respond(c, f, a.g.Store().AddFilter(f))
}

func (a *api) delFilter(c *gin.Context) {
	id, ok := gid(c)
	if !ok {
		return
	}
	fid, _ := strconv.Atoi(c.Param("fid"))
	ok2, err := a.g.Store().DelFilterByID(id, uint(fid))
	deleted(c, ok2, err)
}

// ---------- 笔记 / 自动回复 / 定时（面板只能新建纯文字，回复图片的方式仍用群命令） ----------

type textIn struct {
	Name    string `json:"name"`
	Keyword string `json:"keyword"`
	At      string `json:"at"`
	Text    string `json:"text"`
}

func bindText(c *gin.Context) (textIn, bool) {
	var in textIn
	if c.ShouldBindJSON(&in) != nil || strings.TrimSpace(in.Text) == "" || len([]rune(in.Text)) > 4000 {
		fail(c, http.StatusBadRequest, "内容为空或超过 4000 字")
		return in, false
	}
	return in, true
}

func (a *api) listNotes(c *gin.Context) {
	id, ok := gid(c)
	if !ok {
		return
	}
	ns, err := a.g.Store().Notes(id)
	respond(c, ns, err)
}

func (a *api) addNote(c *gin.Context) {
	id, ok := gid(c)
	if !ok {
		return
	}
	in, ok := bindText(c)
	if !ok {
		return
	}
	name := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(in.Name), "#"))
	if name == "" || strings.ContainsAny(name, " \n") || len(name) > 64 {
		fail(c, http.StatusBadRequest, "笔记名不能为空或含空格")
		return
	}
	n := &model.Note{GroupID: id, Name: name, Content: model.Content{Text: in.Text}}
	respond(c, n, a.g.Store().SaveNote(n))
}

func (a *api) delNote(c *gin.Context) {
	id, ok := gid(c)
	if !ok {
		return
	}
	ok2, err := a.g.Store().DelNote(id, c.Query("name"))
	deleted(c, ok2, err)
}

func (a *api) listReplies(c *gin.Context) {
	id, ok := gid(c)
	if !ok {
		return
	}
	rs, err := a.g.Store().Replies(id)
	respond(c, rs, err)
}

func (a *api) addReply(c *gin.Context) {
	id, ok := gid(c)
	if !ok {
		return
	}
	in, ok := bindText(c)
	if !ok {
		return
	}
	kw := strings.ToLower(strings.TrimSpace(in.Keyword))
	if kw == "" || len(kw) > 200 {
		fail(c, http.StatusBadRequest, "关键词不能为空")
		return
	}
	r := &model.AutoReply{GroupID: id, Keyword: kw, Content: model.Content{Text: in.Text}}
	respond(c, r, a.g.Store().SaveReply(r))
}

func (a *api) delReply(c *gin.Context) {
	id, ok := gid(c)
	if !ok {
		return
	}
	ok2, err := a.g.Store().DelReply(id, c.Query("keyword"))
	deleted(c, ok2, err)
}

func (a *api) listSchedules(c *gin.Context) {
	id, ok := gid(c)
	if !ok {
		return
	}
	ss, err := a.g.Store().Schedules(id)
	respond(c, ss, err)
}

func (a *api) addSchedule(c *gin.Context) {
	id, ok := gid(c)
	if !ok {
		return
	}
	in, ok := bindText(c)
	if !ok {
		return
	}
	if !hmRe.MatchString(in.At) {
		fail(c, http.StatusBadRequest, "时间需要 HH:MM 格式")
		return
	}
	sc := &model.Schedule{GroupID: id, At: in.At, Content: model.Content{Text: in.Text}}
	respond(c, sc, a.g.Store().AddSchedule(sc))
}

func (a *api) delSchedule(c *gin.Context) {
	id, ok := gid(c)
	if !ok {
		return
	}
	sid, _ := strconv.Atoi(c.Param("sid"))
	ok2, err := a.g.Store().DelSchedule(id, uint(sid))
	deleted(c, ok2, err)
}

// ---------- 白名单 ----------

type approvedOut struct {
	UserID int64  `json:"user_id"`
	Name   string `json:"name"`
}

func (a *api) listApproved(c *gin.Context) {
	id, ok := gid(c)
	if !ok {
		return
	}
	as, err := a.g.Store().ApprovedList(id)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]approvedOut, 0, len(as))
	for _, x := range as {
		o := approvedOut{UserID: x.UserID}
		if u, _ := a.g.Store().UserByID(x.UserID); u != nil {
			o.Name = strings.TrimSpace(u.FirstName + " " + u.LastName)
			if u.Username != "" {
				o.Name += " @" + u.Username
			}
		}
		out = append(out, o)
	}
	c.JSON(http.StatusOK, out)
}

func (a *api) addApproved(c *gin.Context) {
	id, ok := gid(c)
	if !ok {
		return
	}
	var in userIn
	if c.ShouldBindJSON(&in) != nil || in.UserID == 0 {
		fail(c, http.StatusBadRequest, "需要 user_id")
		return
	}
	respond(c, gin.H{"ok": true}, a.g.Store().SetApproved(id, in.UserID, true))
}

func (a *api) delApproved(c *gin.Context) {
	id, ok := gid(c)
	if !ok {
		return
	}
	uid, _ := strconv.ParseInt(c.Param("uid"), 10, 64)
	respond(c, gin.H{"ok": true}, a.g.Store().SetApproved(id, uid, false))
}

// ---------- 全局封禁 / 日志 ----------

func (a *api) listGBans(c *gin.Context) {
	gs, err := a.g.Store().GBans()
	respond(c, gs, err)
}

func (a *api) addGBan(c *gin.Context) {
	var in userIn
	if c.ShouldBindJSON(&in) != nil || in.UserID == 0 {
		fail(c, http.StatusBadRequest, "需要 user_id")
		return
	}
	respond(c, gin.H{"ok": true}, a.g.PanelGBan(c.Request.Context(), in.UserID, in.Reason, true))
}

func (a *api) delGBan(c *gin.Context) {
	uid, err := strconv.ParseInt(c.Param("uid"), 10, 64)
	if err != nil {
		fail(c, http.StatusBadRequest, "user_id 无效")
		return
	}
	respond(c, gin.H{"ok": true}, a.g.PanelGBan(c.Request.Context(), uid, "", false))
}

func (a *api) bioReport(c *gin.Context) {
	id, ok := gid(c)
	if !ok {
		return
	}
	r, running := a.g.LatestBioReport(id)
	c.JSON(http.StatusOK, gin.H{"running": running, "report": r})
}

func (a *api) bioScanNow(c *gin.Context) {
	id, ok := gid(c)
	if !ok {
		return
	}
	var in struct {
		BanCluster *struct {
			ReportID uint `json:"report_id"`
			Index    int  `json:"index"`
		} `json:"ban_cluster"`
	}
	c.ShouldBindJSON(&in)
	if in.BanCluster != nil {
		n, err := a.g.BanBioCluster(c.Request.Context(), id, in.BanCluster.ReportID, in.BanCluster.Index, 0)
		if err != nil {
			fail(c, http.StatusBadRequest, err.Error())
			return
		}
		c.JSON(http.StatusOK, gin.H{"banned": n})
		return
	}
	if !a.g.StartBioScan(id, 0) {
		fail(c, http.StatusConflict, "已有查重在进行中")
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"started": true})
}

// ---------- 定时广告 ----------

func (a *api) listAds(c *gin.Context) {
	id, ok := gid(c)
	if !ok {
		return
	}
	as, err := a.g.Store().Ads(id)
	respond(c, as, err)
}

type adIn struct {
	Name       *string `json:"name"`
	Text       *string `json:"text"`
	Photo      *string `json:"photo"`      // 链接 / file_id；空字符串表示去掉图片
	PhotoData  string  `json:"photo_data"` // data:image/...;base64,...（上传）
	Buttons    *string `json:"buttons"`
	Times      *string `json:"times"`
	Interval   *int    `json:"interval"`
	Enabled    *bool   `json:"enabled"`
	DeletePrev *bool   `json:"delete_prev"`
	Pin        *bool   `json:"pin"`
}

// saveAd POST 新建 / PATCH 修改
func (a *api) saveAd(c *gin.Context) {
	id, ok := gid(c)
	if !ok {
		return
	}
	var in adIn
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	var base uint
	if c.Request.Method == http.MethodPatch {
		aid, _ := strconv.Atoi(c.Param("aid"))
		base = uint(aid)
	}
	ad, ok := a.buildAd(c, id, base, in)
	if !ok {
		return
	}
	respond(c, ad, a.g.Store().SaveAd(ad))
}

// previewAd 把广告（可以是没保存的草稿）私聊发给机器人主人和管理员白名单
func (a *api) previewAd(c *gin.Context) {
	id, ok := gid(c)
	if !ok {
		return
	}
	var in struct {
		adIn
		ID uint `json:"id"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	ad, ok := a.buildAd(c, id, in.ID, in.adIn)
	if !ok {
		return
	}
	n, fails := a.g.PreviewAd(c.Request.Context(), ad, nil)
	if n == 0 {
		msg := "没有发出去"
		if len(fails) > 0 {
			msg += "：" + strings.Join(fails, "；")
		}
		fail(c, http.StatusBadRequest, msg)
		return
	}
	c.JSON(http.StatusOK, gin.H{"sent": n, "failed": fails})
}

// buildAd 按提交内容组装广告（base 不为 0 时在已有广告上修改）
func (a *api) buildAd(c *gin.Context, id int64, base uint, in adIn) (*model.Ad, bool) {
	ad := &model.Ad{GroupID: id, Enabled: true, DeletePrev: true}
	if base != 0 {
		aid := base
		old, _ := a.g.Store().Ad(id, aid)
		if old == nil {
			fail(c, http.StatusNotFound, "广告不存在")
			return nil, false
		}
		ad = old
	}
	set := func(dst *string, v *string) {
		if v != nil {
			*dst = strings.TrimSpace(*v)
		}
	}
	set(&ad.Name, in.Name)
	set(&ad.Buttons, in.Buttons)
	set(&ad.Times, in.Times)
	if in.Text != nil {
		ad.Text = *in.Text
		ad.FromChat, ad.MsgID = 0, 0 // 面板编辑过文字后不再复制原消息
	}
	if in.Photo != nil {
		ad.Photo, ad.PhotoID = strings.TrimSpace(*in.Photo), ""
	}
	if in.PhotoData != "" {
		p, err := a.g.SaveAdPhoto(in.PhotoData)
		if err != nil {
			fail(c, http.StatusBadRequest, err.Error())
			return nil, false
		}
		ad.Photo, ad.PhotoID, ad.FromChat, ad.MsgID = p, "", 0, 0
	}
	if in.Interval != nil {
		ad.Interval = *in.Interval
	}
	if in.Enabled != nil {
		ad.Enabled = *in.Enabled
	}
	if in.DeletePrev != nil {
		ad.DeletePrev = *in.DeletePrev
	}
	if in.Pin != nil {
		ad.Pin = *in.Pin
	}
	times, err := guard.ValidateAdSchedule(ad.Times, ad.Interval)
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return nil, false
	}
	ad.Times = times
	if _, err := guard.ParseButtons(ad.Buttons); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return nil, false
	}
	if ad.MsgID == 0 && strings.TrimSpace(ad.Text) == "" && ad.Photo == "" {
		fail(c, http.StatusBadRequest, "文字和图片至少要有一个")
		return nil, false
	}
	if ad.Name == "" {
		ad.Name = "广告"
	}
	return ad, true
}

func (a *api) delAd(c *gin.Context) {
	id, ok := gid(c)
	if !ok {
		return
	}
	aid, _ := strconv.Atoi(c.Param("aid"))
	ok2, err := a.g.Store().DelAd(id, uint(aid))
	deleted(c, ok2, err)
}

func (a *api) sendAd(c *gin.Context) {
	id, ok := gid(c)
	if !ok {
		return
	}
	aid, _ := strconv.Atoi(c.Param("aid"))
	if err := a.g.PanelSendAd(c.Request.Context(), id, uint(aid)); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"sent": true})
}

func (a *api) logs(c *gin.Context) {
	n, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	if n < 1 || n > 1000 {
		n = 100
	}
	ls, err := a.g.Store().Logs(n, c.Query("q"))
	respond(c, ls, err)
}

func (a *api) getAI(c *gin.Context) { c.JSON(http.StatusOK, a.g.AIConfig()) }

func (a *api) putAI(c *gin.Context) {
	var in struct {
		BaseURL   string  `json:"base_url"`
		APIKey    *string `json:"api_key"` // 不传表示不改
		Model     string  `json:"model"`
		Threshold float64 `json:"threshold"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	if in.Threshold == 0 {
		in.Threshold = 0.8
	}
	if err := a.g.SetAIConfig(in.BaseURL, in.APIKey, in.Model, in.Threshold); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	c.JSON(http.StatusOK, a.g.AIConfig())
}

func (a *api) aiModels(c *gin.Context) {
	ms, err := a.g.AIModels(c.Request.Context())
	if err != nil {
		fail(c, http.StatusBadGateway, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"models": ms})
}

func (a *api) aiTest(c *gin.Context) {
	var in struct {
		Text string `json:"text"`
	}
	if err := c.ShouldBindJSON(&in); err != nil || strings.TrimSpace(in.Text) == "" {
		fail(c, http.StatusBadRequest, "请输入要测试的文字")
		return
	}
	res, err := a.g.AITest(c.Request.Context(), truncateRunes(in.Text, 1500))
	if err != nil {
		fail(c, http.StatusBadGateway, "AI 调用失败："+err.Error())
		return
	}
	c.JSON(http.StatusOK, res)
}

func truncateRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
