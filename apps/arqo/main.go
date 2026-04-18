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
	"aurora/apps/arqo/internal/events"
	"aurora/apps/arqo/internal/scheduler"
)

func main() {
	addr := envOrDefault("ARQO_ADDR", ":8080")

	engine, schedulerBackend, err := scheduler.NewEngineFromEnv()
	if err != nil {
		log.Fatalf("failed to init scheduler backend: %v", err)
	}
	defer func() {
		if closeErr := engine.Close(); closeErr != nil {
			log.Printf("scheduler backend close error: %v", closeErr)
		}
	}()

	log.Printf("scheduler backend initialized: %s", schedulerBackend)

	broker, backend, err := events.NewBrokerFromEnv()
	if err != nil {
		log.Fatalf("failed to init event broker: %v", err)
	}
	defer func() {
		if closeErr := broker.Close(); closeErr != nil {
			log.Printf("event broker close error: %v", closeErr)
		}
	}()

	log.Printf("event backend initialized: %s", backend)
	server := api.NewServer(engine, broker)
	mux := http.NewServeMux()
	server.Register(mux)

	httpServer := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go runSweeper(ctx, engine)
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

func runSweeper(ctx context.Context, engine scheduler.Engine) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case t := <-ticker.C:
			expired := engine.ExpireRunningTasks(t.UTC())
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
