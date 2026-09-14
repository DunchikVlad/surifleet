// Package enroll — gRPC-сервис первичной регистрации агентов (enrollment).
//
// Отдельный TLS-слушатель (server.enroll_addr, по умолчанию :8444) БЕЗ
// клиентского сертификата (его ещё нет): аутентификация — одноразовым
// join token. Успех: агент получает сертификат (CN = agent_id) для mTLS-стрима
// Hub. См. docs/architecture.md §10, docs/protocol.md §9.
package enroll

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	agentv1 "github.com/surifleet/surifleet/internal/gen/agent/v1"
	"github.com/surifleet/surifleet/internal/pki"
	"github.com/surifleet/surifleet/internal/store"
)

// Service — реализация agent.v1.Enrollment.
type Service struct {
	agentv1.UnimplementedEnrollmentServer

	db           *store.Store
	ca           *pki.CA
	hubEndpoints []string
	log          *slog.Logger
}

// NewService собирает enrollment-сервис.
func NewService(db *store.Store, ca *pki.CA, hubEndpoints []string, log *slog.Logger) *Service {
	return &Service{db: db, ca: ca, hubEndpoints: hubEndpoints, log: log}
}

// Enroll — обмен join token + CSR на сертификат агента (одноразово).
//
// Порядок: валидация входа → транзакция (расход токена, переиспользование
// или создание хоста по (cluster_id, hostname), проверка отсутствия агента,
// создание агента) → подпись CSR. Повторный enrollment хоста, у которого уже
// есть агент, отклоняется AlreadyExists (пере-enrollment — отдельная фича,
// отложена; перевыпуск — по новому join token после удаления агента).
func (s *Service) Enroll(ctx context.Context, req *agentv1.EnrollRequest) (*agentv1.EnrollResponse, error) {
	if req.GetJoinToken() == "" {
		return nil, status.Error(codes.InvalidArgument, "join_token обязателен")
	}
	host := req.GetHost()
	if host == nil || host.GetHostname() == "" {
		return nil, status.Error(codes.InvalidArgument, "host.hostname обязателен")
	}
	if len(req.GetCsr()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "csr обязателен (PEM PKCS#10)")
	}

	// Подписываем CSR заранее (до транзакции): при битом CSR не тратим токен.
	// agent_id ещё нет — генерируем заранее и кладём в CN.
	agentID := uuid.New()
	certPEM, serial, err := s.ca.SignCSR(req.GetCsr(), agentID.String(), pki.AgentCertTTL)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "некорректный CSR: %v", err)
	}
	certExpires := time.Now().Add(pki.AgentCertTTL)

	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Атомарный расход токена: существует, не истёк, лимит не исчерпан.
	tok, err := s.db.JoinTokens.Consume(ctx, tx, store.HashToken(req.GetJoinToken()))
	if errors.Is(err, store.ErrNotFound) {
		return nil, status.Error(codes.Unauthenticated, "join token недействителен, истёк или исчерпан")
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "проверка токена: %v", err)
	}

	// Хост: переиспользуем по (cluster_id, hostname) или создаём.
	h, err := s.db.Hosts.FindHostByHostname(ctx, tx, tok.ClusterID, host.GetHostname())
	if errors.Is(err, store.ErrNotFound) {
		h, err = s.db.Hosts.CreateHostTx(ctx, tx, store.HostInput{
			ClusterID:   tok.ClusterID,
			Hostname:    host.GetHostname(),
			IPAddresses: host.GetIpAddresses(),
			OS:          strPtrOrNil(host.GetOs()),
		})
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "регистрация хоста: %v", err)
	}

	// Повторный enrollment хоста с существующим агентом — конфликт.
	if _, err := s.db.Agents.GetByHostID(ctx, tx, h.ID); err == nil {
		return nil, status.Errorf(codes.AlreadyExists,
			"хост %q уже имеет зарегистрированного агента; пере-enrollment пока не поддержан", h.Hostname)
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, status.Errorf(codes.Internal, "проверка агента: %v", err)
	}

	if _, err := s.db.Agents.CreateEnrolled(ctx, tx, agentID, h.ID, host.GetAgentVersion(), serial, certExpires); err != nil {
		return nil, status.Errorf(codes.Internal, "создание агента: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, status.Errorf(codes.Internal, "commit: %v", err)
	}

	s.log.Info("агент зарегистрирован",
		"agent_id", agentID, "host_id", h.ID, "hostname", h.Hostname,
		"cluster_id", tok.ClusterID, "token_id", tok.ID,
	)

	return &agentv1.EnrollResponse{
		AgentId:      agentID.String(),
		Certificate:  certPEM,
		CaChain:      s.ca.CertPEM,
		HubEndpoints: s.hubEndpoints,
		Config:       defaultAgentConfig(),
	}, nil
}

// defaultAgentConfig — начальная конфигурация агента (интервалы по умолчанию
// из docs/protocol.md §5: heartbeat 30 с, state report 300 с, metrics 60 с).
func defaultAgentConfig() *agentv1.AgentConfig {
	return &agentv1.AgentConfig{
		LogLevel:                   "info",
		LogMaxSizeMb:               100,
		LogMaxBackups:              5,
		HeartbeatIntervalSeconds:   30,
		StateReportIntervalSeconds: 300,
		MetricsIntervalSeconds:     60,
	}
}

func strPtrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
