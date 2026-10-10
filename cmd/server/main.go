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
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/surifleet/surifleet/internal/authn"
	"github.com/surifleet/surifleet/internal/blob"
	"github.com/surifleet/surifleet/internal/chlogs"
	"github.com/surifleet/surifleet/internal/config"
	"github.com/surifleet/surifleet/internal/enroll"
	"github.com/surifleet/surifleet/internal/feedsync"
	agentv1 "github.com/surifleet/surifleet/internal/gen/agent/v1"
	"github.com/surifleet/surifleet/internal/httpapi"
	"github.com/surifleet/surifleet/internal/hub"
	"github.com/surifleet/surifleet/internal/iocrules"
	"github.com/surifleet/surifleet/internal/notify"
	"github.com/surifleet/surifleet/internal/oidc"
	"github.com/surifleet/surifleet/internal/orchestrator"
	"github.com/surifleet/surifleet/internal/pki"
	"github.com/surifleet/surifleet/internal/samlauth"
	"github.com/surifleet/surifleet/internal/autoruleset"
	"github.com/surifleet/surifleet/internal/store"
	"github.com/surifleet/surifleet/internal/suriupdate"
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
		"http_addr", cfg.HTTPAddr, "grpc_addr", cfg.GRPCAddr,
		"enroll_addr", cfg.EnrollAddr, "metrics_addr", cfg.MetricsAddr,
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// PostgreSQL: пул соединений + автоматические миграции при старте.
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

	// Встроенный CA (генерируется при первом старте, хранится в ca_dir).
	ca, err := pki.LoadOrCreateCA(cfg.CADir)
	if err != nil {
		fatal(fmt.Errorf("CA: %w", err))
	}
	log.Info("CA загружен", "ca_dir", cfg.CADir, "ca_subject", ca.Cert.Subject)

	// Серверный сертификат (Hub и Enrollment слушатели) от нашего CA.
	srvCertPEM, srvKeyPEM, err := ca.IssueServerCert(cfg.CertSANs)
	if err != nil {
		fatal(fmt.Errorf("серверный сертификат: %w", err))
	}

	// Redis — реестр стримов (presence).
	rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr})
	pingCtx, pingCancel := context.WithTimeout(ctx, 5*time.Second)
	if err := rdb.Ping(pingCtx).Err(); err != nil {
		pingCancel()
		fatal(fmt.Errorf("ping Redis %s: %w", cfg.RedisAddr, err))
	}
	pingCancel()
	defer func() { _ = rdb.Close() }()
	log.Info("Redis подключён", "addr", cfg.RedisAddr)

	hubID, _ := os.Hostname()
	hubID = fmt.Sprintf("%s-%d", hubID, os.Getpid())

	// S3-блобы (ruleset) и оркестратор волновых деплоев (chunk 11).
	blobStore, err := blob.New(ctx, cfg.S3)
	if err != nil {
		fatal(fmt.Errorf("S3: %w", err))
	}
	log.Info("S3 подключено", "endpoint", cfg.S3.Endpoint, "bucket", cfg.S3.Bucket)

	hubSrv := hub.NewServer(db, rdb, hubID, version, log)

	// ClickHouse — приёмник логов агентов (chunk 13c). Пустой DSN — выключено.
	// Ошибка создания таблицы не фатальна: вставка будет ретраиться на каждом
	// LogBatch, а API логов вернёт 503.
	var chLogs *chlogs.Client
	if cfg.ClickHouseDSN != "" {
		chLogs, err = chlogs.New(cfg.ClickHouseDSN)
		if err != nil {
			fatal(fmt.Errorf("clickhouse: %w", err))
		}
		ensureCtx, ensureCancel := context.WithTimeout(ctx, 15*time.Second)
		if err := chLogs.EnsureTable(ensureCtx, cfg.CHRetentionDays); err != nil {
			log.Error("ClickHouse: таблица agent_logs не создана (ретрай при вставке)", "err", err)
		} else {
			log.Info("ClickHouse подключён", "dsn", cfg.ClickHouseDSN, "retention_days", cfg.CHRetentionDays)
		}
		ensureCancel()
		hubSrv.SetLogWriter(chLogs)
	} else {
		log.Info("ClickHouse не настроен (server.clickhouse_dsn пуст) — логи агентов не сохраняются")
	}

	orch := orchestrator.New(db, blobStore, hubSrv, log)
	// Цепочка результатов задач: оркестратор деплоев, затем импорт
	// suricata-update наборов в мастер-репозиторий (чанк 82).
	hubSrv.OnTaskResult = func(ctx context.Context, agentID uuid.UUID, res *agentv1.TaskResult) {
		orch.HandleTaskResult(ctx, agentID, res)
		suriupdate.HandleResult(ctx, log, db, blobStore, agentID, res)
		autoruleset.HandleSuriupdateResult(ctx, log, db, blobStore, orch, agentID, res)
	}
	hubSrv.OnAgentOnline = orch.DispatchPending
	if err := orch.Recover(ctx); err != nil {
		log.Error("восстановление оркестратора", "err", err)
	}

	// Планировщик авто-ruleset'ов (чанк 96): минутный тик, пересборка по
	// расписанию (schedule_time) с автодеплоем на таргетинг.
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if n := autoruleset.RunScheduled(ctx, log, db, blobStore, orch, time.Now()); n > 0 {
					log.Info("auto-ruleset'ы пересобраны по расписанию", "count", n)
				}
			}
		}
	}()

	// Движок уведомлений (чанк 84): переходы online/offline агентов и
	// провалы задач деплоев → каналы webhook/telegram с дедупликацией
	// (окно 10 мин). Доработки чанка 103 (KI-2..KI-4): recovery «агент
	// снова онлайн» через трекер offline-эпизодов, IP/hostname в текстах,
	// уведомления о неудачном обновлении правил/конфигураций.
	notifEngine := notify.NewEngine(db, notify.NewSender(), log)
	offlineTracker := notify.NewOfflineTracker()
	hubSrv.OnAgentStatus = func(_ context.Context, agentID uuid.UUID, status, ip string) {
		emitAgentStatusEvent(notifEngine, db, offlineTracker, log, agentID, status, ip)
	}
	orch.OnTaskFailed = func(ctx context.Context, t store.DeploymentTask, errMsg string) {
		emitDeployFailedEvent(notifEngine, db, hubSrv.AgentIP, log, t, errMsg)
	}

	// Break-glass администратор (чанк 28): в token-режиме гарантируем
	// локального админа вне SSO — иначе войти в систему некому.
	if cfg.AuthMode == "token" {
		bootstrapBreakGlass(ctx, db, cfg, log)
	}

	// Цепочка хэшей аудита (чанк 38): записи audit_log связываются
	// prev_hash→hash (защита от подделки). Выкл. по умолчанию.
	db.Audit.HashChain = cfg.AuditHashChain
	if cfg.AuditHashChain {
		log.Info("цепочка хэшей аудита включена (audit_hash_chain)")
	}

	// Свипер просроченных IOC (чанк 17): active с expires_at < now() → expired.
	// Работает при роли api|all (там же, где HTTP API с генерацией правил).
	if (cfg.Role == "api" || cfg.Role == "all") && cfg.IocSweepInterval.D() > 0 {
		go runIocSweeper(ctx, db, cfg.IocSweepInterval.D(), log)
	}

	// Синхронизация фидов (чанки 18–26 — коннекторы): syncer общий для
	// HTTP-хендлера ручного синка и фонового планировщика (schedule у фида —
	// длительность Go "1h"/"30m" или 5-полевой cron, чанк 27).
	feedSync := &feedsync.Syncer{Store: db, Log: log}
	if (cfg.Role == "api" || cfg.Role == "all") && cfg.FeedSyncInterval.D() > 0 {
		go runFeedScheduler(ctx, feedSync, cfg.FeedSyncInterval.D(), log)
	}

	// Свипер «тихой» смерти агентов (чанк 23): агенты в статусе online, чей
	// last_seen_at старше agent_offline_after, переводятся в offline (с
	// историей и пересчётом compliance в stale). Роль hub|all — там живут
	// стримы агентов и их heartbeat-пульс.
	if (cfg.Role == "hub" || cfg.Role == "all") && cfg.OfflineSweepInterval.D() > 0 && cfg.AgentOfflineAfter.D() > 0 {
		go runAgentOfflineSweeper(ctx, hubSrv, cfg.OfflineSweepInterval.D(), cfg.AgentOfflineAfter.D(), log)
	}

	app := &App{cfg: cfg, log: log, db: db, ca: ca, rdb: rdb, hubID: hubID, hub: hubSrv, blob: blobStore, orch: orch, chLogs: chLogs, feedSync: feedSync}
	errCh := make(chan error, 4)

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

	// gRPC Hub — mTLS-стримы агентов (роль hub|all).
	if cfg.Role == "hub" || cfg.Role == "all" {
		tlsCfg, err := ca.HubServerTLSConfig(srvCertPEM, srvKeyPEM)
		if err != nil {
			fatal(fmt.Errorf("TLS Hub: %w", err))
		}
		grpcSrv := grpc.NewServer(grpc.Creds(credentials.NewTLS(tlsCfg)))
		agentv1.RegisterAgentChannelServer(grpcSrv, hubSrv)
		ln, err := net.Listen("tcp", cfg.GRPCAddr)
		if err != nil {
			fatal(fmt.Errorf("слушатель hub %s: %w", cfg.GRPCAddr, err))
		}
		go func() {
			log.Info("Hub (mTLS) слушает", "addr", cfg.GRPCAddr, "hub_id", hubID)
			if err := grpcSrv.Serve(ln); err != nil {
				errCh <- fmt.Errorf("hub: %w", err)
			}
		}()
		defer func() {
			// GracefulStop шлёт GOAWAY и ждёт завершения стримов.
			done := make(chan struct{})
			go func() { grpcSrv.GracefulStop(); close(done) }()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				grpcSrv.Stop()
			}
		}()
	} else {
		log.Info("роль api: Hub отключён")
	}

	// Enrollment — отдельный TLS-слушатель БЕЗ клиентского сертификата
	// (одноразовый join token внутри RPC), все роли.
	enrollTLS, err := ca.EnrollmentTLSConfig(srvCertPEM, srvKeyPEM)
	if err != nil {
		fatal(fmt.Errorf("TLS enrollment: %w", err))
	}
	enrollSrv := grpc.NewServer(grpc.Creds(credentials.NewTLS(enrollTLS)))
	agentv1.RegisterEnrollmentServer(enrollSrv, enroll.NewService(db, ca, cfg.HubEndpoints, log))
	enrollLn, err := net.Listen("tcp", cfg.EnrollAddr)
	if err != nil {
		fatal(fmt.Errorf("слушатель enrollment %s: %w", cfg.EnrollAddr, err))
	}
	go func() {
		log.Info("Enrollment (TLS) слушает", "addr", cfg.EnrollAddr)
		if err := enrollSrv.Serve(enrollLn); err != nil {
			errCh <- fmt.Errorf("enrollment: %w", err)
		}
	}()
	defer enrollSrv.GracefulStop()

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

