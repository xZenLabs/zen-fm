// Package tlsutil manages ZenFM's persistent per-device certificate.
package tlsutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
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
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/xZenLabs/zen-fm/internal/platform"
)

const managedMarkerVersion = "zenfm-managed-v1"

var (
	chmodCertificatePath = os.Chmod
	chmodCertificateFile = func(file *os.File, mode os.FileMode) error { return file.Chmod(mode) }
)

type Options struct {
	ModeLessFilesystem bool
}

// Manager serves a certificate and refreshes a ZenFM-managed certificate when
// a DHCP/interface change introduces a LAN address absent from its IP SANs.
// Reissuance keeps the same private key, so the displayed public-key
// fingerprint remains stable. Pairs without a valid managed marker are treated
// as administrator-supplied and are never modified.
type Manager struct {
	mu          sync.Mutex
	certFile    string
	keyFile     string
	hosts       []string
	addresses   func() []net.IP
	now         func() time.Time
	options     Options
	managed     bool
	certificate tls.Certificate
	leaf        *x509.Certificate
}

// NewManager ensures and loads a certificate suitable for use as a
// tls.Config.GetCertificate callback.
func NewManager(certFile, keyFile string, hosts []string) (*Manager, string, error) {
	return newManager(certFile, keyFile, hosts, interfaceIPs, time.Now)
}

// NewManagerWithOptions permits callers on explicitly mode-less filesystems to
// continue when chmod reports a permission error.
func NewManagerWithOptions(certFile, keyFile string, hosts []string, options Options) (*Manager, string, error) {
	return newManagerWithOptions(certFile, keyFile, hosts, interfaceIPs, time.Now, options)
}

// Ensure preserves the original fingerprint-only API for callers that do not
// need live DHCP refresh.
func Ensure(certFile, keyFile string, hosts []string) (string, error) {
	_, fingerprint, err := NewManager(certFile, keyFile, hosts)
	return fingerprint, err
}

func newManager(certFile, keyFile string, hosts []string, addresses func() []net.IP, now func() time.Time) (*Manager, string, error) {
	return newManagerWithOptions(certFile, keyFile, hosts, addresses, now, Options{})
}

func newManagerWithOptions(certFile, keyFile string, hosts []string, addresses func() []net.IP, now func() time.Time, options Options) (*Manager, string, error) {
	if certFile == "" || keyFile == "" || certFile == keyFile {
		return nil, "", errors.New("invalid certificate paths")
	}
	m := &Manager{certFile: certFile, keyFile: keyFile, hosts: append([]string(nil), hosts...), addresses: addresses, now: now, options: options}
	certExists, keyExists := fileExists(certFile), fileExists(keyFile)
	if certExists != keyExists {
		return nil, "", errors.New("certificate and key must both exist or both be absent")
	}
	if !certExists {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, "", err
		}
		if err := writePairWithOptions(certFile, keyFile, key, hosts, addresses(), now(), options); err != nil {
			return nil, "", err
		}
		fingerprint, err := Fingerprint(certFile)
		if err != nil {
			return nil, "", err
		}
		if err := atomicWriteWithOptions(markerPath(certFile), []byte(managedMarkerVersion+" "+fingerprint+"\n"), 0o600, options); err != nil {
			return nil, "", fmt.Errorf("write managed marker: %w", err)
		}
	}
	if err := m.load(); err != nil {
		return nil, "", err
	}
	fingerprint := certificateFingerprint(m.leaf)
	m.managed = validManagedMarker(certFile, fingerprint)
	if m.managed {
		if err := platform.ModeChangeError(chmodCertificatePath(certFile, 0o600), options.ModeLessFilesystem); err != nil {
			return nil, "", err
		}
		if err := platform.ModeChangeError(chmodCertificatePath(keyFile, 0o600), options.ModeLessFilesystem); err != nil {
			return nil, "", err
		}
		if err := m.refreshLocked(); err != nil {
			return nil, "", err
		}
		fingerprint = certificateFingerprint(m.leaf)
	}
	return m, fingerprint, nil
}

// GetCertificate refreshes managed SANs before each handshake. Interface
// enumeration is small on KOReader devices and avoids a stale-DHCP window.
func (m *Manager) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.refreshLocked(); err != nil {
		return nil, err
	}
	return &m.certificate, nil
}

func (m *Manager) refreshLocked() error {
	if !m.managed {
		return nil
	}
	addresses := normalizedIPs(m.addresses())
	if certificateCovers(m.leaf, addresses) && m.now().Before(m.leaf.NotAfter) && !m.now().Before(m.leaf.NotBefore) {
		return nil
	}
	if err := writePairWithOptions(m.certFile, m.keyFile, m.certificate.PrivateKey, m.hosts, addresses, m.now(), m.options); err != nil {
		return err
	}
	return m.load()
}

func (m *Manager) load() error {
	pair, err := tls.LoadX509KeyPair(m.certFile, m.keyFile)
	if err != nil {
		return err
	}
	if len(pair.Certificate) == 0 {
		return errors.New("certificate chain is empty")
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return err
	}
	pair.Leaf = leaf
	m.certificate, m.leaf = pair, leaf
	return nil
}

