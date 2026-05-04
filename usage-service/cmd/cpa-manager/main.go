package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/seakee/cpa-manager/usage-service/internal/codexinspect"
	"github.com/seakee/cpa-manager/usage-service/internal/collector"
	"github.com/seakee/cpa-manager/usage-service/internal/config"
	"github.com/seakee/cpa-manager/usage-service/internal/httpapi"
	"github.com/seakee/cpa-manager/usage-service/internal/store"
)

func main() {
	cfg := config.Load()
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	manager := collector.NewManager(cfg, db)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	resolveRuntime := func(ctx context.Context) (codexinspect.RuntimeConfig, bool, error) {
		if cfg.CPAUpstreamURL != "" && cfg.ManagementKey != "" {
			return codexinspect.RuntimeConfig{BaseURL: cfg.CPAUpstreamURL, ManagementKey: cfg.ManagementKey}, true, nil
		}
		setup, ok, err := db.LoadSetup(ctx)
		if err != nil || !ok {
			return codexinspect.RuntimeConfig{}, ok, err
		}
		return codexinspect.RuntimeConfig{BaseURL: setup.CPAUpstreamURL, ManagementKey: setup.ManagementKey}, true, nil
	}
	inspector := codexinspect.NewScheduler(db, resolveRuntime)
	inspector.Start(ctx)

	if cfg.CPAUpstreamURL != "" && cfg.ManagementKey != "" {
		manager.Start(ctx, collector.RuntimeConfig{
			CPAUpstreamURL: cfg.CPAUpstreamURL,
			ManagementKey:  cfg.ManagementKey,
			Queue:          cfg.Queue,
			PopSide:        cfg.PopSide,
		})
	} else if setup, ok, err := db.LoadSetup(ctx); err == nil && ok {
		manager.Start(ctx, collector.RuntimeConfig{
			CPAUpstreamURL: setup.CPAUpstreamURL,
			ManagementKey:  setup.ManagementKey,
			Queue:          setup.Queue,
			PopSide:        setup.PopSide,
		})
	} else if err != nil {
		log.Printf("load setup: %v", err)
	}

	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           httpapi.New(cfg, db, manager, inspector).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("cpa-manager listening on %s", cfg.HTTPAddr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http server: %v", err)
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	manager.Stop()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}