// App — корневой объект сервера: конфигурация, логер, БД, CA, Redis.
type App struct {
	cfg      *config.ServerConfig
	log      *slog.Logger
	db       *store.Store
	ca       *pki.CA
	rdb      *redis.Client
	hubID    string
	hub      *hub.Server
	blob     *blob.Store
	orch     *orchestrator.Orchestrator
	chLogs   *chlogs.Client
	feedSync *feedsync.Syncer
}

// routes собирает HTTP-маршруты API v1 (реализация — internal/httpapi).
func (a *App) routes() http.Handler {
	return httpapi.NewRouter(httpapi.Deps{
		Log:      a.log,
		Version:  version,
		Commit:   commit,
		Store:    a.db,
		AuthMode: a.cfg.AuthMode, SessionTTL: a.cfg.SessionTTL.D(),
		Blob:     a.blob,
		Orch:     a.orch,
		Hub:      a.hub,
		CHLogs:   a.chLogs,
		FeedSync: a.feedSync,
		OIDC:     oidc.NewService(a.db),
		SAML:     samlauth.NewService(samlSPKeyDir(a.cfg)),
		Notify:   notify.NewSender(),
		PingDB:   a.db.Pool.Ping,
	})
}

// samlSPKeyDir — каталог постоянного SAML SP-ключа (чанк 42): рядом с CA
// (ca_dir/saml-sp). Ключ самоподписанный, хранится 0600 — метаданные SP
// стабильны между рестартами.
func samlSPKeyDir(cfg *config.ServerConfig) string {
	if cfg.CADir == "" {
		return ""
	}
	return filepath.Join(cfg.CADir, "saml-sp")
}