func Fingerprint(certFile string) (string, error) {
	data, err := os.ReadFile(certFile)
	if err != nil {
		return "", err
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return "", errors.New("invalid certificate PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", err
	}
	return certificateFingerprint(cert), nil
}

func certificateFingerprint(cert *x509.Certificate) string {
	digest := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return strings.ToUpper(hex.EncodeToString(digest[:]))
}

func writePair(certFile, keyFile string, privateKey any, hosts []string, addresses []net.IP, now time.Time) error {
	return writePairWithOptions(certFile, keyFile, privateKey, hosts, addresses, now, Options{})
}

func writePairWithOptions(certFile, keyFile string, privateKey any, hosts []string, addresses []net.IP, now time.Time, options Options) error {
	if err := os.MkdirAll(filepath.Dir(certFile), 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(keyFile), 0o700); err != nil {
		return err
	}
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return err
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "ZenFM Local Device"},
		NotBefore:    now.Add(-time.Hour), NotAfter: now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	addHost(template, "localhost")
	addHost(template, "127.0.0.1")
	addHost(template, "::1")
	if hostname, err := os.Hostname(); err == nil {
		addHost(template, hostname)
	}
	for _, host := range hosts {
		if parsed, _, err := net.SplitHostPort(host); err == nil {
			host = parsed
		}
		addHost(template, host)
	}
	for _, address := range normalizedIPs(addresses) {
		addHost(template, address.String())
	}
	publicKey, err := publicKey(privateKey)
	if err != nil {
		return err
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, publicKey, privateKey)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	keyAlreadyPresent := fileExists(keyFile)
	if err := atomicWriteWithOptions(certFile, certPEM, 0o600, options); err != nil {
		return fmt.Errorf("write certificate: %w", err)
	}
	if !keyAlreadyPresent {
		if err := atomicWriteWithOptions(keyFile, keyPEM, 0o600, options); err != nil {
			_ = os.Remove(certFile)
			return fmt.Errorf("write key: %w", err)
		}
	}
	return nil
}

func publicKey(privateKey any) (any, error) {
	switch key := privateKey.(type) {
	case *ecdsa.PrivateKey:
		return &key.PublicKey, nil
	default:
		return nil, errors.New("managed private key type is unsupported")
	}
}

func certificateCovers(cert *x509.Certificate, addresses []net.IP) bool {
	for _, required := range normalizedIPs(addresses) {
		found := false
		for _, present := range cert.IPAddresses {
			if present.Equal(required) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func interfaceIPs() []net.IP {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	values := make([]net.IP, 0)
	for _, networkInterface := range interfaces {
		if networkInterface.Flags&net.FlagUp == 0 || networkInterface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, err := networkInterface.Addrs()
		if err != nil {
			continue
		}
		for _, address := range addresses {
			value := strings.SplitN(address.String(), "/", 2)[0]
			if zone := strings.LastIndexByte(value, '%'); zone >= 0 {
				value = value[:zone]
			}
			ip := net.ParseIP(value)
			if ip != nil && ip.IsGlobalUnicast() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() {
				values = append(values, ip)
			}
		}
	}
	return normalizedIPs(values)
}

func normalizedIPs(values []net.IP) []net.IP {
	unique := make(map[string]net.IP)
	for _, value := range values {
		if value == nil || value.IsUnspecified() || value.IsMulticast() {
			continue
		}
		canonical := value.To16()
		if v4 := value.To4(); v4 != nil {
			canonical = v4
		}
		if canonical != nil {
			unique[canonical.String()] = append(net.IP(nil), canonical...)
		}
	}
	keys := make([]string, 0, len(unique))
	for key := range unique {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]net.IP, 0, len(keys))
	for _, key := range keys {
		result = append(result, unique[key])
	}
	return result
}

func addHost(cert *x509.Certificate, host string) {
	host = strings.TrimSpace(strings.Trim(host, "[]"))
	if host == "" || host == "0.0.0.0" || host == "::" {
		return
	}
	if ip := net.ParseIP(host); ip != nil {
		for _, present := range cert.IPAddresses {
			if present.Equal(ip) {
				return
			}
		}
		cert.IPAddresses = append(cert.IPAddresses, ip)
	} else if !strings.ContainsAny(host, " /\\\x00") {
		cert.DNSNames = append(cert.DNSNames, host)
	}
}

func markerPath(certFile string) string { return certFile + ".zenfm-managed" }

func validManagedMarker(certFile, fingerprint string) bool {
	data, err := os.ReadFile(markerPath(certFile))
	return err == nil && strings.TrimSpace(string(data)) == managedMarkerVersion+" "+fingerprint
}

func atomicWrite(name string, data []byte, mode os.FileMode) error {
	return atomicWriteWithOptions(name, data, mode, Options{})
}

func atomicWriteWithOptions(name string, data []byte, mode os.FileMode, options Options) error {
	if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(name), ".zenfm-cert-")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		_ = tmp.Close()
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()
	if err := platform.ModeChangeError(chmodCertificateFile(tmp, mode), options.ModeLessFilesystem); err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, name); err != nil {
		return err
	}
	ok = true
	return nil
}

func fileExists(name string) bool {
	info, err := os.Stat(name)
	return err == nil && info.Mode().IsRegular()
}
