package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/surifleet/surifleet/internal/config"
	agentv1 "github.com/surifleet/surifleet/internal/gen/agent/v1"
)

// protocolMajor — версия протокола агента (docs/protocol.md §7).
const protocolMajor = 1

// runSession — одна сессия стрима агента: mTLS-подключение к Hub,
// Hello → HelloAck → heartbeat'ы + приём серверных сообщений.
// Возвращает ошибку разрыва (по ней connectLoop уходит в backoff).
func runSession(ctx context.Context, cfg *config.AgentConfig, id *identity, levelVar *slog.LevelVar, log *slog.Logger) error {
	cert, err := tls.X509KeyPair(id.certPEM, id.keyPEM)
	if err != nil {
		return fmt.Errorf("сертификат агента: %w", err)
	}
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      id.certPool,
		MinVersion:   tls.VersionTLS12,
	}

	log.Info("подключение к Hub", "addr", id.hubAddr, "agent_id", id.agentID)
	conn, err := grpc.NewClient(id.hubAddr, grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)))
	if err != nil {
		return fmt.Errorf("создание gRPC-клиента: %w", err)
	}
	defer conn.Close()

	stream, err := agentv1.NewAgentChannelClient(conn).Channel(ctx)
	if err != nil {
		return fmt.Errorf("открытие стрима: %w", err)
	}

	// seq — монотонный счётчик сообщений агента в рамках сессии (§3 protocol.md).
	var seq atomic.Int64
	var sendMu sync.Mutex // stream.Send не потокобезопасен
	// send отправляет сообщение с заполненным payload
	// (тип oneof-интерфейса неэкспортируем — payload ставится на месте).
	send := func(m *agentv1.AgentMessage) error {
		sendMu.Lock()
		defer sendMu.Unlock()
		m.MsgId = uuid.New().String()
		m.Seq = seq.Add(1)
		m.SentAt = timestamppb.Now()
		return stream.Send(m)
	}

	hostname, _ := osHostname()
	startedAt := time.Now()
	if err := send(&agentv1.AgentMessage{Payload: &agentv1.AgentMessage_Hello{Hello: &agentv1.Hello{
		AgentId:         id.agentID,
		AgentVersion:    version,
		ProtocolVersion: protocolMajor,
		BootId:          bootID(),
		Hostname:        hostname,
		Instances:       nil, // управляемые инстансы — chunk 10+ (discovery идёт отдельным DiscoveryReport)
		Capabilities:    nil,
	}}}); err != nil {
		return fmt.Errorf("отправка Hello: %w", err)
	}

	// Первым от сервера ждём HelloAck.
	first, err := stream.Recv()
	if err != nil {
		return fmt.Errorf("ожидание HelloAck: %w", err)
	}
	ack := first.GetHelloAck()
	if ack == nil {
		return fmt.Errorf("первое сообщение сервера — не HelloAck (%T)", first.GetPayload())
	}
	log.Info("сессия установлена",
		"session_id", ack.GetSessionId(), "server_version", ack.GetServerVersion(),
		"heartbeat_s", ack.GetHeartbeatIntervalSeconds(), "log_level", ack.GetLogLevel(),
		"capabilities", ack.GetConfig().GetCapabilities())
	applyLogLevel(levelVar, ack.GetLogLevel(), log)

	// Исполнитель задач сервера (chunk 11): capability из HelloAck.Config,
	// журнал обработанных task_id в data_dir (идемпотентность).
	exec := newTaskExecutor(cfg.DataDir, ack.GetConfig().GetCapabilities(), send, log)

	// Смещение часов относительно сервера (по SentAt HelloAck, без поправки
	// на RTT — грубая оценка для детекта заметного рассинхрона).
	var clockOffsetMs atomic.Int64
	if sent := first.GetSentAt(); sent != nil {
		clockOffsetMs.Store(sent.AsTime().Sub(time.Now()).Milliseconds())
	}

	// Discovery существующей установки Suricata (ТЗ п.4): один раз за сессию,
	// асинхронно — вызовы --build-info/systemctl не должны задерживать старт
	// heartbeat'ов. Отчёт кэшируется: heartbeat использует версию и юниты.
	var disc atomic.Pointer[agentv1.DiscoveryReport]
	wg0 := sync.WaitGroup{}
	wg0.Add(1)
	go func() {
		defer wg0.Done()
		rep := discoverSuricata(log)
		if rep == nil {
			return
		}
		disc.Store(rep)
		err := send(&agentv1.AgentMessage{Payload: &agentv1.AgentMessage_DiscoveryReport{DiscoveryReport: rep}})
		if err != nil {
			log.Warn("DiscoveryReport не отправлен", "err", err)
		}
	}()
	defer wg0.Wait()

	// Heartbeat-горутина.
	sampler := &resourceSampler{}
	hbInterval := time.Duration(ack.GetHeartbeatIntervalSeconds()) * time.Second
	if hbInterval <= 0 {
		hbInterval = 30 * time.Second
	}
	hbCtx, hbStop := context.WithCancel(ctx)
	defer hbStop()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(hbInterval)
		defer t.Stop()
		for {
			select {
			case <-hbCtx.Done():
				return
			case <-t.C:
				// Данные из кэша discovery (если уже завершён): версия,
				// диск с логами первого инстанса, статусы systemd-юнитов.
				var surVer, diskPath string
				var svcStatuses []*agentv1.InstanceServiceStatus
				if rep := disc.Load(); rep != nil {
					surVer = rep.GetBinary().GetVersion()
					if inst := rep.GetInstances(); len(inst) > 0 {
						diskPath = inst[0].GetLogDir()
						for _, in := range inst {
							if st, pid := unitStatus(in.GetSystemdUnit()); st != "" {
								svcStatuses = append(svcStatuses, &agentv1.InstanceServiceStatus{
									InstanceId: "", // серверный id появится после confirm (chunk 10+)
									State:      normalizeUnitState(st),
									Pid:        pid,
								})
							}
						}
					}
				}
				hb := &agentv1.AgentMessage{Payload: &agentv1.AgentMessage_Heartbeat{Heartbeat: &agentv1.Heartbeat{
					AgentId:         id.agentID,
					UptimeSeconds:   int64(time.Since(startedAt).Seconds()),
					AgentVersion:    version,
					SuricataVersion: surVer,
					Resources:       sampler.sample(diskPath),
					ClockOffsetMs:   clockOffsetMs.Load(),
					Instances:       svcStatuses,
				}}}
				if err := send(hb); err != nil {
					log.Warn("heartbeat не отправлен", "err", err)
					return
				}
			}
		}
	}()
	defer wg.Wait()

	// Приём серверных сообщений до разрыва.
	for {
		msg, err := stream.Recv()
		if err != nil {
			hbStop()
			if err == io.EOF {
				return fmt.Errorf("сервер закрыл стрим")
			}
			return fmt.Errorf("разрыв стрима: %w", err)
		}
		handleServerMessage(msg, exec, levelVar, log)
	}
}

