package tlsutil

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestEnsureCertificate(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := filepath.Join(dir, "tls", "cert.pem"), filepath.Join(dir, "tls", "key.pem")
	fingerprint, err := Ensure(certFile, keyFile, []string{"192.0.2.10:8443"})
	if err != nil {
		t.Fatal(err)
	}
	if len(fingerprint) != 64 {
		t.Fatalf("fingerprint = %q", fingerprint)
	}
	if _, err := tls.LoadX509KeyPair(certFile, keyFile); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{certFile, keyFile} {
		info, err := os.Stat(name)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("mode for %s: %v %v", name, info.Mode().Perm(), err)
		}
	}
	again, err := Ensure(certFile, keyFile, nil)
	if err != nil || again != fingerprint {
		t.Fatalf("certificate not persistent: %q %v", again, err)
	}
}

func TestCertificateChmodIsOptionalOnlyForModeLessFilesystem(t *testing.T) {
	originalPath := chmodCertificatePath
	originalFile := chmodCertificateFile
	pathCalls, fileCalls := 0, 0
	chmodCertificatePath = func(string, os.FileMode) error {
		pathCalls++
		return syscall.EPERM
	}
	chmodCertificateFile = func(*os.File, os.FileMode) error {
		fileCalls++
		return syscall.EPERM
	}
	t.Cleanup(func() {
		chmodCertificatePath = originalPath
		chmodCertificateFile = originalFile
	})

	now := time.Unix(1_700_000_000, 0)
	dir := t.TempDir()
	manager, fingerprint, err := newManagerWithOptions(
		filepath.Join(dir, "portable-cert.pem"), filepath.Join(dir, "portable-key.pem"), nil,
		func() []net.IP { return nil }, func() time.Time { return now }, Options{ModeLessFilesystem: true},
	)
	if err != nil || manager == nil || len(fingerprint) != 64 {
		t.Fatalf("mode-less filesystem failed to create certificate: %v, fingerprint %q", err, fingerprint)
	}
	if pathCalls == 0 || fileCalls == 0 {
		t.Fatalf("chmod paths were not both exercised: paths %d, temporary files %d", pathCalls, fileCalls)
	}

	strictDir := t.TempDir()
	_, _, err = newManagerWithOptions(
		filepath.Join(strictDir, "cert.pem"), filepath.Join(strictDir, "key.pem"), nil,
		func() []net.IP { return nil }, func() time.Time { return now }, Options{},
	)
	if !errors.Is(err, syscall.EPERM) {
		t.Fatalf("strict mode did not preserve chmod failure: %v", err)
	}
}

func TestManagedCertificateRefreshesDHCPAddressWithoutChangingFingerprint(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	now := time.Unix(1_700_000_000, 0)
	addresses := []net.IP{net.ParseIP("192.0.2.10")}
	manager, fingerprint, err := newManager(certFile, keyFile, nil, func() []net.IP { return addresses }, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(certFile)
	if err != nil {
		t.Fatal(err)
	}
	if !certificateHasIP(t, before, net.ParseIP("192.0.2.10")) {
		t.Fatal("initial LAN address missing from certificate")
	}
	addresses = []net.IP{net.ParseIP("192.0.2.11")}
	if _, err := manager.GetCertificate(nil); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(certFile)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(before, after) || !certificateHasIP(t, after, net.ParseIP("192.0.2.11")) {
		t.Fatal("managed certificate was not reissued for DHCP address")
	}
	refreshedFingerprint, err := Fingerprint(certFile)
	if err != nil || refreshedFingerprint != fingerprint {
		t.Fatalf("public-key fingerprint changed: %q -> %q (%v)", fingerprint, refreshedFingerprint, err)
	}
}

func TestAdministratorCertificateIsNeverModifiedForInterfaceChanges(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0)
	if err := writePair(certFile, keyFile, key, []string{"admin.example"}, []net.IP{net.ParseIP("192.0.2.20")}, now); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(certFile, 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(certFile)
	manager, _, err := newManager(certFile, keyFile, nil, func() []net.IP { return []net.IP{net.ParseIP("192.0.2.21")} }, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.GetCertificate(nil); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(certFile)
	info, _ := os.Stat(certFile)
	if !bytes.Equal(before, after) || info.Mode().Perm() != 0o644 {
		t.Fatal("administrator certificate content or mode was modified")
	}
}

func certificateHasIP(t *testing.T, data []byte, wanted net.IP) bool {
	t.Helper()
	block, _ := pem.Decode(data)
	if block == nil {
		t.Fatal("certificate PEM missing")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	for _, address := range certificate.IPAddresses {
		if address.Equal(wanted) {
			return true
		}
	}
	return false
}

func TestEnsureRejectsHalfPair(t *testing.T) {
	dir := t.TempDir()
	cert := filepath.Join(dir, "cert.pem")
	if err := os.WriteFile(cert, []byte("not replaced"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Ensure(cert, filepath.Join(dir, "key.pem"), nil); err == nil {
		t.Fatal("half pair accepted")
	}
	data, _ := os.ReadFile(cert)
	if string(data) != "not replaced" {
		t.Fatal("existing certificate overwritten")
	}
}