// shutdownHTTP мягко останавливает HTTP-сервер с таймаутом.
func shutdownHTTP(log *slog.Logger, srv *http.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Error("ошибка остановки HTTP-сервера", "addr", srv.Addr, "err", err)
	}
}

// runIocSweeper — фоновый свип просроченных IOC: раз в interval все active
// с expires_at < now() переводятся в expired (исключаются из генерации
// правил), их сгенерированные правила отзываются в disabled (чанк 19).
// Дублируется свипом внутри POST /iocs/generate.
func runIocSweeper(ctx context.Context, db *store.Store, interval time.Duration, log *slog.Logger) {
	log.Info("IOC-свипер запущен", "interval", interval.String())
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			swept, err := db.Iocs.SweepExpired(ctx)
			if err != nil {
				log.Error("IOC-свипер", "err", err)
				continue
			}
			if len(swept) == 0 {
				continue
			}
			revoked := 0
			for _, ioc := range swept {
				ok, err := iocrules.RevokeForIoc(ctx, ioc.OrganizationID, ioc.Type, ioc.Value, db.Rules)
				if err != nil {
					log.Error("отзыв IOC-правила при свипе", "ioc_id", ioc.ID, "err", err)
					continue
				}
				if ok {
					revoked++
				}
			}
			log.Info("IOC-свипер: погашены просроченные", "expired", len(swept), "revoked_rules", revoked)
		}
	}
}

