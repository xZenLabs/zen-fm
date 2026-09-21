package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	zenfiles "github.com/xZenLabs/zen-fm/internal/files"
	"github.com/xZenLabs/zen-fm/internal/state"
)

type lockedBuffer struct {
	sync.Mutex
	bytes.Buffer
}

func (b *lockedBuffer) Write(value []byte) (int, error) {
	b.Lock()
	defer b.Unlock()
	return b.Buffer.Write(value)
}

func (b *lockedBuffer) String() string {
	b.Lock()
	defer b.Unlock()
	return b.Buffer.String()
}

func TestValidatePeerOfferRejectsUnsafeManifests(t *testing.T) {
	fingerprint := strings.Repeat("A", 64)
	valid := peerOfferRequest{
		Sender: peerIdentity{Name: "Reader", Fingerprint: fingerprint, Port: 54321},
		Item:   peerItem{Name: "Books", Type: "directory", Bytes: 3, FileCount: 1, EntryCount: 2},
		Entries: []peerManifestEntry{
			{ID: "zfm_file_00000000", Path: ".", Type: "directory"},
			{ID: "zfm_file_00000001", Path: "book.epub", Type: "file", Size: 3},
		},
	}
	if err := validatePeerOffer(valid); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*peerOfferRequest){
		"traversal":      func(value *peerOfferRequest) { value.Entries[1].Path = "../book.epub" },
		"duplicate":      func(value *peerOfferRequest) { value.Entries[1].Path = "." },
		"missing parent": func(value *peerOfferRequest) { value.Entries[1].Path = "missing/book.epub" },
		"summary":        func(value *peerOfferRequest) { value.Item.Bytes++ },
		"control name":   func(value *peerOfferRequest) { value.Item.Name = "bad\nname" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := valid
			candidate.Entries = append([]peerManifestEntry(nil), valid.Entries...)
			mutate(&candidate)
			if validatePeerOffer(candidate) == nil {
				t.Fatal("unsafe manifest was accepted")
			}
		})
	}
	fileWithChild := valid
	fileWithChild.Item = peerItem{Name: "book.epub", Type: "file", Bytes: 6, FileCount: 2, EntryCount: 2}
	fileWithChild.Entries = []peerManifestEntry{
		{ID: "zfm_file_00000000", Path: ".", Type: "file", Size: 3},
		{ID: "zfm_file_00000001", Path: "child", Type: "file", Size: 3},
	}
	if validatePeerOffer(fileWithChild) == nil {
		t.Fatal("file manifest with children was accepted")
	}
}

func TestPeerDiscoveryRejectsMalformedAndSelfAnnouncements(t *testing.T) {
	nonce := "0123456789abcdef"
	fingerprint := strings.Repeat("A", 64)
	valid := `{"service":"zenfm-peer","version":1,"type":"announce","nonce":"` + nonce +
		`","name":"Reader","fingerprint":"` + fingerprint + `","port":54321}`
	datagram, ok := decodePeerDatagram([]byte(valid))
	if !ok {
		t.Fatal("valid discovery datagram was rejected")
	}
	if _, ok := announcedPeer(datagram, "192.0.2.10", fingerprint); ok {
		t.Fatal("self announcement was accepted")
	}
	if device, ok := announcedPeer(datagram, "192.0.2.10", strings.Repeat("B", 64)); !ok || device.Address != "192.0.2.10" {
		t.Fatalf("announcement did not use source address: %+v, %v", device, ok)
	}
	for _, malformed := range []string{
		valid + `{}`,
		strings.Replace(valid, `"version":1`, `"version":2`, 1),
		strings.Replace(valid, `"port":54321`, `"port":54321,"extra":true`, 1),
		`{"service":"zenfm-peer"}`,
	} {
		if _, ok := decodePeerDatagram([]byte(malformed)); ok {
			t.Fatalf("malformed datagram accepted: %s", malformed)
		}
	}
}

func TestPeerDiscoveryListenerRespondsAndLogs(t *testing.T) {
	peer := newPeerTestServer(t, "Receiver", true)
	available, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero})
	if err != nil {
		t.Fatal(err)
	}
	port := available.LocalAddr().(*net.UDPAddr).Port
	_ = available.Close()
	var logs lockedBuffer
	peer.api.cfg.Logger = log.New(&logs, "", 0)
	peer.api.peer.startDiscovery(port)
	if peer.api.peer.udp == nil {
		t.Fatalf("discovery listener failed: %s", logs.String())
	}

	loopback := net.IPv4(127, 0, 0, 1)
	client, err := net.ListenUDP("udp4", &net.UDPAddr{IP: loopback})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	nonce := "0123456789abcdef"
	request, _ := json.Marshal(peerDatagram{Service: peerService, Version: peerProtocolVersion, Type: "discover", Nonce: nonce})
	if _, err := client.WriteToUDP(request, &net.UDPAddr{IP: loopback, Port: port}); err != nil {
		t.Fatal(err)
	}
	_ = client.SetReadDeadline(time.Now().Add(time.Second))
	buffer := make([]byte, 2048)
	length, _, err := client.ReadFromUDP(buffer)
	if err != nil {
		t.Fatal(err)
	}
	response, ok := decodePeerDatagram(buffer[:length])
	if !ok || response.Type != "announce" || response.Nonce != nonce || response.Name != "Receiver" {
		t.Fatalf("invalid discovery response: %+v", response)
	}
	if output := logs.String(); !strings.Contains(output, "peer discovery listening") ||
		!strings.Contains(output, "peer discovery response sent") {
		t.Fatalf("missing discovery diagnostics: %s", output)
	}
}

