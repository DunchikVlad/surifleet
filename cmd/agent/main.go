// SuriFleet Agent — агент хоста-сенсора: единый статический бинарь,
// исходящее gRPC-подключение к серверу (enrollment → mTLS-стрим Hub),
// локальное логирование в файл с ротацией + дублирование в stdout.
//
// Версия подставляется при сборке: -ldflags "-X main.version=0.1.0".
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

// version — версия агента (переопределяется через -ldflags -X).
var version = "dev"

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

	// Уровень логирования — динамический: сервер может менять его на лету
	// (LogLevelChange / HelloAck, docs/protocol.md §5).
	level, err := config.ParseLogLevel(cfg.LogLevel)
	if err != nil {
		fatal(err)
	}
	levelVar := &slog.LevelVar{}
	levelVar.Set(level)
	// Локальный файл — источник истины при потере связи; stdout — для journald/отладки.
	// captureHandler дополнительно складывает записи в logBuf — буфер
	// доставки логов на сервер (chunk 13c); переживает разрывы сессий.
	logBuf := newLogBuffer()
	log := slog.New(&captureHandler{
		next: slog.NewJSONHandler(io.MultiWriter(os.Stdout, rotator), &slog.HandlerOptions{Level: levelVar}),
		buf:  logBuf,
	})
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Info("запуск агента",
		"version", version,
		"server_addr", cfg.ServerAddr,
		"enroll_addr", cfg.EnrollAddr,
		"data_dir", cfg.DataDir,
		"log_file", logFile,
		"backoff", fmt.Sprintf("%s..%s", cfg.BackoffMin.D(), cfg.BackoffMax.D()),
	)

	// Идентичность: сертификат с диска или enrollment по join token.
	id, err := loadOrEnroll(ctx, cfg, log)
	if err != nil {
		fatal(err)
	}

	connectLoop(ctx, cfg, id, levelVar, log, logBuf)
	log.Info("агент остановлен")
}

// connectLoop — цикл переподключения к Hub: exponential backoff
// (backoff_min → ×2 → backoff_max) с полным jitter (защита от
// reconnect-штормов, см. docs/architecture.md §4.7).
func connectLoop(ctx context.Context, cfg *config.AgentConfig, id *identity, levelVar *slog.LevelVar, log *slog.Logger, logBuf *logBuffer) {
	backoff := cfg.BackoffMin.D()
	for {
		err := runSession(ctx, cfg, id, levelVar, log, logBuf)
		if ctx.Err() != nil {
			return // остановка по сигналу
		}
		log.Warn("сессия завершилась, переподключение", "err", err, "backoff", backoff)

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
