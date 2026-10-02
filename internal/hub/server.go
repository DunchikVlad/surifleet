// Package hub — концентратор gRPC-стримов агентов (роль hub|all).
//
// AgentChannel.Channel поверх mTLS (CN клиентского сертификата = agent_id).
// Первым сообщением ждёт Hello (таймаут 10 с), сверяет agent_id с CN,
// регистрирует presence в Redis (ключ stream:{agent_id}, TTL 120 с,
// продлевается heartbeat'ами), смены статуса пишет в PostgreSQL
// (agents.status/last_seen_at + agent_state_history). См.
// docs/architecture.md §4, §5.2.
package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/surifleet/surifleet/internal/chlogs"
	agentv1 "github.com/surifleet/surifleet/internal/gen/agent/v1"
	"github.com/surifleet/surifleet/internal/store"
)

// Параметры стрима (docs/architecture.md §4.3, §5.2).
const (
	helloTimeout  = 10 * time.Second  // ожидание первого Hello
	presenceTTL   = 120 * time.Second // TTL ключа stream:{agent_id} в Redis
	clockSkewWarn = 60_000            // |clock_offset_ms| свыше — warn
	protocolMajor = 1                 // поддерживаемая версия протокола
	// touchLastSeenMin — heartbeat пишет last_seen_at в PostgreSQL не чаще
	// этого интервала (по нему свипер детектирует «тихую» смерть агента).
	// 30 с = heartbeat-интервал: худший разрыв между пульсами ~60 с
	// (тик на 29.98 с пропускается), поэтому agent_offline_after должен
	// быть ≥ 2× этого значения (дефолт 120 с).
	touchLastSeenMin = 30 * time.Second
)

// presence — значение ключа stream:{agent_id} в Redis.
type presence struct {
	HubID       string `json:"hub_id"`
	SessionID   string `json:"session_id"`
	ConnectedAt string `json:"connected_at"`
}

// Server — реализация agent.v1.AgentChannel.
type Server struct {
	agentv1.UnimplementedAgentChannelServer

	db      *store.Store
	rdb     *redis.Client
	hubID   string
	version string
	log     *slog.Logger

	// chLogs — приёмник логов агентов в ClickHouse (nil — запись выключена).
	chLogs *chlogs.Client

	// streams — реестр подключённых стримов (agentID → *streamHandle),
	// in-process доставка задач (chunk 11).
	streams sync.Map

	// lastTouch — agentID → момент последней записи last_seen_at в PG
	// (троттлинг heartbeat-пульса, chunk 23).
	lastTouch sync.Map

	// OnTaskResult — подписчик результатов задач (оркестратор деплоев).
	OnTaskResult func(ctx context.Context, agentID uuid.UUID, res *agentv1.TaskResult)
	// OnAgentOnline — подписчик подключения агента (подхват pending-задач).
	OnAgentOnline func(ctx context.Context, agentID uuid.UUID)
}

// NewServer собирает Hub.
func NewServer(db *store.Store, rdb *redis.Client, hubID, version string, log *slog.Logger) *Server {
	return &Server{db: db, rdb: rdb, hubID: hubID, version: version, log: log}
}

// SetLogWriter подключает приёмник логов агентов (ClickHouse); nil — выкл.
func (s *Server) SetLogWriter(c *chlogs.Client) { s.chLogs = c }

