// SuriFleet Server — единый бинарь сервера с ролями процесса:
// api (stateless REST API), hub (концентратор gRPC-стримов агентов), all.
//
// Версия и коммит подставляются при сборке:
//
//	go build -ldflags "-X main.version=0.1.0 -X main.commit=$(git rev-parse --short HEAD)"
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

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/surifleet/surifleet/internal/config"
	"github.com/surifleet/surifleet/internal/httpapi"
	"github.com/surifleet/surifleet/internal/store"
)

// Версия и коммит сборки (переопределяются через -ldflags -X).
var (
	version = "dev"
	commit  = "none"
)

func main() {
	var (
		configPath  = flag.String("config", os.Getenv("SURIFLEET_CONFIG"), "путь к YAML-конфигурации")
		roleFlag    = flag.String("role", "", "роль процесса: api|hub|all (по умолчанию — из конфига)")
		migrateOnly = flag.Bool("migrate-only", false, "только применить миграции PostgreSQL и выйти")
	)
	flag.Parse()

	cfg := config.DefaultServer()
	if err := config.LoadSection(*configPath, "server", cfg); err != nil {
		fatal(fmt.Errorf("загрузка конфигурации: %w", err))
	}
	if *roleFlag != "" {
		cfg.Role = *roleFlag
		if err := cfg.Validate(); err != nil {
			fatal(fmt.Errorf("валидация конфигурации: %w", err))
		}
	}

	level, err := config.ParseLogLevel(cfg.LogLevel)
	if err != nil {
		fatal(err)
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(log)

	log.Info("запуск сервера",
		"version", version, "commit", commit,
		"role", cfg.Role,
		"http_addr", cfg.HTTPAddr, "grpc_addr", cfg.GRPCAddr, "metrics_addr", cfg.MetricsAddr,
	)

	// PostgreSQL: пул соединений + автоматические миграции при старте.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := store.Connect(ctx, cfg.PostgresDSN)
	if err != nil {
		fatal(err)
	}
	defer db.Close()

	migVersion, noChange, err := store.Migrate(db.Pool)
	if err != nil {
		fatal(fmt.Errorf("миграции: %w", err))
	}
	if noChange {
		log.Info("миграции: изменений нет", "version", migVersion)
	} else {
		log.Info("миграции применены", "version", migVersion)
	}
	if *migrateOnly {
		log.Info("режим --migrate-only: завершение")
		return
	}

	app := &App{cfg: cfg, log: log, db: db}

	errCh := make(chan error, 2)

	// HTTP API (роль api|all).
	if cfg.Role == "api" || cfg.Role == "all" {
		srv := &http.Server{
			Addr:              cfg.HTTPAddr,
			Handler:           app.routes(),
			ReadHeaderTimeout: 10 * time.Second,
		}
		go func() {
			log.Info("HTTP API слушает", "addr", cfg.HTTPAddr)
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- fmt.Errorf("http api: %w", err)
			}
		}()
		defer shutdownHTTP(log, srv)
	} else {
		log.Info("роль hub: HTTP API отключён")
	}

	// TODO(chunk 8+): gRPC Hub-стримы агентов (роль hub|all) на cfg.GRPCAddr.

	// Метрики Prometheus — отдельный listener для всех ролей.
	if cfg.MetricsAddr != "" {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.Handler())
		ms := &http.Server{
			Addr:              cfg.MetricsAddr,
			Handler:           mux,
			ReadHeaderTimeout: 10 * time.Second,
		}
		go func() {
			log.Info("метрики слушают", "addr", cfg.MetricsAddr)
			if err := ms.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- fmt.Errorf("metrics: %w", err)
			}
		}()
		defer shutdownHTTP(log, ms)
	}

	select {
	case <-ctx.Done():
		log.Info("завершение по сигналу")
	case err := <-errCh:
		log.Error("фатальная ошибка слушателя", "err", err)
		stop()
	}
}

// App — корневой объект сервера: конфигурация, логер и подключение к БД.
type App struct {
	cfg *config.ServerConfig
	log *slog.Logger
	db  *store.Store
}

// routes собирает HTTP-маршруты API v1 (реализация — internal/httpapi).
func (a *App) routes() http.Handler {
	return httpapi.NewRouter(httpapi.Deps{
		Log:     a.log,
		Version: version,
		Commit:  commit,
		Store:   a.db,
		PingDB:  a.db.Pool.Ping,
	})
}

// shutdownHTTP мягко останавливает HTTP-сервер с таймаутом.
func shutdownHTTP(log *slog.Logger, srv *http.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Error("ошибка остановки HTTP-сервера", "addr", srv.Addr, "err", err)
	}
}

// fatal печатает ошибку в stderr и завершает процесс (до инициализации логера).
func fatal(err error) {
	fmt.Fprintln(os.Stderr, "surifleet-server:", err)
	os.Exit(1)
}