// runFeedScheduler — фоновый авто-синк фидов (чанк 18): раз в interval
// выбирает enabled-фиды с schedule и синкает те, у которых наступил срок.
// schedule — длительность Go ("1h", "30m") или 5-полевой cron
// ("*/15 * * * *", @daily; локальное время сервера) — чанк 27.
// Автопрогон генерации правил выполняет только ручной sync
// (POST /feeds/{id}/sync); плановый — только импорт.
func runFeedScheduler(ctx context.Context, syncer *feedsync.Syncer, interval time.Duration, log *slog.Logger) {
	log.Info("планировщик авто-синка фидов запущен", "interval", interval.String())
	t := time.NewTicker(interval)
	defer t.Stop()
	// badSchedule — фиды с непарсящимся schedule, предупреждаем один раз.
	badSchedule := map[uuid.UUID]bool{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			feeds, err := syncer.Store.Feeds.ListScheduled(ctx)
			if err != nil {
				log.Error("планировщик фидов: список", "err", err)
				continue
			}
			for _, f := range feeds {
				sc, err := feedsync.ParseSchedule(*f.Schedule)
				if err != nil {
					if !badSchedule[f.ID] {
						badSchedule[f.ID] = true
						log.Warn("планировщик фидов: schedule не распознан — авто-синк пропущен",
							"feed_id", f.ID, "name", f.Name, "schedule", *f.Schedule, "err", err)
					}
					continue
				}
				delete(badSchedule, f.ID) // расписание исправлено
				if !sc.Due(f.LastSyncAt, time.Now()) {
					continue // ещё рано
				}
				syncCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
				run, err := syncer.Sync(syncCtx, f)
				cancel()
				if err != nil {
					log.Error("планировщик фидов: sync", "feed_id", f.ID, "err", err)
				} else if run.Status == "failed" {
					log.Warn("планировщик фидов: sync failed",
						"feed_id", f.ID, "name", f.Name, "error", run.Error)
				}
			}
		}
	}
}

