package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"aurora/apps/arqo/internal/api"
	"aurora/apps/arqo/internal/scheduler"
)

func main() {
	addr := envOrDefault("ARQO_ADDR", ":8080")

	store := scheduler.NewStore()
	server := api.NewServer(store)
	mux := http.NewServeMux()
	server.Register(mux)

	httpServer := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go runSweeper(ctx, store)
	go func() {
		log.Printf("arqo listening on %s", addr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("arqo failed: %v", err)
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown error: %v", err)
	}
}

func runSweeper(ctx context.Context, store *scheduler.Store) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case t := <-ticker.C:
			expired := store.ExpireRunningTasks(t.UTC())
			if len(expired) > 0 {
				log.Printf("sweeper expired tasks: %v", expired)
			}
		}
	}
}

func envOrDefault(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}