// handleServerMessage — разбор одного серверного сообщения.
func handleServerMessage(msg *agentv1.ServerMessage, exec *taskExecutor, levelVar *slog.LevelVar, log *slog.Logger) {
	switch p := msg.GetPayload().(type) {
	case *agentv1.ServerMessage_Task:
		exec.handle(p.Task)

	case *agentv1.ServerMessage_LogLevelChange:
		applyLogLevel(levelVar, p.LogLevelChange.GetLevel(), log)

	case *agentv1.ServerMessage_ConfigPush:
		log.Info("ConfigPush", "config", p.ConfigPush.GetConfig())
		// TODO(chunk 10+): применение интервалов/ротации на лету.

	case *agentv1.ServerMessage_TaskCancel:
		log.Info("TaskCancel", "task_id", p.TaskCancel.GetTaskId(), "reason", p.TaskCancel.GetReason())

	default:
		log.Debug("сообщение сервера", "type", fmt.Sprintf("%T", p), "msg_id", msg.GetMsgId())
	}
}

// applyLogLevel меняет уровень логирования на лету (по команде сервера).
func applyLogLevel(levelVar *slog.LevelVar, level string, log *slog.Logger) {
	lv, err := config.ParseLogLevel(level)
	if err != nil {
		log.Warn("неизвестный log_level от сервера", "level", level)
		return
	}
	if levelVar.Level() != lv {
		levelVar.Set(lv)
		log.Info("уровень логирования изменён сервером", "level", level)
	}
}

// osHostname — обёртка для тестируемости.
var osHostname = func() (string, error) { return os.Hostname() }
