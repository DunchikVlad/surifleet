package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
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
func runSession(ctx context.Context, cfg *config.AgentConfig, id *identity, levelVar *slog.LevelVar, log *slog.Logger, logBuf *logBuffer) error {
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

	// Привязка к зарегистрированным инстансам (chunk 12c): сервер сообщает
	// instance_id при подключении — агент знает их до первой задачи.
	// Сохраняем в data_dir/bound_instances.json для остальных компонентов.
	if bindings := ack.GetBoundInstances(); len(bindings) > 0 {
		for _, b := range bindings {
			log.Info("инстанс привязан", "instance_id", b.GetInstanceId(),
				"name", b.GetName(), "config", b.GetConfigPath())
		}
		if err := saveBoundInstances(cfg.DataDir, bindings); err != nil {
			log.Warn("сохранение bound_instances.json", "err", err)
		}
	} else {
		log.Info("привязанных инстансов нет (ожидается confirm_discovery)")
	}

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

	// Доставка логов агента на сервер (chunk 13c): каждые 30 с сливаем
	// очередь logBuf одним LogBatch. При ошибке отправки записи
	// возвращаются в голову очереди (уедут после переподключения).
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-hbCtx.Done():
				return
			case <-t.C:
				entries := logBuf.drain(logBufferMax)
				if len(entries) == 0 {
					continue
				}
				batch := &agentv1.LogBatch{Entries: make([]*agentv1.LogEntry, 0, len(entries))}
				for _, e := range entries {
					batch.Entries = append(batch.Entries, &agentv1.LogEntry{
						Seq:   e.seq,
						Ts:    timestamppb.New(e.ts),
						Level: e.level,
						Msg:   e.msg,
						Attrs: e.attrs,
					})
				}
				if err := send(&agentv1.AgentMessage{Payload: &agentv1.AgentMessage_LogBatch{LogBatch: batch}}); err != nil {
					logBuf.requeueFront(entries)
					log.Warn("LogBatch не отправлен", "entries", len(entries), "err", err)
				}
			}
		}
	}()

	// Доставка метрик агента на сервер (чанк 33): MetricsBatch каждые
	// metrics_interval (HelloAck, default 60 с): host.cpu_percent,
	// host.mem_bytes, host.disk_used_percent. Отдельный sampler (CPU%
	// считается между сэмплами — делить с heartbeat нельзя).
	metSampler := &resourceSampler{}
	metInterval := time.Duration(ack.GetMetricsIntervalSeconds()) * time.Second
	if metInterval <= 0 {
		metInterval = 60 * time.Second
	}
	// Метрики Suricata из eve.json (чанк 34): tail по log_dir первого
	// инстанса, instance_id — из привязок HelloAck.
	var eve *eveTailer
	bindings := ack.GetBoundInstances()
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(metInterval)
		defer t.Stop()
		for {
			select {
			case <-hbCtx.Done():
				return
			case <-t.C:
				var diskPath string
				if rep := disc.Load(); rep != nil {
					if inst := rep.GetInstances(); len(inst) > 0 {
						diskPath = inst[0].GetLogDir()
					}
				}
				sum := metSampler.sample(diskPath)
				now := timestamppb.Now()
				batch := &agentv1.MetricsBatch{Points: []*agentv1.MetricPoint{
					{Ts: now, Name: "host.cpu_percent", Value: sum.GetCpuPercent()},
					{Ts: now, Name: "host.mem_bytes", Value: float64(sum.GetMemBytes())},
					{Ts: now, Name: "host.disk_used_percent", Value: sum.GetDiskUsedPercent()},
				}}
				if diskPath != "" {
					if eve == nil {
						eve = &eveTailer{path: strings.TrimRight(diskPath, "/") + "/eve.json"}
					}
					batch.Points = append(batch.Points, eve.points(suricataInstanceID(bindings, diskPath))...)
				}
				if err := send(&agentv1.AgentMessage{Payload: &agentv1.AgentMessage_MetricsBatch{MetricsBatch: batch}}); err != nil {
					log.Warn("MetricsBatch не отправлен", "err", err)
					return
				}
			}
		}
	}()

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

// saveBoundInstances сохраняет привязку агент→инстансы из HelloAck в
// data_dir/bound_instances.json (атомарно): источник instance_id для
// компонентов агента вне задач деплоя.
func saveBoundInstances(dataDir string, bindings []*agentv1.InstanceBinding) error {
	type binding struct {
		InstanceID string `json:"instance_id"`
		Name       string `json:"name"`
		ConfigPath string `json:"config_path"`
		RulesDir   string `json:"rules_dir"`
		LogDir     string `json:"log_dir"`
	}
	out := make([]binding, 0, len(bindings))
	for _, b := range bindings {
		out = append(out, binding{
			InstanceID: b.GetInstanceId(),
			Name:       b.GetName(),
			ConfigPath: b.GetConfigPath(),
			RulesDir:   b.GetRulesDir(),
			LogDir:     b.GetLogDir(),
		})
	}
	raw, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	tmp := filepath.Join(dataDir, "bound_instances.json.tmp")
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dataDir, "bound_instances.json"))
}
