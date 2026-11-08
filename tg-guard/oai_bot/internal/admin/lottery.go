package admin

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"oai_bot/internal/guard"
	"oai_bot/internal/model"
)

func (a *api) routesLottery(v *gin.RouterGroup) {
	v.GET("/groups/:id/lotteries", a.listLotteries)
	v.POST("/groups/:id/lotteries", a.createLottery)
	v.GET("/groups/:id/lotteries/:lid", a.getLottery)
	v.POST("/groups/:id/lotteries/:lid/draw", a.drawLottery)
	v.POST("/groups/:id/lotteries/:lid/cancel", a.cancelLottery)
	v.POST("/groups/:id/lotteries/:lid/resend", a.resendLottery)
}

// lotteryOf 取本群的抽奖，不属于本群返回 nil
func (a *api) lotteryOf(c *gin.Context) *model.Lottery {
	id, ok := gid(c)
	if !ok {
		return nil
	}
	lid, _ := strconv.ParseUint(c.Param("lid"), 10, 32)
	l, _ := a.g.Store().Lottery(uint(lid))
	if l == nil || l.GroupID != id {
		fail(c, http.StatusNotFound, "抽奖不存在")
		return nil
	}
	return l
}

type lotteryRow struct {
	model.Lottery
	Codes int64 `json:"codes"`
}

func (a *api) listLotteries(c *gin.Context) {
	id, ok := gid(c)
	if !ok {
		return
	}
	ls, err := a.g.Store().Lotteries(id)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]lotteryRow, 0, len(ls))
	for _, l := range ls {
		out = append(out, lotteryRow{Lottery: l, Codes: a.g.Store().CountLotteryCodes(l.ID)})
	}
	c.JSON(http.StatusOK, out)
}

func (a *api) getLottery(c *gin.Context) {
	l := a.lotteryOf(c)
	if l == nil {
		return
	}
	es, _ := a.g.Store().LotteryEntries(l.ID)
	codes, _ := a.g.Store().LotteryCodes(l.ID)
	if es == nil {
		es = []model.LotteryEntry{}
	}
	// 未发出的卡密：没分配出去的 + 分配了但没送达的（单条共用卡密有人中奖就不算剩余）
	unused, undelivered := []string{}, []string{}
	won := false
	for _, e := range es {
		if e.Won {
			won = true
			if !e.Delivered && e.Code != "" {
				undelivered = append(undelivered, e.Code)
			}
		}
	}
	for _, cd := range codes {
		if cd.UserID == 0 && !(len(codes) == 1 && won) {
			unused = append(unused, cd.Code)
		}
	}
	c.JSON(http.StatusOK, gin.H{"lottery": l, "entries": es, "codes": len(codes), "unused": unused, "undelivered": undelivered})
}

