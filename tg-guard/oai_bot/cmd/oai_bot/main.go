// oai_bot：Telegram 群管机器人（入群验证、反垃圾、管理命令），可与 AstrBot 等 AI 机器人同群共存。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"oai_bot/internal/admin"
	"oai_bot/internal/config"
	"oai_bot/internal/guard"
	"oai_bot/internal/server"
	"oai_bot/internal/store"
	"oai_bot/internal/telegram"
)

var version = "dev"

func main() {
	path := flag.String("c", "config.yaml", "配置文件路径")
	showVersion := flag.Bool("version", false, "显示版本号")
	flag.Parse()
	if *showVersion {
		fmt.Println("oai_bot", version)
		return
	}
	if err := run(*path); err != nil {
		slog.Error("exit", "err", err)
		os.Exit(1)
	}
}

func run(path string) error {
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	st, err := store.Open(cfg.DB.Path)
	if err != nil {
		return err
	}
	tg, err := telegram.New(cfg.Bot.Token)
	if err != nil {
		return err
	}
	g := guard.New(cfg, tg, st)

	var webhook http.Handler
	if cfg.Bot.Mode == config.ModeWebhook {
		webhook = g.WebhookHandler()
	}
	srv := server.New(cfg, g, webhook)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errc := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
			stop()
		}
	}()
	adm := admin.New(cfg, g)
	if adm != nil {
		slog.Info("管理接口已开启", "listen", cfg.Admin.Listen)
		go func() {
			if err := adm.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errc <- err
				stop()
			}
		}()
	} else if cfg.Admin.Listen != "" {
		slog.Warn("admin.token 少于 16 位，管理接口未开启")
	}
	slog.Info("started", "version", version, "bot", "@"+tg.Me().Username, "mode", cfg.Bot.Mode, "listen", cfg.Web.Listen)
	if err := g.Run(ctx); err != nil {
		return err
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(shutdown)
	if adm != nil {
		adm.Shutdown(shutdown)
	}
	select {
	case err := <-errc:
		return err
	default:
		return nil
	}
}