// Channel — основной стрим агента (см. контракт agent.proto).
func (s *Server) Channel(stream grpc.BidiStreamingServer[agentv1.AgentMessage, agentv1.ServerMessage]) error {
	ctx := stream.Context()
	log := s.log

	// Идентичность из mTLS: CN клиентского сертификата.
	certAgentID, err := peerAgentID(ctx)
	if err != nil {
		return status.Errorf(codes.Unauthenticated, "mTLS-идентичность: %v", err)
	}

	// Первое сообщение — Hello, с таймаутом (агент без Hello бесполезен).
	helloMsg, err := recvWithTimeout(stream, helloTimeout)
	if err != nil {
		return status.Errorf(codes.DeadlineExceeded, "ожидание Hello: %v", err)
	}
	hello := helloMsg.GetHello()
	if hello == nil {
		return status.Error(codes.InvalidArgument, "первое сообщение стрима должно быть Hello")
	}

	// Сверка agent_id из Hello с CN сертификата (защита от чужого agent_id).
	agentID, err := uuid.Parse(hello.GetAgentId())
	if err != nil {
		return status.Error(codes.InvalidArgument, "Hello.agent_id должен быть UUID")
	}
	if agentID.String() != certAgentID {
		return status.Errorf(codes.PermissionDenied,
			"agent_id %s не совпадает с CN сертификата %s", agentID, certAgentID)
	}
	if hello.GetProtocolVersion() != protocolMajor {
		return status.Errorf(codes.FailedPrecondition,
			"неподдерживаемая версия протокола %d (сервер поддерживает %d)", hello.GetProtocolVersion(), protocolMajor)
	}

	// Агент должен существовать в БД (зарегистрирован enrollment'ом).
	if _, err := s.db.Agents.GetByID(ctx, agentID); errors.Is(err, store.ErrNotFound) {
		return status.Error(codes.PermissionDenied, "агент не зарегистрирован (enrollment не пройден)")
	} else if err != nil {
		return status.Errorf(codes.Internal, "проверка агента: %v", err)
	}

	sessionID := uuid.New().String()
	log = log.With("agent_id", agentID, "session_id", sessionID, "hostname", hello.GetHostname())
	log.Info("агент подключился",
		"agent_version", hello.GetAgentVersion(), "boot_id", hello.GetBootId(),
		"instances", len(hello.GetInstances()), "capabilities", hello.GetCapabilities())

	// Online: PostgreSQL (статус + история) и Redis (presence с TTL).
	details := map[string]any{"hub_id": s.hubID, "session_id": sessionID, "boot_id": hello.GetBootId()}
	if err := s.db.Agents.SetStatus(ctx, agentID, "online", details); err != nil {
		log.Error("установка статуса online", "err", err)
		return status.Errorf(codes.Internal, "смена статуса: %v", err)
	}
	if err := s.setPresence(ctx, agentID, sessionID); err != nil {
		log.Error("регистрация presence в Redis", "err", err)
	}
	// SetStatus уже записал last_seen_at=now() — считаем пульс свежим.
	s.lastTouch.Store(agentID, time.Now())

	// Отключение — при выходе из функции (разрыв, ошибка, shutdown).
	// Защита от дубль-стрима: статус offline ставим, только если в реестре
	// всё ещё ЭТА сессия (иначе агент переподключился и жив на новой).
	defer func() {
		s.lastTouch.Delete(agentID)
		cur, registered := s.streams.Load(agentID)
		if registered && cur.(*streamHandle).sessionID != sessionID {
			log.Info("отключилась старая сессия — агент жив на новой, статус не меняем",
				"old_session", sessionID)
			return
		}
		bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.db.Agents.SetStatus(bgCtx, agentID, "offline", map[string]any{"hub_id": s.hubID, "session_id": sessionID}); err != nil {
			log.Error("установка статуса offline", "err", err)
		}
		if err := s.rdb.Del(bgCtx, presenceKey(agentID)).Err(); err != nil {
			log.Error("удаление presence из Redis", "err", err)
		}
		if err := s.rdb.SRem(bgCtx, "hub:"+s.hubID+":agents", agentID.String()).Err(); err != nil {
			log.Error("srem hub-set", "err", err)
		}
		// Агент офлайн — compliance инстансов хоста больше недостоверен (stale).
		s.recomputeHostCompliance(bgCtx, log, agentID, false)
		log.Info("агент отключился")
	}()

	// HelloAck — параметры сессии и начальная конфигурация (включая
	// включённые capability хоста: host → cluster → дефолт monitoring).
	caps := s.hostCapabilities(ctx, log, agentID)
	bindings := s.instanceBindings(ctx, log, agentID)
	if err := stream.Send(&agentv1.ServerMessage{
		MsgId:  uuid.New().String(),
		Seq:    1,
		SentAt: timestamppb.Now(),
		Payload: &agentv1.ServerMessage_HelloAck{HelloAck: &agentv1.HelloAck{
			SessionId:                  sessionID,
			ServerVersion:              s.version,
			HeartbeatIntervalSeconds:   30,
			StateReportIntervalSeconds: 300,
			MetricsIntervalSeconds:     60,
			LogLevel:                   "info",
			Config:                     &agentv1.AgentConfig{LogLevel: "info", Capabilities: caps},
			BoundInstances:             bindings,
		}},
	}); err != nil {
		return fmt.Errorf("отправка HelloAck: %w", err)
	}

	// Реестр стрима: с этого момента открыт приём задач через SendTask.
	// Отдельная горутина-отправитель — единственный писатель в stream.Send
	// (seq сервера монотонен с 2; HelloAck ушёл с seq=1).
	handle := &streamHandle{out: make(chan *agentv1.ServerMessage, 64), sessionID: sessionID}
	s.streams.Store(agentID, handle)
	defer func() {
		// Удаляем только СВОЮ сессию: если агент переподключился, в реестре
		// новый handle, его трогать нельзя (дубль-стрим, chunk 28).
		if cur, ok := s.streams.Load(agentID); ok && cur.(*streamHandle).sessionID == sessionID {
			s.streams.Delete(agentID)
		}
	}()
	go func() {
		seq := int64(2)
		for {
			select {
			case <-ctx.Done():
				return
			case m := <-handle.out:
				m.Seq = seq
				seq++
				m.SentAt = timestamppb.Now()
				if err := stream.Send(m); err != nil {
					log.Warn("отправка в стрим", "err", err)
					return
				}
			}
		}
	}()

	// Агент онлайн: пересчёт compliance его инстансов (из stale в фактический
	// статус) и подхват накопленных pending-задач (оркестратор).
	s.recomputeHostCompliance(ctx, log, agentID, true)
	if s.OnAgentOnline != nil {
		go s.OnAgentOnline(context.Background(), agentID)
	}

	// Основной цикл приёма сообщений агента.
	lastSeq := helloMsg.GetSeq()
	for {
		msg, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return nil // разрыв соединения — defer зафиксирует offline
		}

		// Replay-защита: seq строго монотонен в рамках сессии (§4.6).
		if msg.GetSeq() <= lastSeq {
			log.Warn("разрыв: немонотонный seq", "seq", msg.GetSeq(), "last", lastSeq)
			return status.Errorf(codes.InvalidArgument, "seq %d <= %d: нарушение монотонности", msg.GetSeq(), lastSeq)
		}
		lastSeq = msg.GetSeq()

		s.handleMessage(ctx, log, agentID, sessionID, msg)
	}
}

