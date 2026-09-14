// Package pki — встроенный CA сервера SuriFleet (MVP, docs/architecture.md §10).
//
// Самоподписанный root CA (ECDSA P-256) генерируется при первом старте
// сервера и хранится в ca_dir (права 0700, закрытый ключ 0600), переживает
// рестарты. CA подписывает: серверный сертификат Hub/Enrollment (serverAuth)
// и клиентские сертификаты агентов (clientAuth, CN = agent_id, 90 дней).
//
// TODO(security): интерфейс внешнего PKI/Vault — после MVP.
package pki

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// Имена файлов CA и серверного сертификата внутри ca_dir.
const (
	caCertFile   = "ca.crt.pem"
	caKeyFile    = "ca.key.pem"
	srvCertFile  = "server.crt.pem"
	srvKeyFile   = "server.key.pem"
	caCommonName = "SuriFleet Root CA"
	// caTTL — срок жизни root CA (10 лет); serverTTL — серверного сертификата.
	caTTL     = 10 * 365 * 24 * time.Hour
	serverTTL = 365 * 24 * time.Hour
	// AgentCertTTL — срок клиентского сертификата агента (90 дней, §10).
	AgentCertTTL = 90 * 24 * time.Hour
)

// CA — корневой центр сертификации сервера.
type CA struct {
	Cert    *x509.Certificate
	Key     *ecdsa.PrivateKey
	CertPEM []byte
	dir     string
}

// LoadOrCreateCA загружает CA из dir либо создаёт новый (первый старт).
// Каталог создаётся с правами 0700, ключ хранится с 0600.
func LoadOrCreateCA(dir string) (*CA, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("создание ca_dir: %w", err)
	}
	certPath := filepath.Join(dir, caCertFile)
	keyPath := filepath.Join(dir, caKeyFile)

	certPEM, certErr := os.ReadFile(certPath)
	keyPEM, keyErr := os.ReadFile(keyPath)
	switch {
	case certErr == nil && keyErr == nil:
		ca, err := parseCA(certPEM, keyPEM)
		if err != nil {
			return nil, fmt.Errorf("загрузка CA из %s: %w", dir, err)
		}
		ca.dir = dir
		return ca, nil
	case errors.Is(certErr, os.ErrNotExist) && errors.Is(keyErr, os.ErrNotExist):
		return createCA(dir, certPath, keyPath)
	default:
		return nil, fmt.Errorf("частично отсутствует CA в %s (cert: %v, key: %v) — восстановите пару файлов", dir, certErr, keyErr)
	}
}

// createCA генерирует новый root CA и сохраняет его атомарно.
func createCA(dir, certPath, keyPath string) (*CA, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("генерация ключа CA: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	tpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: caCommonName, Organization: []string{"SuriFleet"}},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(caTTL),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("создание сертификата CA: %w", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("маршалинг ключа CA: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return nil, fmt.Errorf("запись ключа CA: %w", err)
	}
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		return nil, fmt.Errorf("запись сертификата CA: %w", err)
	}
	return parseCA(certPEM, keyPEM)
}

// parseCA разбирает PEM-пару сертификат+ключ.
func parseCA(certPEM, keyPEM []byte) (*CA, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, errors.New("некорректный PEM сертификата CA")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("разбор сертификата CA: %w", err)
	}
	kb, _ := pem.Decode(keyPEM)
	if kb == nil {
		return nil, errors.New("некорректный PEM ключа CA")
	}
	key, err := x509.ParseECPrivateKey(kb.Bytes)
	if err != nil {
		return nil, fmt.Errorf("разбор ключа CA (ожидается ECDSA P-256): %w", err)
	}
	return &CA{Cert: cert, Key: key, CertPEM: certPEM}, nil
}

// randomSerial — криптостойкий 128-битный серийный номер.
func randomSerial() (*big.Int, error) {
	n := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, n)
	if err != nil {
		return nil, fmt.Errorf("генерация серийного номера: %w", err)
	}
	return serial, nil
}

// SignCSR подписывает PEM-кодированный PKCS#10 CSR агента: выдаёт
// клиентский сертификат с CN = agentID (CN из CSR игнорируется, §10),
// ExtKeyUsage clientAuth, срок — AgentCertTTL.
// Возвращает PEM сертификата и серийный номер (hex) для agents.cert_serial.
func (ca *CA) SignCSR(csrPEM []byte, agentID string, ttl time.Duration) (certPEM []byte, serial string, err error) {
	if ttl <= 0 {
		ttl = AgentCertTTL
	}
	block, _ := pem.Decode(csrPEM)
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		return nil, "", errors.New("некорректный PEM: ожидается CERTIFICATE REQUEST")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, "", fmt.Errorf("разбор CSR: %w", err)
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, "", fmt.Errorf("подпись CSR невалидна: %w", err)
	}

	sn, err := randomSerial()
	if err != nil {
		return nil, "", err
	}
	now := time.Now()
	tpl := &x509.Certificate{
		SerialNumber: sn,
		Subject:      pkix.Name{CommonName: agentID, Organization: []string{"SuriFleet Agent"}},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(ttl),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, ca.Cert, csr.PublicKey, ca.Key)
	if err != nil {
		return nil, "", fmt.Errorf("подпись CSR: %w", err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	sum := sha256.Sum256(sn.Bytes())
	return certPEM, hex.EncodeToString(sum[:8]), nil
}

// IssueServerCert выдаёт (или возвращает сохранённый) серверный сертификат
// для слушателей Hub/Enrollment: ExtKeyUsage serverAuth, SAN из sans
// (IP-строки распознаются как IP SAN, прочее — DNS SAN).
func (ca *CA) IssueServerCert(sans []string) (certPEM, keyPEM []byte, err error) {
	certPath := filepath.Join(ca.dir, srvCertFile)
	keyPath := filepath.Join(ca.dir, srvKeyFile)
	if c, err1 := os.ReadFile(certPath); err1 == nil {
		if k, err2 := os.ReadFile(keyPath); err2 == nil {
			return c, k, nil
		}
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("генерация серверного ключа: %w", err)
	}
	sn, err := randomSerial()
	if err != nil {
		return nil, nil, err
	}
	tpl := &x509.Certificate{
		SerialNumber: sn,
		Subject:      pkix.Name{CommonName: "surifleet-server", Organization: []string{"SuriFleet"}},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(serverTTL),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, san := range sans {
		if ip := net.ParseIP(san); ip != nil {
			tpl.IPAddresses = append(tpl.IPAddresses, ip)
		} else {
			tpl.DNSNames = append(tpl.DNSNames, san)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, ca.Cert, &key.PublicKey, ca.Key)
	if err != nil {
		return nil, nil, fmt.Errorf("подпись серверного сертификата: %w", err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, fmt.Errorf("маршалинг серверного ключа: %w", err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return nil, nil, fmt.Errorf("запись серверного ключа: %w", err)
	}
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		return nil, nil, fmt.Errorf("запись серверного сертификата: %w", err)
	}
	return certPEM, keyPEM, nil
}

// VerifyCert проверяет сертификат по цепочке этого CA (clientAuth).
func (ca *CA) VerifyCert(certPEM []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, errors.New("некорректный PEM сертификата")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("разбор сертификата: %w", err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca.Cert)
	if _, err := cert.Verify(x509.VerifyOptions{
		Roots:     roots,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}); err != nil {
		return nil, fmt.Errorf("проверка цепочки: %w", err)
	}
	return cert, nil
}
