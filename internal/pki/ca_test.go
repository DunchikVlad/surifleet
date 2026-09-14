package pki

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// makeCSR генерирует ключ агента и CSR (PEM) для тестов.
func makeCSR(t *testing.T, cn string) (csrPEM []byte, key *ecdsa.PrivateKey) {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("генерация ключа: %v", err)
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: cn},
	}, k)
	if err != nil {
		t.Fatalf("создание CSR: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}), k
}

func TestLoadOrCreateCA(t *testing.T) {
	dir := t.TempDir()
	ca1, err := LoadOrCreateCA(dir)
	if err != nil {
		t.Fatalf("первое создание: %v", err)
	}
	if !ca1.Cert.IsCA {
		t.Fatal("сертификат должен быть CA")
	}
	// Персистентность между «рестартами».
	ca2, err := LoadOrCreateCA(dir)
	if err != nil {
		t.Fatalf("повторная загрузка: %v", err)
	}
	if !ca1.Cert.Equal(ca2.Cert) {
		t.Fatal("после перезапуска должен загружаться тот же CA")
	}
	// Права на ключ (проверка только на POSIX — Windows игнорирует 0600/0700).
	if runtime.GOOS != "windows" {
		st, err := os.Stat(filepath.Join(dir, caKeyFile))
		if err != nil {
			t.Fatalf("stat ключа: %v", err)
		}
		if perm := st.Mode().Perm(); perm != 0o600 {
			t.Errorf("права ключа CA: хочу 0600, получил %o", perm)
		}
		st, err = os.Stat(dir)
		if err != nil {
			t.Fatalf("stat каталога: %v", err)
		}
		if perm := st.Mode().Perm(); perm != 0o700 {
			t.Errorf("права ca_dir: хочу 0700, получил %o", perm)
		}
	}
}

func TestSignCSRAndVerify(t *testing.T) {
	ca, err := LoadOrCreateCA(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	agentID := "11111111-2222-3333-4444-555555555555"
	csrPEM, _ := makeCSR(t, "ignored-cn")
	certPEM, serial, err := ca.SignCSR(csrPEM, agentID, 0)
	if err != nil {
		t.Fatalf("SignCSR: %v", err)
	}
	if serial == "" {
		t.Error("серийный номер не возвращён")
	}
	cert, err := ca.VerifyCert(certPEM)
	if err != nil {
		t.Fatalf("VerifyCert: %v", err)
	}
	if cert.Subject.CommonName != agentID {
		t.Errorf("CN: хочу %q, получил %q", agentID, cert.Subject.CommonName)
	}
	if time.Until(cert.NotAfter) < 89*24*time.Hour {
		t.Errorf("срок сертификата подозрительно короток: до %v", cert.NotAfter)
	}
}

func TestSignCSRRejectsGarbage(t *testing.T) {
	ca, err := LoadOrCreateCA(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]byte{nil, []byte(""), []byte("not pem"),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("x")})} {
		if _, _, err := ca.SignCSR(bad, "id", 0); err == nil {
			t.Errorf("мусорный CSR %q должен отклоняться", bad)
		}
	}
}

func TestForeignCertRejected(t *testing.T) {
	ca, err := LoadOrCreateCA(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Другой, «чужой» CA подписывает сертификат — наш CA должен его отвергнуть.
	foreign, err := LoadOrCreateCA(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	csrPEM, _ := makeCSR(t, "attacker")
	certPEM, _, err := foreign.SignCSR(csrPEM, "attacker", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ca.VerifyCert(certPEM); err == nil {
		t.Fatal("чужой сертификат должен отвергаться")
	}
}

func TestIssueServerCert(t *testing.T) {
	ca, err := LoadOrCreateCA(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	certPEM, keyPEM, err := ca.IssueServerCert([]string{"localhost", "127.0.0.1", "192.168.31.28"})
	if err != nil {
		t.Fatalf("IssueServerCert: %v", err)
	}
	// Повторный вызов — тот же сертификат (персистентность).
	cert2, _, err := ca.IssueServerCert([]string{"other"})
	if err != nil {
		t.Fatal(err)
	}
	if string(certPEM) != string(cert2) {
		t.Error("серверный сертификат должен переиспользоваться между вызовами")
	}
	// Сертификат валиден как серверный от нашего CA.
	if _, err := ca.HubServerTLSConfig(certPEM, keyPEM); err != nil {
		t.Fatalf("HubServerTLSConfig: %v", err)
	}
	if _, err := ca.EnrollmentTLSConfig(certPEM, keyPEM); err != nil {
		t.Fatalf("EnrollmentTLSConfig: %v", err)
	}
	block, _ := pem.Decode(certPEM)
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ip := range cert.IPAddresses {
		if ip.String() == "192.168.31.28" {
			found = true
		}
	}
	if !found {
		t.Errorf("IP SAN 192.168.31.28 не найден в %v", cert.IPAddresses)
	}
}
