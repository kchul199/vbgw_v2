package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"vbgw-ai/internal/ai"
	"vbgw-ai/internal/config"
	pb "vbgw-ai/proto/voicebot"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

func main() {
	// 설정 로드
	config.LoadConfig()

	// 1. AI 엔진 인스턴스 생성 (OpenAI)
	engine := ai.NewOpenAIProvider(config.AppConfig)

	port := config.AppConfig.InternalPort
	listenAddr := fmt.Sprintf(":%s", port)
	lis, err := net.Listen("tcp", listenAddr)
	if err != nil {
		slog.Error("Failed to listen", "port", port, "err", err)
		os.Exit(1)
	}

	s := grpc.NewServer()

	// 2. 서버 생성 시 엔진 주입
	aiServer := ai.NewServer(engine)
	pb.RegisterVoicebotAiServiceServer(s, aiServer)

	// Enable reflection for debugging/grpctest
	reflection.Register(s)

	// A6: Graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigCh
		slog.Info("Received shutdown signal", "signal", sig)

		// GracefulStop allows in-flight RPCs to complete
		slog.Info("[Shutdown 1/2] Draining active gRPC streams (30s timeout)")
		done := make(chan struct{})
		go func() {
			s.GracefulStop()
			close(done)
		}()

		select {
		case <-done:
			slog.Info("[Shutdown 2/2] gRPC server stopped gracefully")
		case <-time.After(30 * time.Second):
			slog.Warn("[Shutdown 2/2] Graceful stop timed out, forcing stop")
			s.Stop()
		}

		cancel()
	}()

	slog.Info("VBGW AI Engine starting with OpenAI", "port", port)
	if err := s.Serve(lis); err != nil {
		// Serve returns after GracefulStop/Stop — check if context was cancelled
		select {
		case <-ctx.Done():
			slog.Info("AI Engine shutdown complete")
		default:
			slog.Error("Failed to serve gRPC", "err", err)
			os.Exit(1)
		}
	}
}