// handleMessage — разбор одного сообщения агента (после проверки seq).
func (s *Server) handleMessage(ctx context.Context, log *slog.Logger, agentID uuid.UUID, sessionID string, msg *agentv1.AgentMessage) {
	// Детект рассинхронизации часов по sent_at (protocol.md §2).
	if sentAt := msg.GetSentAt(); sentAt != nil {
		if skew := time.Since(sentAt.AsTime()); skew > clockSkewWarn*time.Millisecond || skew < -clockSkewWarn*time.Millisecond {
			log.Warn("рассинхронизация часов агента", "skew_ms", skew.Milliseconds())
		}
	}

	switch p := msg.GetPayload().(type) {
	case *agentv1.AgentMessage_Heartbeat:
		hb := p.Heartbeat
		if off := hb.GetClockOffsetMs(); off > clockSkewWarn || off < -clockSkewWarn {
			log.Warn("большой clock_offset агента", "clock_offset_ms", off)
		}
		// Heartbeat продлевает Redis-TTL и (не чаще touchLastSeenMin) пишет
		// last_seen_at в PG — по нему свипер детектирует «тихую» смерть
		// процесса агента (чанк 23).
		if err := s.touchPresence(ctx, agentID); err != nil {
			log.Error("продление presence", "err", err)
		}
		if last, ok := s.lastTouch.Load(agentID); !ok || time.Since(last.(time.Time)) >= touchLastSeenMin {
			revived, err := s.db.Agents.HeartbeatPulse(ctx, agentID)
			if err != nil {
				log.Error("heartbeat: запись last_seen_at", "err", err)
			} else {
				s.lastTouch.Store(agentID, time.Now())
				if revived {
					// Свипер успел погасить агента, но стрим жив — вернули online.
					log.Warn("агент снова online: heartbeat возобновился")
					s.recomputeHostCompliance(ctx, log, agentID, true)
				}
			}
		}
		res := hb.GetResources()
		log.Debug("heartbeat",
			"uptime_s", hb.GetUptimeSeconds(), "agent_version", hb.GetAgentVersion(),
			"suricata_version", hb.GetSuricataVersion(),
			"cpu_pct", fmt.Sprintf("%.1f", res.GetCpuPercent()),
			"mem_bytes", res.GetMemBytes(),
			"disk_pct", fmt.Sprintf("%.1f", res.GetDiskUsedPercent()),
			"svc_statuses", len(hb.GetInstances()))
		// Статусы сервисов инстансов — в Redis (живут до TTL, §5.2).
		// instance_id появляется у агента только после confirm_discovery;
		// статусы с пустым/неизвестным id пропускаем.
		for _, st := range hb.GetInstances() {
			iid, err := uuid.Parse(st.GetInstanceId())
			if err != nil {
				continue
			}
			val, _ := json.Marshal(map[string]any{
				"state": st.GetState(), "pid": st.GetPid(),
				"agent_id": agentID.String(),
				"at":       time.Now().UTC().Format(time.RFC3339),
			})
			if err := s.rdb.Set(ctx, instanceSvcKey(iid), val, presenceTTL).Err(); err != nil {
				log.Error("instance_svc в Redis", "instance_id", iid, "err", err)
			}
		}

	case *agentv1.AgentMessage_DiscoveryReport:
		s.handleDiscoveryReport(ctx, log, agentID, p.DiscoveryReport)

	case *agentv1.AgentMessage_StateReport:
		s.handleStateReport(ctx, log, p.StateReport)

	case *agentv1.AgentMessage_RuleLoadReport:
		s.handleRuleLoadReport(ctx, log, p.RuleLoadReport)

	case *agentv1.AgentMessage_LogBatch:
		s.handleLogBatch(ctx, log, agentID, p.LogBatch)

	case *agentv1.AgentMessage_MetricsBatch:
		log.Debug("MetricsBatch", "points", len(p.MetricsBatch.GetPoints()))
		// TODO(chunk 9+): запись метрик в ClickHouse.

	case *agentv1.AgentMessage_TaskResult:
		s.handleTaskResult(ctx, log, agentID, p.TaskResult)

	default:
		log.Debug("сообщение агента", "type", fmt.Sprintf("%T", p), "msg_id", msg.GetMsgId())
	}
}