func TestPinnedPeerClientRejectsFingerprintSubstitutionAndRedirects(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	digest := sha256.Sum256(server.Certificate().RawSubjectPublicKeyInfo)
	fingerprint := strings.ToUpper(hex.EncodeToString(digest[:]))
	response, err := pinnedPeerClient(fingerprint).Get(server.URL)
	if err != nil || response.StatusCode != http.StatusNoContent {
		t.Fatalf("pinned request: %v, %v", response, err)
	}
	response.Body.Close()
	if _, err := pinnedPeerClient(strings.Repeat("0", 64)).Get(server.URL); err == nil {
		t.Fatal("substituted certificate was accepted")
	}
	response, err = pinnedPeerClient(fingerprint).Get(server.URL + "/redirect")
	if err != nil || response.StatusCode != http.StatusFound {
		t.Fatalf("redirect was followed or failed: %v, %v", response, err)
	}
	response.Body.Close()
}

type peerTestServer struct {
	api         *Server
	http        *httptest.Server
	root        *zenfiles.Root
	store       *state.Store
	rootPath    string
	events      string
	fingerprint string
}

func newPeerTestServer(t *testing.T, name string, setup bool, sourceFiles ...*zenfiles.Root) *peerTestServer {
	t.Helper()
	rootPath := t.TempDir()
	store, err := state.Open(filepath.Join(rootPath, ".state", "zenfm.db"), state.Options{PasswordParams: fastPassword})
	if err != nil {
		t.Fatal(err)
	}
	if setup {
		owner, err := store.Owner()
		if err != nil || store.ReplacePassword(owner.PasswordHash, false) != nil {
			t.Fatalf("complete setup: %v", err)
		}
	}
	root, err := zenfiles.Open(rootPath, zenfiles.Options{})
	if err != nil {
		t.Fatal(err)
	}
	var handler atomic.Value
	httpServer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		value := handler.Load()
		if value == nil {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		value.(http.Handler).ServeHTTP(w, r)
	}))
	httpServer.StartTLS()
	digest := sha256.Sum256(httpServer.Certificate().RawSubjectPublicKeyInfo)
	fingerprint := strings.ToUpper(hex.EncodeToString(digest[:]))
	events := filepath.Join(rootPath, ".state", "peer-events.json")
	config := Config{
		Store: store, Files: root, Version: "test", SecureTransport: true,
		PeerName: name, PeerFingerprint: fingerprint, PeerAddress: httpServer.Listener.Addr().String(), PeerEvents: events,
	}
	if len(sourceFiles) > 0 {
		config.PeerFiles = sourceFiles[0]
	}
	api, err := New(config)
	if err != nil {
		httpServer.Close()
		root.Close()
		store.Close()
		t.Fatal(err)
	}
	handler.Store(api.Handler())
	result := &peerTestServer{api: api, http: httpServer, root: root, store: store, rootPath: rootPath, events: events, fingerprint: fingerprint}
	t.Cleanup(func() {
		result.http.Close()
		result.api.Close()
		_ = result.root.Close()
		_ = result.store.Close()
	})
	return result
}

func TestPeerRoutesRequireCompletedSetup(t *testing.T) {
	peer := newPeerTestServer(t, "Receiver", false)
	request := httptest.NewRequest(http.MethodGet, "/api/peer/v1/info?challenge=abcdefghijklmnop", nil)
	request.RemoteAddr = "127.0.0.1:1234"
	response := httptest.NewRecorder()
	peer.api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("peer route before setup = %d", response.Code)
	}
}

