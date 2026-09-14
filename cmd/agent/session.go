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
		Instances:       nil, // discovery инстансов Suricata — chunk 9
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
		"heartbeat_s", ack.GetHeartbeatIntervalSeconds(), "log_level", ack.GetLogLevel())
	applyLogLevel(levelVar, ack.GetLogLevel(), log)

	// Heartbeat-горутина.
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
				hb := &agentv1.AgentMessage{Payload: &agentv1.AgentMessage_Heartbeat{Heartbeat: &agentv1.Heartbeat{
					AgentId:       id.agentID,
					UptimeSeconds: int64(time.Since(startedAt).Seconds()),
					AgentVersion:  version,
					// TODO(chunk 9): resources, статусы инстансов, clock_offset.
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
		handleServerMessage(msg, send, levelVar, log)
	}
}

// handleServerMessage — разбор одного серверного сообщения.
func handleServerMessage(msg *agentv1.ServerMessage, send func(*agentv1.AgentMessage) error, levelVar *slog.LevelVar, log *slog.Logger) {
	switch p := msg.GetPayload().(type) {
	case *agentv1.ServerMessage_Task:
		task := p.Task
		log.Info("получена задача", "task_id", task.GetTaskId(), "type", fmt.Sprintf("%T", task.GetType()))
		// TODO(chunk 10+): реальное выполнение задач. Пока — честный отказ,
		// чтобы оркестратор не ждал таймаутом.
		res := &agentv1.AgentMessage{Payload: &agentv1.AgentMessage_TaskResult{TaskResult: &agentv1.TaskResult{
			TaskId: task.GetTaskId(),
			Status: agentv1.TaskStatus_TASK_STATUS_FAILED,
			Error:  "not implemented",
		}}}
		if err := send(res); err != nil {
			log.Warn("TaskResult не отправлен", "task_id", task.GetTaskId(), "err", err)
		}

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
