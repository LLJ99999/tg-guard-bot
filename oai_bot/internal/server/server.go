// Package server HTTP 接入层：Mini App 验证页、验证接口、webhook。
package server

import (
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"oai_bot/internal/config"
	"oai_bot/internal/guard"
	"oai_bot/internal/web"
	"oai_bot/pkg/tginit"
)

const (
	apiVerifyPath  = "/api/verify"
	initDataMaxAge = time.Hour
)

type Server struct {
	cfg *config.Config
	g   *guard.Guard
}

func New(cfg *config.Config, g *guard.Guard, webhook http.Handler) *http.Server {
	s := &Server{cfg: cfg, g: g}
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.GET(guard.VerifyPath, s.verifyPage)
	r.POST(apiVerifyPath, s.verifyAPI)
	r.GET("/healthz", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	if webhook != nil {
		r.POST(guard.WebhookPath, gin.WrapH(webhook))
	}
	return &http.Server{Addr: cfg.Web.Listen, Handler: r, ReadHeaderTimeout: 10 * time.Second}
}

func (s *Server) verifyPage(c *gin.Context) {
	data := struct {
		Widget  template.HTML
		APIPath string
	}{APIPath: strings.TrimPrefix(apiVerifyPath, "/")} // 相对路径，挂在反代子路径（如 /oai/）下也能用
	if p := s.g.Captcha(); p != nil {
		data.Widget = p.Widget()
	}
	c.Header("Content-Type", "text/html; charset=utf-8")
	if err := web.Templates.ExecuteTemplate(c.Writer, "verify.html", data); err != nil {
		slog.Error("render verify page", "err", err)
	}
}

func (s *Server) verifyAPI(c *gin.Context) {
	var req struct {
		InitData string `json:"init_data"`
		Token    string `json:"token"`
		Group    int64  `json:"group"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Token == "" || req.Group == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误，请从机器人私聊里的按钮重新打开"})
		return
	}
	u, err := tginit.Validate(req.InitData, s.cfg.Bot.Token, initDataMaxAge, time.Now())
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "请在 Telegram 中重新打开验证页面"})
		return
	}
	p := s.g.Captcha()
	if p == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "当前无需验证"})
		return
	}
	// 先确认有待验证记录，避免任何人拿验证码接口刷
	if !s.g.HasPending(req.Group, u.ID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": guard.ErrNoPending.Error()})
		return
	}
	ok, err := p.Verify(c.Request.Context(), req.Token, c.ClientIP())
	if err != nil {
		slog.Error("captcha siteverify", "provider", p.Name(), "err", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "验证服务暂时不可用，请稍后再试"})
		return
	}
	if !ok {
		if s.g.RecordFail(c.Request.Context(), req.Group, u.ID) {
			c.JSON(http.StatusForbidden, gin.H{"error": "失败次数过多，本次入群已作废，请稍后重新申请"})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": "验证失败，请重试"})
		return
	}
	switch err := s.g.OnVerified(c.Request.Context(), req.Group, u.ID); {
	case err == nil:
		c.JSON(http.StatusOK, gin.H{"ok": true})
	case errors.Is(err, guard.ErrNoPending):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	default:
		slog.Error("on verified", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "服务器错误"})
	}
}
