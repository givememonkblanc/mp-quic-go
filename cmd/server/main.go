package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"mp-quic-go/internal/logger"
	"mp-quic-go/internal/server"
)

func main() {
	log := logger.New()

	cfg := server.DefaultConfig()
	cfg.ListenAddr = ":4433"

	s, err := server.New(*cfg, log)
	if err != nil {
		log.Error("failed to create server", "error", err)
		os.Exit(1)
	}

	log.Info("Starting QUIC server...")

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := s.Start(ctx); err != nil {
		log.Error("server failed", "error", err)
	}
}
