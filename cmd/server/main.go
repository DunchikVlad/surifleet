// SuriFleet Server — единый бинарь сервера с ролями процесса:
// api (stateless REST API), hub (концентратор gRPC-стримов агентов), all.
//
// Версия и коммит подставляются при сборке:
//
//	go build -ldflags "-X main.version=0.1.0 -X main.commit=$(git rev-parse --short HEAD)"
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/surifleet/surifleet/internal/config"
)

// Версия и коммит сборки (переопределяются через -ldflags -X).
var (
	version = "dev"
	commit  = "none"
)

func main() {
	var (
		configPath = flag.String("config", os.Getenv("SURIFLEET_CONFIG"), "путь к YAML-конфигурации")
		roleFlag   = flag.String("role", "", "роль процесса: api|hub|all (по умолчанию — из конфига)")
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

	app := &App{cfg: cfg, log: log}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 2)

	// HTTP API (роль api|all). Реальные зависимости (PostgreSQL, Redis, NATS,
	// S3) подключаются в следующих чанках — здесь только служебные endpoint'ы.
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

	// TODO(chunk 6+): gRPC Hub-стримы агентов (роль hub|all) на cfg.GRPCAddr.

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

// App — корневой объект сервера: конфигурация, логер и (в следующих чанках)
// подключения к PostgreSQL/Redis/NATS/S3.
type App struct {
	cfg *config.ServerConfig
	log *slog.Logger
}

// routes собирает HTTP-маршруты API v1.
func (a *App) routes() http.Handler {
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/health", a.handleHealth)
		r.Get("/version", a.handleVersion)
	})
	return r
}

// handleHealth — GET /api/v1/health: живость процесса.
// Состояние зависимостей появится после их подключения (следующие чанки).
func (a *App) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

// handleVersion — GET /api/v1/version: версия и коммит сборки.
func (a *App) handleVersion(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"version": version, "commit": commit})
}

// writeJSON пишет JSON-ответ.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
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