func (a *api) createLottery(c *gin.Context) {
	id, ok := gid(c)
	if !ok {
		return
	}
	var in struct {
		Title     string `json:"title"`
		Desc      string `json:"desc"`
		PrizeType string `json:"prize_type"`
		Codes     string `json:"codes"`
		FileName  string `json:"file_name"`
		FileData  string `json:"file_data"`
		Winners   int    `json:"winners"`
		DrawAt    string `json:"draw_at"` // 本地时间 2006-01-02T15:04，或 RFC3339
		DrawCount int    `json:"draw_count"`
		// 参与条件
		RequireChat string `json:"require_chat"`
		MinMsgs     int    `json:"min_msgs"`
		OldOnly     bool   `json:"old_only"`
		MinPoints   int    `json:"min_points"`
		CostPoints  int    `json:"cost_points"`
		MinInvites  int    `json:"min_invites"`
		// v1.7
		ThreadID    int                 `json:"thread_id"`
		Repeat      string              `json:"repeat"`
		PinMsg      bool                `json:"pin_msg"`
		JoinInGroup bool                `json:"join_in_group"`
		Tiers       []guard.LotteryTier `json:"tiers"` // 多奖项：每个奖项自己的卡密写在 codes_text
		TierCodes   []string            `json:"tier_codes"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" || len([]rune(in.Title)) > 100 {
		fail(c, http.StatusBadRequest, "标题不能为空，最多 100 字")
		return
	}
	if len([]rune(in.Desc)) > 2000 {
		fail(c, http.StatusBadRequest, "说明最多 2000 字")
		return
	}
	if in.Winners < 1 || in.Winners > 1000 {
		fail(c, http.StatusBadRequest, "中奖人数 1 ~ 1000")
		return
	}
	if in.DrawCount < 0 || in.DrawCount > 100000 {
		fail(c, http.StatusBadRequest, "满人开奖人数无效")
		return
	}
	if in.MinMsgs < 0 || in.MinMsgs > 10000 {
		fail(c, http.StatusBadRequest, "最少发言条数 0 ~ 10000")
		return
	}
	if rc := strings.TrimSpace(in.RequireChat); rc != "" && !requireChatRe.MatchString(rc) {
		fail(c, http.StatusBadRequest, "必须加入的频道请填 @用户名 或 -100 开头的数字 ID")
		return
	}
	l := &model.Lottery{GroupID: id, Title: in.Title, Desc: strings.TrimSpace(in.Desc), PrizeType: in.PrizeType, Winners: in.Winners, DrawCount: in.DrawCount, CreatedBy: "面板",
		RequireChat: strings.TrimSpace(in.RequireChat), MinMsgs: in.MinMsgs, OldOnly: in.OldOnly, CreatedAt: time.Now(),
		MinPoints: max(in.MinPoints, 0), CostPoints: max(in.CostPoints, 0), MinInvites: max(in.MinInvites, 0),
		ThreadID: max(in.ThreadID, 0), PinMsg: in.PinMsg, JoinInGroup: in.JoinInGroup}
	switch in.Repeat {
	case "daily", "weekly":
		l.Repeat = in.Repeat
	}
	if s := strings.TrimSpace(in.DrawAt); s != "" {
		t, err := time.ParseInLocation("2006-01-02T15:04", s, a.cfg.Location())
		if err != nil {
			t, err = time.Parse(time.RFC3339, s)
		}
		if err != nil {
			fail(c, http.StatusBadRequest, "开奖时间格式不对")
			return
		}
		if t.Before(time.Now().Add(time.Minute)) {
			fail(c, http.StatusBadRequest, "开奖时间要晚于现在至少 1 分钟")
			return
		}
		l.DrawAt = &t
	}
	if l.DrawAt == nil && l.DrawCount == 0 {
		fail(c, http.StatusBadRequest, "请设置开奖时间或满多少人开奖（至少一个）")
		return
	}
	if len(in.Tiers) > 20 {
		fail(c, http.StatusBadRequest, "奖项最多 20 个")
		return
	}
	for i := range in.Tiers {
		if i < len(in.TierCodes) {
			in.Tiers[i].Codes = guard.ParseCodes(in.TierCodes[i])
		}
	}
	if l.Repeat != "" && l.DrawAt == nil {
		fail(c, http.StatusBadRequest, "周期抽奖需要设置开奖时间")
		return
	}
	if err := a.g.CreateLottery(c.Request.Context(), l, guard.ParseCodes(in.Codes), in.FileName, in.FileData, in.Tiers...); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	c.JSON(http.StatusOK, l)
}

var requireChatRe = regexp.MustCompile(`^(@?[A-Za-z][A-Za-z0-9_]{3,31}|-100\d{5,})$`)

func (a *api) drawLottery(c *gin.Context) {
	l := a.lotteryOf(c)
	if l == nil {
		return
	}
	n, err := a.g.DrawLottery(c.Request.Context(), l.ID)
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"winners": n})
}

func (a *api) cancelLottery(c *gin.Context) {
	l := a.lotteryOf(c)
	if l == nil {
		return
	}
	if err := a.g.CancelLottery(c.Request.Context(), l.ID); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (a *api) resendLottery(c *gin.Context) {
	l := a.lotteryOf(c)
	if l == nil {
		return
	}
	ok, failed := a.g.ResendPrizes(c.Request.Context(), l.ID)
	c.JSON(http.StatusOK, gin.H{"sent": ok, "failed": failed})
}