// presenceKey — ключ реестра стримов (§5.2).
func presenceKey(agentID uuid.UUID) string { return "stream:" + agentID.String() }

// instanceSvcKey — ключ статуса сервиса инстанса (heartbeat → Redis, TTL 120 с).
func instanceSvcKey(instanceID uuid.UUID) string { return "instance_svc:" + instanceID.String() }

// handleDiscoveryReport сохраняет DiscoveryReport агента в hosts.discovery
// (jsonb) + discovered_at. Хост находится по agent_id (агент своего host_id
// не знает). encoding/json даёт snake_case по тегам сгенерированных
// proto-структур — форму openapi DiscoveryReport (без обёртки
// host_id/received_at, они добавляются HTTP-слоем из колонок).
func (s *Server) handleDiscoveryReport(ctx context.Context, log *slog.Logger, agentID uuid.UUID, rep *agentv1.DiscoveryReport) {
	host, err := s.db.Hosts.GetByAgentID(ctx, agentID)
	if err != nil {
		log.Error("DiscoveryReport: хост агента не найден", "err", err)
		return
	}
	raw, err := json.Marshal(rep)
	if err != nil {
		log.Error("DiscoveryReport: marshal", "err", err)
		return
	}
	if err := s.db.Hosts.SetDiscovery(ctx, host.ID, raw); err != nil {
		log.Error("DiscoveryReport: сохранение", "host_id", host.ID, "err", err)
		return
	}
	log.Info("DiscoveryReport сохранён",
		"host_id", host.ID, "instances", len(rep.GetInstances()),
		"binary", rep.GetBinary().GetPath(), "suricata_version", rep.GetBinary().GetVersion())
}

// handleLogBatch пишет батч операционных логов агента в ClickHouse
// (surifleet.agent_logs, chunk 13c). Ошибка вставки логируется, батч
// теряется (логи — best-effort; локальный файл агента — источник истины).
// Атрибуты slog при наличии добавляются к message компактным JSON.
func (s *Server) handleLogBatch(ctx context.Context, log *slog.Logger, agentID uuid.UUID, batch *agentv1.LogBatch) {
	entries := batch.GetEntries()
	if s.chLogs == nil || len(entries) == 0 {
		return
	}
	rows := make([]chlogs.AgentLogRow, 0, len(entries))
	for _, e := range entries {
		ts := time.Now().UTC()
		if e.GetTs() != nil {
			ts = e.GetTs().AsTime()
		}
		msg := e.GetMsg()
		if len(e.GetAttrs()) > 0 {
			if raw, err := json.Marshal(e.GetAttrs()); err == nil {
				msg = msg + " " + string(raw)
			}
		}
		rows = append(rows, chlogs.AgentLogRow{
			AgentID: agentID.String(),
			Ts:      chlogs.FormatTS(ts),
			Level:   strings.ToLower(e.GetLevel()),
			Message: msg,
		})
	}
	if err := s.chLogs.InsertAgentLogs(ctx, rows); err != nil {
		log.Error("LogBatch: вставка в ClickHouse", "entries", len(rows), "err", err)
		return
	}
	log.Debug("LogBatch записан в ClickHouse", "entries", len(rows))
}

