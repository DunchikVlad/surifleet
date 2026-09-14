package pki

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
)

// Две TLS-схемы сервера (docs/architecture.md §4.1, §10):
//   - Hub (:8443) — строгий mTLS: клиентский сертификат обязателен и
//     проверяется по CA (CN сверяется с agent_id в Hello на уровне Hub).
//   - Enrollment (:8444) — обычный TLS: у агента ещё нет сертификата,
//     аутентификация — одноразовым join token внутри RPC.

// HubServerTLSConfig — mTLS-конфиг Hub: RequireAndVerifyClientCert по CA.
func (ca *CA) HubServerTLSConfig(certPEM, keyPEM []byte) (*tls.Config, error) {
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("серверный сертификат: %w", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca.Cert)
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    pool,
		MinVersion:   tls.VersionTLS12,
	}, nil
}

// EnrollmentTLSConfig — обычный TLS (без клиентского сертификата).
func (ca *CA) EnrollmentTLSConfig(certPEM, keyPEM []byte) (*tls.Config, error) {
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("серверный сертификат: %w", err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}, nil
}

// AgentIDFromPeer извлекает agent_id (CN) из проверенного клиентского
// сертификата mTLS-соединения. certs — peer.PeerCertificates из gRPC.
func AgentIDFromPeer(certs []*x509.Certificate) (string, error) {
	if len(certs) == 0 {
		return "", fmt.Errorf("нет клиентского сертификата")
	}
	cn := certs[0].Subject.CommonName
	if cn == "" {
		return "", fmt.Errorf("пустой CN клиентского сертификата")
	}
	return cn, nil
}
