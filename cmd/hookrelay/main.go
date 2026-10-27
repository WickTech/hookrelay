package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/WickTech/hookrelay/internal/api"
	"github.com/WickTech/hookrelay/internal/config"
	"github.com/WickTech/hookrelay/internal/relay"
	"github.com/WickTech/hookrelay/internal/store"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfgPath := "targets.yaml"
	if len(os.Args) > 1 {
		cfgPath = os.Args[1]
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Error("load config", "err", err)
		os.Exit(1)
	}

	s, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Error("open store", "err", err)
		os.Exit(1)
	}
	defer s.Close()

	targets := cfg.TargetMap()
	notify := make(chan struct{}, 1)

	mux := http.NewServeMux()
	h := api.New(targets, s, notify, log)
	h.Register(mux)

	worker := relay.NewWorker(targets, s, notify, log)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	go worker.Run(ctx)

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutCancel()
		_ = srv.Shutdown(shutCtx)
	}()

	log.Info("hookrelay starting", "addr", srv.Addr, "targets", len(targets))
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Error("server error", "err", err)
		os.Exit(1)
	}
	log.Info("hookrelay stopped")
}
