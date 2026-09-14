package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/surifleet/surifleet/internal/config"
	agentv1 "github.com/surifleet/surifleet/internal/gen/agent/v1"
)

// Имена файлов идентичности агента в data_dir.
const (
	agentCertFile = "agent.crt.pem"
	agentKeyFile  = "agent.key.pem"
	agentCAFile   = "ca.pem"
	agentIDFile   = "agent_id"
)

// identity — загруженная идентичность агента (ключи, сертификат, CA, id).
type identity struct {
	agentID  string
	certPEM  []byte
	keyPEM   []byte
	caPEM    []byte
	hubAddr  string
	certPool *x509.CertPool
}

// loadOrEnroll возвращает идентичность агента: загружает с диска либо
// проходит enrollment по join_token из конфига (первый запуск).
func loadOrEnroll(ctx context.Context, cfg *config.AgentConfig, log *slog.Logger) (*identity, error) {
	certPath := filepath.Join(cfg.DataDir, agentCertFile)
	keyPath := filepath.Join(cfg.DataDir, agentKeyFile)
	caPath := filepath.Join(cfg.DataDir, agentCAFile)
	idPath := filepath.Join(cfg.DataDir, agentIDFile)

	certPEM, certErr := os.ReadFile(certPath)
	keyPEM, keyErr := os.ReadFile(keyPath)
	caPEM, caErr := os.ReadFile(caPath)
	idRaw, idErr := os.ReadFile(idPath)
	if certErr == nil && keyErr == nil && caErr == nil && idErr == nil {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("некорректный CA в %s", caPath)
		}
		log.Info("идентичность загружена с диска", "agent_id", strings.TrimSpace(string(idRaw)))
		return &identity{
			agentID: strings.TrimSpace(string(idRaw)),
			certPEM: certPEM, keyPEM: keyPEM, caPEM: caPEM,
			hubAddr: cfg.ServerAddr, certPool: pool,
		}, nil
	}

	// Первого запуска нет сертификата — нужен join token.
	if cfg.JoinToken == "" {
		return nil, fmt.Errorf("нет сертификата агента в %s и не задан agent.join_token — enrollment невозможен", cfg.DataDir)
	}
	return enroll(ctx, cfg, log)
}

// enroll — первичная регистрация: ключи → CSR → Enroll → сохранение
// сертификата, CA и agent_id в data_dir.
//
// TODO(security): enrollment идёт по TLS с InsecureSkipVerify — у агента ещё
// нет CA для проверки сервера (проблема «курицы и яйца»); защита канала —
// одноразовость и TTL join token. В проде: TOFU-пиннинг CA или доставка
// отпечатка CA вместе с токеном.
func enroll(ctx context.Context, cfg *config.AgentConfig, log *slog.Logger) (*identity, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("генерация ключа агента: %w", err)
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: "pending"}, // CN игнорируется, сервер ставит agent_id
	}, key)
	if err != nil {
		return nil, fmt.Errorf("создание CSR: %w", err)
	}
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})

	hostname, _ := os.Hostname()
	meta := &agentv1.HostMeta{
		Hostname:     hostname,
		Os:           hostOS(),
		Arch:         runtime.GOARCH,
		IpAddresses:  localIPs(),
		AgentVersion: version,
	}

	log.Info("enrollment: запрос к серверу", "enroll_addr", cfg.EnrollAddr, "hostname", hostname)
	conn, err := grpc.NewClient(cfg.EnrollAddr,
		grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{InsecureSkipVerify: true}))) //nolint:gosec — см. TODO выше
	if err != nil {
		return nil, fmt.Errorf("подключение к enrollment %s: %w", cfg.EnrollAddr, err)
	}
	defer conn.Close()

	resp, err := agentv1.NewEnrollmentClient(conn).Enroll(ctx, &agentv1.EnrollRequest{
		JoinToken: cfg.JoinToken,
		Csr:       csrPEM,
		Host:      meta,
	})
	if err != nil {
		return nil, fmt.Errorf("Enroll: %w", err)
	}

	hubAddr := cfg.ServerAddr
	if eps := resp.GetHubEndpoints(); len(eps) > 0 {
		hubAddr = eps[0] // адрес Hub из ответа сервера приоритетнее дефолта конфига
	}

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("маршалинг ключа: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, err
	}
	// Секретный ключ — 0600; сертификат/CA/agent_id — не секреты.
	if err := os.WriteFile(filepath.Join(cfg.DataDir, agentKeyFile), keyPEM, 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(cfg.DataDir, agentCertFile), resp.GetCertificate(), 0o644); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(cfg.DataDir, agentCAFile), resp.GetCaChain(), 0o644); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(cfg.DataDir, agentIDFile), []byte(resp.GetAgentId()+"\n"), 0o644); err != nil {
		return nil, err
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(resp.GetCaChain()) {
		return nil, fmt.Errorf("некорректная ca_chain в ответе сервера")
	}
	log.Info("enrollment завершён", "agent_id", resp.GetAgentId(), "hub", hubAddr)
	return &identity{
		agentID:  resp.GetAgentId(),
		certPEM:  resp.GetCertificate(),
		keyPEM:   keyPEM,
		caPEM:    resp.GetCaChain(),
		hubAddr:  hubAddr,
		certPool: pool,
	}, nil
}

// hostOS — краткое описание ОС для карточки хоста (PRETTY_NAME из os-release).
func hostOS() string {
	data, err := os.ReadFile("/etc/os-release")
	if err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if v, ok := strings.CutPrefix(line, "PRETTY_NAME="); ok {
				return strings.Trim(v, `"`)
			}
		}
	}
	return runtime.GOOS
}

// localIPs — нелокальные адреса интерфейсов хоста.
func localIPs() []string {
	var out []string
	ifaces, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				continue
			}
			out = append(out, ip.String())
		}
	}
	return out
}

// bootID — уникальный ID загрузки ОС (для детекта ребута хоста).
func bootID() string {
	data, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