func TestPeerDirectoryTransferIsAtomicAndCollisionSafe(t *testing.T) {
	sourcePath := t.TempDir()
	sourceFiles, err := zenfiles.Open(sourcePath, zenfiles.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sourceFiles.Close() })
	sender := newPeerTestServer(t, "Sender", true, sourceFiles)
	receiver := newPeerTestServer(t, "Receiver", true)
	if err := os.MkdirAll(filepath.Join(sourcePath, "Books", "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourcePath, "Books", "book.epub"), []byte("book-data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(receiver.rootPath, "ZenFM Received", "Books"), 0o755); err != nil {
		t.Fatal(err)
	}
	sender.api.peer.mu.Lock()
	sender.api.peer.peers[receiver.fingerprint] = peerDevice{
		peerIdentity: peerIdentity{Name: "Receiver", Fingerprint: receiver.fingerprint, Port: receiver.http.Listener.Addr().(*net.TCPAddr).Port},
		Address:      "127.0.0.1",
	}
	sender.api.peer.discoveryDeadline = time.Now().Add(time.Minute)
	sender.api.peer.mu.Unlock()
	jobID := "0123456789abcdef0123456789abcdef"
	var logs lockedBuffer
	sender.api.cfg.Logger = log.New(&logs, "", 0)
	selected := filepath.Join(sourcePath, "Books")
	encoded := base64.RawURLEncoding.EncodeToString([]byte(selected))
	if err := sender.api.PeerCommand("peer-send " + jobID + " " + receiver.fingerprint + " " + encoded); err != nil {
		t.Fatal(err)
	}
	if output := logs.String(); !strings.Contains(output, fmt.Sprintf("selected=%q", selected)) ||
		!strings.Contains(output, fmt.Sprintf("source-root=%q", sourceFiles.Name())) ||
		!strings.Contains(output, fmt.Sprintf("home=%q", sender.root.Name())) {
		t.Fatalf("source mapping was not logged: %s", output)
	}
	waitPeerStatus(t, sender.api.peer, "waiting")
	receiver.api.peer.mu.Lock()
	offerID := receiver.api.peer.incoming
	receiver.api.peer.mu.Unlock()
	if err := receiver.api.PeerCommand("peer-accept " + offerID); err != nil {
		t.Fatal(err)
	}
	waitPeerStatus(t, sender.api.peer, "complete")
	received := filepath.Join(receiver.rootPath, "ZenFM Received", "Books (1)")
	data, err := os.ReadFile(filepath.Join(received, "book.epub"))
	if err != nil || string(data) != "book-data" {
		t.Fatalf("received file = %q, %v", data, err)
	}
	if info, err := os.Stat(filepath.Join(received, "empty")); err != nil || !info.IsDir() {
		t.Fatalf("empty directory was not preserved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(receiver.rootPath, "ZenFM Received", "Books", "book.epub")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("existing destination was overwritten")
	}
	eventData, err := os.ReadFile(sender.events)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(eventData), sender.rootPath) || strings.Contains(string(eventData), sourcePath) || strings.Contains(string(eventData), "zfm_peer_offer_") || strings.Contains(string(eventData), "zfm_peer_transfer_") {
		t.Fatalf("event file exposed path or capability: %s", eventData)
	}
}

