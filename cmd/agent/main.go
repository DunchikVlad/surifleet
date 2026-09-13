// SuriFleet Agent — агент хоста-сенсора: единый статический бинарь,
// исходящее gRPC-подключение к серверу (mTLS), локальное логирование
// в файл с ротацией + дублирование в stdout.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"

	"github.com/surifleet/surifleet/internal/config"
)

func main() {
	configPath := flag.String("config", os.Getenv("SURIFLEET_CONFIG"), "путь к YAML-конфигурации")
	flag.Parse()

	cfg := config.DefaultAgent()
	if err := config.LoadSection(*configPath, "agent", cfg); err != nil {
		fatal(fmt.Errorf("загрузка конфигурации: %w", err))
	}

	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		fatal(fmt.Errorf("создание data_dir: %w", err))
	}

	logFile := cfg.LogFile
	if logFile == "" {
		logFile = filepath.Join(cfg.DataDir, "agent.log")
	}
	rotator := &lumberjack.Logger{
		Filename:   logFile,
		MaxSize:    cfg.LogMaxSizeMB,  // МБ
		MaxBackups: cfg.LogMaxBackups, // файлов
		MaxAge:     cfg.LogMaxAgeDays, // дней
		Compress:   true,
	}
	defer rotator.Close()

	level, err := config.ParseLogLevel(cfg.LogLevel)
	if err != nil {
		fatal(err)
	}
	// Локальный файл — источник истины при потере связи; stdout — для journald/отладки.
	log := slog.New(slog.NewJSONHandler(io.MultiWriter(os.Stdout, rotator), &slog.HandlerOptions{Level: level}))
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Info("запуск агента",
		"server_addr", cfg.ServerAddr,
		"data_dir", cfg.DataDir,
		"log_file", logFile,
		"backoff", fmt.Sprintf("%s..%s", cfg.BackoffMin.D(), cfg.BackoffMax.D()),
	)

	connectLoop(ctx, cfg, log)
	log.Info("агент остановлен")
}

// connectLoop — цикл переподключения к серверу: exponential backoff
// (backoff_min → ×2 → backoff_max) с полным jitter (защита от
// reconnect-штормов, см. docs/architecture.md §4.7).
func connectLoop(ctx context.Context, cfg *config.AgentConfig, log *slog.Logger) {
	backoff := cfg.BackoffMin.D()
	attempt := 0
	for {
		attempt++
		// TODO(chunk 6+): реальное подключение — mTLS (cert/key/ca из конфига),
		// gRPC-стрим agent.v1.AgentChannel/Channel, Hello → HelloAck → heartbeat.
		log.Info("попытка подключения к серверу", "attempt", attempt, "addr", cfg.ServerAddr)

		select {
		case <-ctx.Done():
			return
		case <-time.After(fullJitter(backoff)):
		}

		backoff *= 2
		if backoff > cfg.BackoffMax.D() {
			backoff = cfg.BackoffMax.D()
		}
	}
}

// fullJitter возвращает случайную задержку в [0, d] (math/rand/v2).
func fullJitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	return time.Duration(rand.Int64N(int64(d)))
}

// fatal печатает ошибку в stderr и завершает процесс (до инициализации логера).
func fatal(err error) {
	fmt.Fprintln(os.Stderr, "surifleet-agent:", err)
	os.Exit(1)
}