// runAgentOfflineSweeper — фоновый детект «тихой» смерти агента (чанк 23):
// раз в interval гасит агентов online с протухшим last_seen_at (старше
// offlineAfter). Закрывает инцидент 2026-09-16: мёртвый процесс агента 2 часа
// отображался online, т.к. статус менялся только при разрыве gRPC-стрима.
func runAgentOfflineSweeper(ctx context.Context, hubSrv *hub.Server, interval, offlineAfter time.Duration, log *slog.Logger) {
	log.Info("свипер offline-агентов запущен", "interval", interval.String(), "offline_after", offlineAfter.String())
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			sweepCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			n, err := hubSrv.SweepOfflineAgents(sweepCtx, offlineAfter)
			cancel()
			if err != nil {
				log.Error("свипер offline-агентов", "err", err)
			} else if n > 0 {
				log.Info("свипер offline-агентов: погашены", "count", n)
			}
		}
	}
}

// fatal печатает ошибку в stderr и завершает процесс (до инициализации логера).
func fatal(err error) {
	fmt.Fprintln(os.Stderr, "surifleet-server:", err)
	os.Exit(1)
}

// bootstrapBreakGlass — гарантия локального break-glass администратора в
// token-режиме (чанк 28): если в старейшей организации нет активных
// break-glass пользователей, создаётся admin с ролью admin. Пароль — из
// конфига/env; если не задан, генерируется случайный и пишется в лог ОДИН
// раз (WARN). Ошибки не фатальны: логируются (сервер поднимается, войти
// пока будет нельзя — видно в логе).
func bootstrapBreakGlass(ctx context.Context, db *store.Store, cfg *config.ServerConfig, log *slog.Logger) {
	org, err := db.Organizations.First(ctx)
	if errors.Is(err, store.ErrNotFound) {
		org, err = db.Organizations.Create(ctx, store.OrganizationInput{Name: "Default", Slug: "default"})
	}
	if err != nil {
		log.Error("bootstrap break-glass: организация", "err", err)
		return
	}
	n, err := db.Users.CountActiveBreakGlass(ctx, org.ID)
	if err != nil {
		log.Error("bootstrap break-glass: проверка", "err", err)
		return
	}
	if n > 0 {
		return
	}
	password := cfg.BootstrapAdminPassword
	generated := password == ""
	if generated {
		token, err := authn.NewToken()
		if err != nil {
			log.Error("bootstrap break-glass: генерация пароля", "err", err)
			return
		}
		password = token
	}
	hash, err := authn.HashPassword(password)
	if err != nil {
		log.Error("bootstrap break-glass: хэш пароля", "err", err)
		return
	}
	adminRole, err := db.Roles.BuiltinByName(ctx, "admin")
	if err != nil {
		log.Error("bootstrap break-glass: роль admin", "err", err)
		return
	}
	u, err := db.Users.Create(ctx, org.ID, store.UserCreateInput{
		Email: cfg.BootstrapAdminEmail, DisplayName: "Break-glass Administrator",
		PasswordHash: hash, IsBreakGlass: true,
		Roles: []store.RoleAssignmentInput{{RoleID: adminRole.ID}},
	})
	if err != nil {
		log.Error("bootstrap break-glass: создание пользователя", "err", err)
		return
	}
	if generated {
		log.Warn("создан break-glass администратор — пароль сгенерирован, смените после входа",
			"email", u.Email, "password", password)
	} else {
		log.Info("создан break-glass администратор", "email", u.Email)
	}
}