func TestPeerPublishCreatesReceiveDirectory(t *testing.T) {
	receiver := newPeerTestServer(t, "Receiver", true)
	var logs lockedBuffer
	receiver.api.cfg.Logger = log.New(&logs, "", 0)
	stageName := "zfm_peer_test"
	if err := receiver.api.peer.stage.Mkdir(stageName, 0o700); err != nil {
		t.Fatal(err)
	}
	file, err := receiver.api.peer.stage.OpenFile(filepath.Join(stageName, "payload"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("book"); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	destination, err := receiver.api.peer.publishReceived(stageName, peerItem{Name: "book.epub", Type: "file"})
	if err != nil || destination != "/ZenFM Received/book.epub" {
		t.Fatalf("publish = %q, %v", destination, err)
	}
	if output := logs.String(); !strings.Contains(output, fmt.Sprintf("home=%q", receiver.root.Name())) ||
		!strings.Contains(output, `directory="/ZenFM Received"`) {
		t.Fatalf("receive mapping was not logged: %s", output)
	}
	data, err := os.ReadFile(filepath.Join(receiver.rootPath, "ZenFM Received", "book.epub"))
	if err != nil || string(data) != "book" {
		t.Fatalf("received file = %q, %v", data, err)
	}
}

func TestPeerNotificationReportsOnlyPendingTransitions(t *testing.T) {
	peer := newPeerTestServer(t, "Receiver", true)
	var notifications []bool
	peer.api.cfg.PeerNotification = func(pending bool) { notifications = append(notifications, pending) }
	peer.api.peer.mu.Lock()
	peer.api.peer.incomingEvent = &peerIncomingEvent{Status: "pending"}
	if err := peer.api.peer.publishEventsLocked(); err != nil {
		peer.api.peer.mu.Unlock()
		t.Fatal(err)
	}
	if err := peer.api.peer.publishEventsLocked(); err != nil {
		peer.api.peer.mu.Unlock()
		t.Fatal(err)
	}
	peer.api.peer.incomingEvent.Status = "accepted"
	if err := peer.api.peer.publishEventsLocked(); err != nil {
		peer.api.peer.mu.Unlock()
		t.Fatal(err)
	}
	peer.api.peer.mu.Unlock()
	if len(notifications) != 2 || !notifications[0] || notifications[1] {
		t.Fatalf("peer notifications = %v", notifications)
	}
}

func waitPeerStatus(t *testing.T, manager *peerManager, wanted string) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		manager.mu.Lock()
		status := ""
		if manager.outgoing != nil {
			status = manager.outgoing.Status
		}
		manager.mu.Unlock()
		if status == wanted {
			return
		}
		if status == "error" || status == "declined" {
			t.Fatalf("transfer ended with %s", status)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", wanted)
}

func TestBuildPeerManifestRejectsSymlinks(t *testing.T) {
	peer := newPeerTestServer(t, "Sender", true)
	target := filepath.Join(peer.rootPath, "target.txt")
	if err := os.WriteFile(target, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(peer.rootPath, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, _, _, err := peer.api.peer.buildManifest(link); !errors.Is(err, zenfiles.ErrNotRegular) {
		t.Fatalf("symlink manifest error = %v", err)
	}
}

func TestPeerCapabilityTokenAndSourceIPAreBound(t *testing.T) {
	peer := newPeerTestServer(t, "Receiver", true)
	offer := &peerOffer{ID: "0123456789abcdef", DecisionToken: "abcdefghijklmnop", SourceIP: "192.0.2.1"}
	peer.api.peer.offers[offer.ID] = offer
	for _, test := range []struct{ remote, token string }{
		{"192.0.2.2:1", offer.DecisionToken},
		{"192.0.2.1:1", "wrongwrongwrong1"},
	} {
		request := httptest.NewRequest(http.MethodGet, "/api/peer/v1/offers/"+offer.ID, nil)
		request.SetPathValue("offerId", offer.ID)
		request.RemoteAddr = test.remote
		request.Header.Set("Authorization", "Bearer "+test.token)
		peer.api.peer.mu.Lock()
		got := peer.api.peer.authorizedOfferLocked(request, offer.ID, "")
		peer.api.peer.mu.Unlock()
		if got != nil {
			t.Fatal("capability accepted from wrong token or source")
		}
	}
}

func TestInterruptedPeerFolderIsCleanedAndNeverPublished(t *testing.T) {
	receiver := newPeerTestServer(t, "Receiver", true)
	offer := &peerOffer{
		ID: "0123456789abcdef", DecisionToken: "abcdefghijklmnop", SourceIP: "192.0.2.1", Status: "pending",
		ExpiresAt: time.Now().Add(time.Minute),
		Request: peerOfferRequest{
			Sender: peerIdentity{Name: "Sender", Fingerprint: strings.Repeat("A", 64), Port: 54321},
			Item:   peerItem{Name: "Books", Type: "directory", Bytes: 4, FileCount: 1, EntryCount: 2},
			Entries: []peerManifestEntry{
				{ID: "zfm_file_00000000", Path: ".", Type: "directory"},
				{ID: "zfm_file_00000001", Path: "partial.epub", Type: "file", Size: 4},
			},
		},
	}
	receiver.api.peer.mu.Lock()
	receiver.api.peer.offers[offer.ID], receiver.api.peer.incoming = offer, offer.ID
	receiver.api.peer.incomingEvent = &peerIncomingEvent{ID: offer.ID, Sender: "Sender", Fingerprint: offer.Request.Sender.Fingerprint,
		Name: "Books", Type: "directory", Bytes: 4, FileCount: 1, EntryCount: 2, Status: "pending"}
	receiver.api.peer.mu.Unlock()
	if err := receiver.api.peer.accept(offer.ID); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPut,
		"/api/peer/v1/transfers/"+offer.SessionID+"/files/zfm_file_00000001", strings.NewReader("ab"))
	request.ContentLength = 4
	request.RemoteAddr = "192.0.2.1:1234"
	request.Header.Set("Authorization", "Bearer "+offer.SessionToken)
	response := httptest.NewRecorder()
	receiver.api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusRequestTimeout {
		t.Fatalf("truncated upload = %d %s", response.Code, response.Body.String())
	}
	receiver.api.peer.mu.Lock()
	_, retained := receiver.api.peer.offers[offer.ID]
	incoming := receiver.api.peer.incoming
	receiver.api.peer.mu.Unlock()
	if retained || incoming != "" {
		t.Fatal("interrupted offer or staging state was retained")
	}
	if _, err := os.Stat(filepath.Join(receiver.rootPath, "ZenFM Received", "Books")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("interrupted folder became visible")
	}
}