// setPresence регистрирует стрим: stream:{agent_id} → presence (TTL 120 с),
// агент добавляется в set hub:{hub_id}:agents.
func (s *Server) setPresence(ctx context.Context, agentID uuid.UUID, sessionID string) error {
	val, err := json.Marshal(presence{
		HubID:       s.hubID,
		SessionID:   sessionID,
		ConnectedAt: time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return err
	}
	if err := s.rdb.Set(ctx, presenceKey(agentID), val, presenceTTL).Err(); err != nil {
		return err
	}
	return s.rdb.SAdd(ctx, "hub:"+s.hubID+":agents", agentID.String()).Err()
}

// touchPresence продлевает TTL ключа presence (на каждый heartbeat).
func (s *Server) touchPresence(ctx context.Context, agentID uuid.UUID) error {
	return s.rdb.Expire(ctx, presenceKey(agentID), presenceTTL).Err()
}

// SweepOfflineAgents — проход свипера «тихой» смерти (чанк 23): агенты со
// статусом online и протухшим last_seen_at (старше offlineAfter) переводятся
// в offline с записью в agent_state_history, их presence в Redis удаляется,
// compliance инстансов хоста пересчитывается в stale. Возвращает число
// погашенных агентов. Живой стрим, чей heartbeat свеж, не затрагивается.
func (s *Server) SweepOfflineAgents(ctx context.Context, offlineAfter time.Duration) (int, error) {
	ids, err := s.db.Agents.SweepStaleOnline(ctx, time.Now().Add(-offlineAfter))
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		if err := s.rdb.Del(ctx, presenceKey(id)).Err(); err != nil {
			s.log.Error("свипер offline: удаление presence", "agent_id", id, "err", err)
		}
		if err := s.rdb.SRem(ctx, "hub:"+s.hubID+":agents", id.String()).Err(); err != nil {
			s.log.Error("свипер offline: srem hub-set", "agent_id", id, "err", err)
		}
		s.lastTouch.Delete(id)
		s.log.Warn("агент помечен offline: heartbeat-timeout",
			"agent_id", id, "offline_after", offlineAfter.String())
		s.recomputeHostCompliance(ctx, s.log, id, false)
	}
	return len(ids), nil
}

// peerAgentID — CN клиентского сертификата из mTLS-контекста gRPC.
func peerAgentID(ctx context.Context) (string, error) {
	p, ok := peer.FromContext(ctx)
	if !ok || p.AuthInfo == nil {
		return "", errors.New("нет данных пира")
	}
	ti, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok {
		return "", errors.New("соединение без TLS")
	}
	if len(ti.State.VerifiedChains) == 0 || len(ti.State.VerifiedChains[0]) == 0 {
		return "", errors.New("клиентский сертификат не проверен")
	}
	cn := ti.State.VerifiedChains[0][0].Subject.CommonName
	if cn == "" {
		return "", errors.New("пустой CN клиентского сертификата")
	}
	return cn, nil
}

// recvWithTimeout — первый Recv стрима с дедлайном (ожидание Hello).
func recvWithTimeout(stream grpc.BidiStreamingServer[agentv1.AgentMessage, agentv1.ServerMessage], d time.Duration) (*agentv1.AgentMessage, error) {
	type result struct {
		msg *agentv1.AgentMessage
		err error
	}
	ch := make(chan result, 1)
	go func() {
		m, err := stream.Recv()
		ch <- result{m, err}
	}()
	select {
	case r := <-ch:
		return r.msg, r.err
	case <-time.After(d):
		return nil, fmt.Errorf("Hello не получен за %s", d)
	case <-stream.Context().Done():
		return nil, stream.Context().Err()
	}
}
