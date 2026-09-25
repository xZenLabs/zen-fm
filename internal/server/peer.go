package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/xZenLabs/zen-fm/internal/auth"
	zenfiles "github.com/xZenLabs/zen-fm/internal/files"
	"github.com/xZenLabs/zen-fm/internal/platform"
)

const (
	peerProtocolVersion = 1
	peerMaxEntries      = 10_000
	peerMaxBytes        = int64(2 << 30)
	peerMaxMetadata     = int64(1 << 20)
	peerOfferTTL        = 2 * time.Minute
	peerDecisionPoll    = 500 * time.Millisecond
	peerDiscoveryWait   = 1200 * time.Millisecond
	peerDiscoveryRetry  = 250 * time.Millisecond
	peerDiscoveryProbes = 3
	peerInternalDir     = ".zenfm-internal-peer"
	peerService         = "zenfm-peer"
)

type peerManager struct {
	server       *Server
	name         string
	fingerprint  string
	port         int
	eventPath    string
	receiveFiles *zenfiles.Root
	stage        *os.Root
	limiter      *attemptLimiter

	mu                  sync.Mutex
	revision            uint64
	discovery           *peerDiscoveryEvent
	incoming            string
	incomingEvent       *peerIncomingEvent
	notificationPending bool
	offers              map[string]*peerOffer
	outgoing            *peerOutgoing
	peers               map[string]peerDevice
	discoveryNonce      string
	discoveryRequest    string
	discoveryDeadline   time.Time
	discoveryError      string
	udp                 *net.UDPConn
	closed              chan struct{}
	wg                  sync.WaitGroup
}

type peerIdentity struct {
	Name        string `json:"name"`
	Fingerprint string `json:"fingerprint"`
	Port        int    `json:"port"`
}

type peerDevice struct {
	peerIdentity
	Address string `json:"-"`
}

type peerManifestEntry struct {
	ID   string `json:"id"`
	Path string `json:"path"`
	Type string `json:"type"`
	Size int64  `json:"size"`
}

type peerItem struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Bytes      int64  `json:"bytes"`
	FileCount  int    `json:"fileCount"`
	EntryCount int    `json:"entryCount"`
}

type peerOfferRequest struct {
	Sender  peerIdentity        `json:"sender"`
	Item    peerItem            `json:"item"`
	Entries []peerManifestEntry `json:"entries"`
}

type peerOffer struct {
	ID            string
	DecisionToken string
	SourceIP      string
	Request       peerOfferRequest
	Status        string
	ExpiresAt     time.Time
	SessionID     string
	SessionToken  string
	StageName     string
	Received      map[string]bool
	ReceivedBytes int64
	StreamedBytes int64
	LastProgress  time.Time
	LastEvent     time.Time
}

type peerOutgoing struct {
	ID             string
	Peer           peerDevice
	Source         string
	Item           peerItem
	Entries        []peerManifestEntry
	Status         string
	SentBytes      int64
	Error          string
	Destination    string
	cancel         context.CancelFunc
	decisionID     string
	decisionToken  string
	sessionID      string
	sessionToken   string
	lastEventBytes int64
	lastEvent      time.Time
}

type peerDiscoveryEvent struct {
	RequestID string              `json:"requestId"`
	Status    string              `json:"status"`
	Peers     []peerDisplayDevice `json:"peers,omitempty"`
	Error     string              `json:"error,omitempty"`
}

type peerDisplayDevice struct {
	Name        string `json:"name"`
	Fingerprint string `json:"fingerprint"`
	Port        int    `json:"port"`
}

type peerIncomingEvent struct {
	ID            string `json:"id"`
	Sender        string `json:"sender"`
	Fingerprint   string `json:"fingerprint"`
	Name          string `json:"name"`
	Type          string `json:"type"`
	Bytes         int64  `json:"bytes"`
	FileCount     int    `json:"fileCount"`
	EntryCount    int    `json:"entryCount"`
	ExpiresAt     int64  `json:"expiresAt"`
	Status        string `json:"status"`
	ReceivedBytes int64  `json:"receivedBytes"`
	Destination   string `json:"destination,omitempty"`
	Error         string `json:"error,omitempty"`
}

type peerOutgoingEvent struct {
	ID          string `json:"id"`
	Peer        string `json:"peer"`
	Fingerprint string `json:"fingerprint"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Status      string `json:"status"`
	Bytes       int64  `json:"bytes"`
	SentBytes   int64  `json:"sentBytes"`
	Destination string `json:"destination,omitempty"`
	Error       string `json:"error,omitempty"`
}

type peerEvents struct {
	Version   int                 `json:"version"`
	Revision  uint64              `json:"revision"`
	Discovery *peerDiscoveryEvent `json:"discovery,omitempty"`
	Incoming  *peerIncomingEvent  `json:"incoming,omitempty"`
	Outgoing  *peerOutgoingEvent  `json:"outgoing,omitempty"`
}

type peerDatagram struct {
	Service     string `json:"service"`
	Version     int    `json:"version"`
	Type        string `json:"type"`
	Nonce       string `json:"nonce"`
	Name        string `json:"name,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Port        int    `json:"port,omitempty"`
}

func newPeerManager(server *Server, receiveFiles *zenfiles.Root) (*peerManager, error) {
	port, err := addressPort(server.cfg.PeerAddress)
	if err != nil {
		return nil, err
	}
	if !validPeerFingerprint(server.cfg.PeerFingerprint) {
		return nil, errors.New("peer fingerprint is invalid")
	}
	stage, _, err := receiveFiles.OpenInternalDirectory(peerInternalDir)
	if err != nil {
		return nil, err
	}
	m := &peerManager{
		server: server, name: cleanPeerName(server.cfg.PeerName),
		fingerprint: strings.ToUpper(server.cfg.PeerFingerprint), port: port,
		eventPath: server.cfg.PeerEvents, receiveFiles: receiveFiles, stage: stage,
		limiter: newAttemptLimiter(8, time.Minute, server.cfg.Now),
		offers:  make(map[string]*peerOffer), peers: make(map[string]peerDevice), closed: make(chan struct{}),
	}
	if m.name == "" {
		m.name = "ZenFM Device"
	}
	m.cleanupStages()
	if err := m.publishEventsLocked(); err != nil {
		stage.Close()
		return nil, err
	}
	if server.cfg.PeerDiscoveryPort > 0 {
		m.startDiscovery(server.cfg.PeerDiscoveryPort)
	}
	return m, nil
}

func addressPort(address string) (int, error) {
	_, value, err := net.SplitHostPort(address)
	if err != nil {
		return 0, errors.New("peer listen address is invalid")
	}
	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return 0, errors.New("peer listen port is invalid")
	}
	return port, nil
}

func cleanPeerName(value string) string {
	value = strings.TrimSpace(value)
	if !utf8.ValidString(value) || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) > 64 {
		value = string(runes[:64])
	}
	return value
}

func validPeerFingerprint(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validPeerID(value string) bool {
	if len(value) < 16 || len(value) > 80 {
		return false
	}
	for _, character := range value {
		if character != '-' && character != '_' && (character < 'A' || character > 'Z') &&
			(character < 'a' || character > 'z') && (character < '0' || character > '9') {
			return false
		}
	}
	return true
}

func (m *peerManager) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/peer/v1/info", m.info)
	mux.HandleFunc("POST /api/peer/v1/offers", m.createOffer)
	mux.HandleFunc("GET /api/peer/v1/offers/{offerId}", m.getOffer)
	mux.HandleFunc("DELETE /api/peer/v1/offers/{offerId}", m.cancelOffer)
	mux.HandleFunc("PUT /api/peer/v1/transfers/{sessionId}/files/{fileId}", m.putTransferFile)
	mux.HandleFunc("POST /api/peer/v1/transfers/{sessionId}", m.completeTransfer)
	mux.HandleFunc("DELETE /api/peer/v1/transfers/{sessionId}", m.cancelTransfer)
}

func (m *peerManager) ready() bool {
	owner, err := m.server.cfg.Store.Owner()
	return err == nil && !owner.SetupRequired
}

func (m *peerManager) close() {
	close(m.closed)
	m.mu.Lock()
	if m.outgoing != nil && m.outgoing.cancel != nil {
		m.outgoing.cancel()
		if !terminalPeerStatus(m.outgoing.Status) {
			m.outgoing.Status = "canceled"
		}
	}
	udp := m.udp
	m.udp = nil
	for _, offer := range m.offers {
		m.cleanupOfferLocked(offer)
	}
	m.incomingEvent, m.discovery = nil, nil
	m.publishEventsLocked()
	m.mu.Unlock()
	if udp != nil {
		_ = udp.Close()
	}
	m.wg.Wait()
	_ = m.stage.Close()
}

func (m *peerManager) cleanupStages() {
	directory, err := m.stage.Open(".")
	if err != nil {
		return
	}
	entries, err := directory.ReadDir(-1)
	_ = directory.Close()
	if err != nil {
		return
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "zfm_peer_") {
			_ = m.stage.RemoveAll(entry.Name())
		}
	}
}

func (m *peerManager) info(w http.ResponseWriter, r *http.Request) {
	if !m.requireReady(w, r) {
		return
	}
	challenge := r.URL.Query().Get("challenge")
	if !validPeerID(challenge) {
		problem(w, r, http.StatusBadRequest, "Invalid Request", "challenge is invalid")
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Service     string `json:"service"`
		Version     int    `json:"version"`
		Name        string `json:"name"`
		Fingerprint string `json:"fingerprint"`
		Challenge   string `json:"challenge"`
	}{peerService, peerProtocolVersion, m.name, m.fingerprint, challenge})
}

func (m *peerManager) requireReady(w http.ResponseWriter, r *http.Request) bool {
	if !m.ready() {
		problem(w, r, http.StatusForbidden, "Setup Required", "ZenFM peer sharing requires completed owner setup")
		return false
	}
	return true
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return ""
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil || ip.IsUnspecified() {
		return ""
	}
	return ip.String()
}

func (m *peerManager) createOffer(w http.ResponseWriter, r *http.Request) {
	if !m.requireReady(w, r) {
		return
	}
	ip := remoteIP(r)
	if ip == "" || !m.limiter.allowed(ip) {
		w.Header().Set("Retry-After", "60")
		problem(w, r, http.StatusTooManyRequests, "Rate Limited", "too many peer offers")
		return
	}
	m.limiter.fail(ip)
	var request peerOfferRequest
	if err := readJSON(w, r, &request, peerMaxMetadata); err != nil || validatePeerOffer(request) != nil {
		problem(w, r, http.StatusBadRequest, "Invalid Offer", "peer offer metadata is invalid")
		return
	}
	request.Sender.Name = cleanPeerName(request.Sender.Name)
	request.Sender.Fingerprint = strings.ToUpper(request.Sender.Fingerprint)
	if err := m.verifySender(r.Context(), ip, request.Sender); err != nil {
		problem(w, r, http.StatusForbidden, "Unverified Sender", "sender identity could not be verified")
		return
	}
	m.mu.Lock()
	m.pruneLocked()
	if m.incoming != "" {
		m.mu.Unlock()
		problem(w, r, http.StatusConflict, "Busy", "another incoming transfer is pending or active")
		return
	}
	id, err := auth.RandomToken("zfm_peer_", 128)
	if err != nil {
		m.mu.Unlock()
		internalError(w, r, err)
		return
	}
	token, err := auth.RandomToken("zfm_peer_offer_", 192)
	if err != nil {
		m.mu.Unlock()
		internalError(w, r, err)
		return
	}
	offer := &peerOffer{ID: id, DecisionToken: token, SourceIP: ip, Request: request,
		Status: "pending", ExpiresAt: m.server.cfg.Now().Add(peerOfferTTL)}
	m.offers[id], m.incoming = offer, id
	m.incomingEvent = &peerIncomingEvent{
		ID: id, Sender: request.Sender.Name, Fingerprint: request.Sender.Fingerprint,
		Name: request.Item.Name, Type: request.Item.Type, Bytes: request.Item.Bytes,
		FileCount: request.Item.FileCount, EntryCount: request.Item.EntryCount,
		ExpiresAt: offer.ExpiresAt.Unix(), Status: "pending",
	}
	m.publishEventsLocked()
	m.wg.Add(1)
	go m.watchOffer(id, offer.ExpiresAt)
	m.mu.Unlock()
	writeJSON(w, http.StatusAccepted, map[string]any{
		"offerId": id, "token": token, "expiresAt": offer.ExpiresAt.Unix(),
	})
}

func validatePeerOffer(request peerOfferRequest) error {
	if cleanPeerName(request.Sender.Name) == "" || !validPeerFingerprint(request.Sender.Fingerprint) ||
		request.Sender.Port < 1 || request.Sender.Port > 65535 || validatePeerItem(request.Item) != nil ||
		len(request.Entries) < 1 || len(request.Entries) > peerMaxEntries {
		return errors.New("invalid offer")
	}
	seenID, seenPath, directories := make(map[string]bool), make(map[string]bool), make(map[string]bool)
	var total int64
	files := 0
	rootType := ""
	for _, entry := range request.Entries {
		if !validPeerID(entry.ID) || seenID[entry.ID] || seenPath[entry.Path] || !validPeerRelativePath(entry.Path) ||
			(entry.Type != "file" && entry.Type != "directory") || entry.Size < 0 ||
			entry.Type == "directory" && entry.Size != 0 {
			return errors.New("invalid manifest")
		}
		seenID[entry.ID], seenPath[entry.Path] = true, true
		if entry.Path == "." {
			rootType = entry.Type
		}
		if entry.Type == "directory" {
			directories[entry.Path] = true
		}
		if entry.Type == "file" {
			files++
			if entry.Size > peerMaxBytes-total {
				return errors.New("too large")
			}
			total += entry.Size
		}
	}
	if rootType != request.Item.Type || files != request.Item.FileCount || total != request.Item.Bytes ||
		len(request.Entries) != request.Item.EntryCount {
		return errors.New("manifest summary mismatch")
	}
	if rootType == "file" && len(request.Entries) != 1 {
		return errors.New("file offer has children")
	}
	for _, entry := range request.Entries {
		if entry.Path == "." {
			continue
		}
		parent := path.Dir(entry.Path)
		if parent != "." && !directories[parent] {
			return errors.New("missing parent")
		}
	}
	return nil
}

func validatePeerItem(item peerItem) error {
	if item.Name == "" || len(item.Name) > 255 || item.Name == "." || item.Name == ".." || !utf8.ValidString(item.Name) ||
		strings.ContainsAny(item.Name, "/\\") || strings.IndexFunc(item.Name, unicode.IsControl) >= 0 ||
		(item.Type != "file" && item.Type != "directory") ||
		item.Bytes < 0 || item.Bytes > peerMaxBytes || item.FileCount < 0 || item.EntryCount < 1 || item.EntryCount > peerMaxEntries {
		return errors.New("invalid item")
	}
	return nil
}

func validPeerRelativePath(value string) bool {
	if value == "." {
		return true
	}
	if value == "" || len(value) > 4096 || !utf8.ValidString(value) || strings.Contains(value, "\\") || strings.IndexFunc(value, unicode.IsControl) >= 0 ||
		path.IsAbs(value) || path.Clean(value) != value {
		return false
	}
	parts := strings.Split(value, "/")
	if len(parts) > 64 {
		return false
	}
	for _, part := range parts {
		if part == "" || len(part) > 255 || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func (m *peerManager) watchOffer(offerID string, expiresAt time.Time) {
	defer m.wg.Done()
	timer := time.NewTimer(expiresAt.Sub(m.server.cfg.Now()))
	defer timer.Stop()
	select {
	case <-timer.C:
		m.mu.Lock()
		if offer := m.offers[offerID]; offer != nil && offer.Status == "pending" {
			offer.Status = "expired"
			m.cleanupOfferLocked(offer)
			if m.incomingEvent != nil && m.incomingEvent.ID == offer.ID {
				m.incomingEvent.Status = "expired"
			}
			m.publishEventsLocked()
		}
		m.mu.Unlock()
	case <-m.closed:
	}
}

func (m *peerManager) verifySender(ctx context.Context, ip string, sender peerIdentity) error {
	challenge, err := auth.RandomToken("zfm_peer_", 128)
	if err != nil {
		return err
	}
	client := pinnedPeerClient(sender.Fingerprint)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://"+net.JoinHostPort(ip, strconv.Itoa(sender.Port))+"/api/peer/v1/info?challenge="+challenge, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return errors.New("sender info failed")
	}
	var info struct {
		Service, Name, Fingerprint, Challenge string
		Version                               int
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 8<<10)).Decode(&info); err != nil ||
		info.Service != peerService || info.Version != peerProtocolVersion || info.Name != sender.Name ||
		!strings.EqualFold(info.Fingerprint, sender.Fingerprint) || info.Challenge != challenge {
		return errors.New("sender info mismatch")
	}
	return nil
}

func pinnedPeerClient(fingerprint string) *http.Client {
	wanted := strings.ToUpper(fingerprint)
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: 3 * time.Second, KeepAlive: 15 * time.Second}).DialContext,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 5 * time.Second,
		TLSClientConfig: &tls.Config{
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: true, // The explicit SPKI pin below replaces public-CA verification.
			VerifyConnection: func(state tls.ConnectionState) error {
				if len(state.PeerCertificates) < 1 {
					return errors.New("peer certificate chain is invalid")
				}
				certificate := state.PeerCertificates[0]
				now := time.Now()
				if now.Before(certificate.NotBefore) || now.After(certificate.NotAfter) {
					return errors.New("peer certificate is expired or not yet valid")
				}
				digest := sha256.Sum256(certificate.RawSubjectPublicKeyInfo)
				actual := strings.ToUpper(hex.EncodeToString(digest[:]))
				if subtle.ConstantTimeCompare([]byte(actual), []byte(wanted)) != 1 {
					return errors.New("peer certificate fingerprint mismatch")
				}
				return nil
			},
		},
	}
	return &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

func (m *peerManager) getOffer(w http.ResponseWriter, r *http.Request) {
	if !m.requireReady(w, r) {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	offer := m.authorizedOfferLocked(r, r.PathValue("offerId"), "")
	if offer == nil {
		peerNotFound(w, r)
		return
	}
	if offer.Status == "pending" && !m.server.cfg.Now().Before(offer.ExpiresAt) {
		offer.Status = "expired"
		if m.incoming == offer.ID {
			m.incoming = ""
		}
		if m.incomingEvent != nil && m.incomingEvent.ID == offer.ID {
			m.incomingEvent.Status = "expired"
		}
		m.publishEventsLocked()
	}
	response := map[string]any{"status": offer.Status}
	if offer.Status == "accepted" {
		response["sessionId"], response["token"] = offer.SessionID, offer.SessionToken
	}
	writeJSON(w, http.StatusOK, response)
}

func (m *peerManager) cancelOffer(w http.ResponseWriter, r *http.Request) {
	if !m.requireReady(w, r) {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	offer := m.authorizedOfferLocked(r, r.PathValue("offerId"), "")
	if offer == nil {
		peerNotFound(w, r)
		return
	}
	m.cleanupOfferLocked(offer)
	delete(m.offers, offer.ID)
	if m.incomingEvent != nil && m.incomingEvent.ID == offer.ID {
		m.incomingEvent.Status = "canceled"
	}
	m.publishEventsLocked()
	w.WriteHeader(http.StatusNoContent)
}

func (m *peerManager) authorizedOfferLocked(r *http.Request, offerID, sessionID string) *peerOffer {
	if !validPeerID(offerID) || remoteIP(r) == "" {
		return nil
	}
	offer := m.offers[offerID]
	if offer == nil || offer.SourceIP != remoteIP(r) {
		return nil
	}
	provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	wanted := offer.DecisionToken
	if sessionID != "" {
		wanted = offer.SessionToken
		if offer.SessionID != sessionID {
			return nil
		}
	}
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") ||
		subtle.ConstantTimeCompare([]byte(provided), []byte(wanted)) != 1 {
		return nil
	}
	return offer
}

func (m *peerManager) offerBySessionLocked(r *http.Request) *peerOffer {
	sessionID := r.PathValue("sessionId")
	for _, offer := range m.offers {
		if offer.SessionID == sessionID {
			return m.authorizedOfferLocked(r, offer.ID, sessionID)
		}
	}
	return nil
}

func (m *peerManager) putTransferFile(w http.ResponseWriter, r *http.Request) {
	if !m.requireReady(w, r) {
		return
	}
	m.mu.Lock()
	offer := m.offerBySessionLocked(r)
	if offer == nil || offer.Status != "accepted" {
		m.mu.Unlock()
		peerNotFound(w, r)
		return
	}
	entry, found := manifestEntry(offer.Request.Entries, r.PathValue("fileId"))
	if !found || entry.Type != "file" || offer.Received[entry.ID] || r.ContentLength != entry.Size {
		m.abortIncomingLocked(offer, "file transfer metadata was invalid")
		m.mu.Unlock()
		problem(w, r, http.StatusBadRequest, "Invalid Transfer", "file transfer metadata is invalid")
		return
	}
	stageName := offer.StageName
	m.mu.Unlock()

	if entry.Size > 0 {
		if err := ensureFilesystemSpace(m.receiveFiles, uint64(entry.Size)); err != nil {
			m.abortIncoming(offer.ID, "insufficient storage")
			mapError(w, r, err)
			return
		}
	}
	fileName := peerStagePath(stageName, entry.Path)
	file, err := m.stage.OpenFile(fileName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		m.abortIncoming(offer.ID, "could not stage the transfer")
		mapError(w, r, err)
		return
	}
	counting := &peerCountingReader{Reader: io.LimitReader(r.Body, entry.Size+1), progress: func(count int64) {
		m.noteIncomingProgress(offer.ID, count)
	}}
	reader := &progressReader{writer: w, reader: counting, timeout: progressTimeout,
		context: r.Context(), touch: func() {
			m.server.touch()
		}}
	written, copyErr := io.CopyBuffer(file, reader, make([]byte, 128*1024))
	if copyErr == nil {
		copyErr = file.Sync()
	}
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil || written != entry.Size {
		m.abortIncoming(offer.ID, "file transfer was incomplete")
		problem(w, r, http.StatusRequestTimeout, "Transfer Interrupted", "file transfer was incomplete")
		return
	}
	m.mu.Lock()
	offer = m.offers[offer.ID]
	if offer == nil || offer.Status != "accepted" || offer.Received[entry.ID] {
		m.mu.Unlock()
		peerNotFound(w, r)
		return
	}
	offer.Received[entry.ID] = true
	offer.ReceivedBytes += written
	offer.LastProgress = m.server.cfg.Now()
	if m.incomingEvent != nil && m.incomingEvent.ID == offer.ID {
		m.incomingEvent.ReceivedBytes = offer.StreamedBytes
	}
	m.publishEventsLocked()
	m.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func manifestEntry(entries []peerManifestEntry, id string) (peerManifestEntry, bool) {
	for _, entry := range entries {
		if entry.ID == id {
			return entry, true
		}
	}
	return peerManifestEntry{}, false
}

func peerStagePath(stageName, relative string) string {
	if relative == "." {
		return path.Join(stageName, "payload")
	}
	return path.Join(stageName, "payload", relative)
}

func (m *peerManager) completeTransfer(w http.ResponseWriter, r *http.Request) {
	if !m.requireReady(w, r) {
		return
	}
	m.mu.Lock()
	offer := m.offerBySessionLocked(r)
	if offer == nil || offer.Status != "accepted" || offer.ReceivedBytes != offer.Request.Item.Bytes {
		if offer != nil {
			m.abortIncomingLocked(offer, "transfer was incomplete")
		}
		m.mu.Unlock()
		problem(w, r, http.StatusConflict, "Incomplete Transfer", "not every offered file was received")
		return
	}
	for _, entry := range offer.Request.Entries {
		if entry.Type == "file" && !offer.Received[entry.ID] {
			m.abortIncomingLocked(offer, "transfer was incomplete")
			m.mu.Unlock()
			problem(w, r, http.StatusConflict, "Incomplete Transfer", "not every offered file was received")
			return
		}
	}
	stageName, item := offer.StageName, offer.Request.Item
	m.mu.Unlock()

	destination, err := m.publishReceived(stageName, item)
	if err != nil {
		m.abortIncoming(offer.ID, "received content could not be published")
		mapError(w, r, err)
		return
	}
	m.mu.Lock()
	if current := m.offers[offer.ID]; current != nil {
		current.Status = "complete"
		if m.incoming == current.ID {
			m.incoming = ""
		}
		delete(m.offers, current.ID)
	}
	if m.incomingEvent != nil && m.incomingEvent.ID == offer.ID {
		m.incomingEvent.Status, m.incomingEvent.Destination = "complete", destination
	}
	m.publishEventsLocked()
	m.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{"destination": destination})
}

func (m *peerManager) cancelTransfer(w http.ResponseWriter, r *http.Request) {
	if !m.requireReady(w, r) {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	offer := m.offerBySessionLocked(r)
	if offer == nil {
		peerNotFound(w, r)
		return
	}
	m.cleanupOfferLocked(offer)
	delete(m.offers, offer.ID)
	if m.incomingEvent != nil && m.incomingEvent.ID == offer.ID {
		m.incomingEvent.Status = "canceled"
	}
	m.publishEventsLocked()
	w.WriteHeader(http.StatusNoContent)
}

func (m *peerManager) publishReceived(stageName string, item peerItem) (string, error) {
	m.logf("peer receive destination: root=%q", m.receiveFiles.Name())
	sessionRoot, err := m.stage.OpenRoot(stageName)
	if err != nil {
		return "", err
	}
	defer sessionRoot.Close()
	destination, err := m.uniqueDestination(item.Name, item.Type)
	if err != nil {
		return "", err
	}
	if item.Type == "directory" {
		err = m.receiveFiles.PublishTemporaryDirectory(sessionRoot, "payload", destination)
	} else {
		var moved bool
		moved, err = m.receiveFiles.PublishTemporary(sessionRoot, "payload", destination, false)
		if err == nil && !moved {
			err = errors.New("peer staging unexpectedly crossed filesystems")
		}
	}
	if err != nil {
		return "", err
	}
	_ = m.stage.RemoveAll(stageName)
	return destination, nil
}

func (m *peerManager) uniqueDestination(name, kind string) (string, error) {
	stem, extension := name, ""
	if kind == "file" {
		extension = path.Ext(name)
		stem = strings.TrimSuffix(name, extension)
		if stem == "" {
			stem, extension = name, ""
		}
	}
	for index := 0; index <= 9999; index++ {
		candidate := name
		if index > 0 {
			candidate = fmt.Sprintf("%s (%d)%s", stem, index, extension)
		}
		destination := path.Join("/", candidate)
		if _, err := m.receiveFiles.Entry(destination); errors.Is(err, fs.ErrNotExist) {
			return destination, nil
		} else if err != nil {
			return "", err
		}
	}
	return "", zenfiles.ErrConflict
}

func peerNotFound(w http.ResponseWriter, r *http.Request) {
	problem(w, r, http.StatusNotFound, "Not Found", "peer transfer is unavailable")
}

func (m *peerManager) cleanupOfferLocked(offer *peerOffer) {
	if offer == nil {
		return
	}
	if offer.StageName != "" {
		_ = m.stage.RemoveAll(offer.StageName)
	}
	if m.incoming == offer.ID {
		m.incoming = ""
	}
}

func (m *peerManager) abortIncoming(offerID, message string) {
	m.mu.Lock()
	if offer := m.offers[offerID]; offer != nil {
		m.abortIncomingLocked(offer, message)
	}
	m.mu.Unlock()
}

func (m *peerManager) abortIncomingLocked(offer *peerOffer, message string) {
	m.cleanupOfferLocked(offer)
	delete(m.offers, offer.ID)
	if m.incomingEvent != nil && m.incomingEvent.ID == offer.ID {
		m.incomingEvent.Status, m.incomingEvent.Error = "error", message
	}
	m.publishEventsLocked()
}

func (m *peerManager) noteIncomingProgress(offerID string, count int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	offer := m.offers[offerID]
	if offer == nil || offer.Status != "accepted" {
		return
	}
	offer.StreamedBytes += count
	offer.LastProgress = m.server.cfg.Now()
	if m.incomingEvent != nil && m.incomingEvent.ID == offer.ID &&
		(offer.StreamedBytes == offer.Request.Item.Bytes || offer.StreamedBytes-m.incomingEvent.ReceivedBytes >= 1<<20 ||
			offer.LastEvent.IsZero() || offer.LastProgress.Sub(offer.LastEvent) >= 250*time.Millisecond) {
		m.incomingEvent.ReceivedBytes = offer.StreamedBytes
		offer.LastEvent = offer.LastProgress
		m.publishEventsLocked()
	}
}

func (m *peerManager) pruneLocked() {
	now := m.server.cfg.Now()
	for id, offer := range m.offers {
		if offer.Status == "pending" && !now.Before(offer.ExpiresAt) {
			offer.Status = "expired"
			m.cleanupOfferLocked(offer)
			if m.incomingEvent != nil && m.incomingEvent.ID == id {
				m.incomingEvent.Status = "expired"
			}
		}
		if (offer.Status == "declined" || offer.Status == "expired") && now.After(offer.ExpiresAt.Add(peerOfferTTL)) {
			delete(m.offers, id)
		}
	}
}

func (m *peerManager) publishEventsLocked() (result error) {
	defer func() {
		if result != nil {
			m.logf("peer event update failed: %v", result)
		}
	}()
	m.revision++
	events := peerEvents{Version: peerProtocolVersion, Revision: m.revision, Discovery: m.discovery, Incoming: m.incomingEvent}
	if outgoing := m.outgoing; outgoing != nil {
		events.Outgoing = &peerOutgoingEvent{
			ID: outgoing.ID, Peer: outgoing.Peer.Name, Fingerprint: outgoing.Peer.Fingerprint,
			Name: outgoing.Item.Name, Type: outgoing.Item.Type, Status: outgoing.Status,
			Bytes: outgoing.Item.Bytes, SentBytes: outgoing.SentBytes,
			Destination: outgoing.Destination, Error: outgoing.Error,
		}
	}
	if m.eventPath == "" {
		return nil
	}
	data, err := json.Marshal(events)
	if err != nil {
		return err
	}
	localDirectory := filepath.Dir(m.eventPath)
	if localDirectory != "" {
		if err := os.MkdirAll(localDirectory, 0o700); err != nil {
			return err
		}
	}
	temporary, err := os.CreateTemp(localDirectory, ".peer-events-")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	err = platform.ModeChangeError(temporary.Chmod(0o600), m.server.cfg.ModeLessFilesystem)
	if err == nil {
		_, err = temporary.Write(data)
	}
	if err == nil {
		err = temporary.Sync()
	}
	err = errors.Join(err, temporary.Close())
	if err != nil {
		return err
	}
	if err := os.Rename(temporaryName, m.eventPath); err != nil {
		return err
	}
	pending := m.incomingEvent != nil && m.incomingEvent.Status == "pending"
	if pending != m.notificationPending {
		m.notificationPending = pending
		if m.server.cfg.PeerNotification != nil {
			m.server.cfg.PeerNotification(pending)
		}
	}
	return nil
}

func (m *peerManager) startDiscovery(port int) {
	address := &net.UDPAddr{IP: net.IPv4zero, Port: port}
	connection, err := net.ListenUDP("udp4", address)
	if err != nil {
		m.discoveryError = err.Error()
		m.logf("peer discovery unavailable: listen UDP %d: %v", port, err)
		return
	}
	raw, err := connection.SyscallConn()
	if err == nil {
		var socketErr error
		controlErr := raw.Control(func(fd uintptr) {
			socketErr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1)
		})
		err = errors.Join(controlErr, socketErr)
	}
	if err != nil {
		_ = connection.Close()
		m.discoveryError = err.Error()
		m.logf("peer discovery unavailable: enable UDP broadcast: %v", err)
		return
	}
	m.udp = connection
	m.logf("peer discovery listening: udp=0.0.0.0:%d", port)
	m.wg.Add(1)
	go m.discoveryLoop(connection)
}

func (m *peerManager) logf(format string, values ...any) {
	if m.server.cfg.Logger != nil {
		m.server.cfg.Logger.Printf(format, values...)
	}
}

func (m *peerManager) discoveryLoop(connection *net.UDPConn) {
	defer m.wg.Done()
	buffer := make([]byte, 2049)
	for {
		length, source, err := connection.ReadFromUDP(buffer)
		if err != nil {
			select {
			case <-m.closed:
			default:
				m.logf("peer discovery stopped: %v", err)
			}
			return
		}
		if length > 2048 || source.IP.To4() == nil {
			continue
		}
		datagram, valid := decodePeerDatagram(buffer[:length])
		if !valid {
			continue
		}
		switch datagram.Type {
		case "discover":
			if !m.ready() {
				m.logf("peer discovery request ignored: source=%s owner-setup=incomplete", source.IP)
				continue
			}
			response, _ := json.Marshal(peerDatagram{Service: peerService, Version: peerProtocolVersion, Type: "announce",
				Nonce: datagram.Nonce, Name: m.name, Fingerprint: m.fingerprint, Port: m.port})
			if _, err := connection.WriteToUDP(response, source); err != nil {
				m.logf("peer discovery response failed: destination=%s error=%v", source, err)
			} else {
				m.logf("peer discovery response sent: destination=%s", source)
			}
		case "announce":
			device, valid := announcedPeer(datagram, source.IP.String(), m.fingerprint)
			if !valid {
				continue
			}
			m.mu.Lock()
			if datagram.Nonce == m.discoveryNonce && m.server.cfg.Now().Before(m.discoveryDeadline) && len(m.peers) < 32 {
				m.peers[device.Fingerprint] = device
				m.refreshDiscoveryLocked("searching", "")
				m.logf("peer discovered: source=%s name=%q fingerprint=...%s https-port=%d", source.IP,
					device.Name, device.Fingerprint[len(device.Fingerprint)-8:], device.Port)
			}
			m.mu.Unlock()
		}
	}
}

func decodePeerDatagram(data []byte) (peerDatagram, bool) {
	var datagram peerDatagram
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	valid := decoder.Decode(&datagram) == nil && decoder.Decode(&struct{}{}) == io.EOF &&
		datagram.Service == peerService && datagram.Version == peerProtocolVersion && validPeerID(datagram.Nonce)
	return datagram, valid
}

func announcedPeer(datagram peerDatagram, address, selfFingerprint string) (peerDevice, bool) {
	name, fingerprint := cleanPeerName(datagram.Name), strings.ToUpper(datagram.Fingerprint)
	if datagram.Type != "announce" || name == "" || net.ParseIP(address).To4() == nil || !validPeerFingerprint(fingerprint) ||
		fingerprint == selfFingerprint || datagram.Port < 1 || datagram.Port > 65535 {
		return peerDevice{}, false
	}
	return peerDevice{peerIdentity: peerIdentity{Name: name, Fingerprint: fingerprint, Port: datagram.Port}, Address: address}, true
}

func (m *peerManager) discover(requestID string) error {
	if !validPeerID(requestID) {
		return errors.New("invalid request id")
	}
	if !m.ready() {
		return errors.New("owner setup is incomplete")
	}
	nonce, err := auth.RandomToken("zfm_peer_", 128)
	if err != nil {
		return err
	}
	m.mu.Lock()
	if m.udp == nil {
		err := m.discoveryError
		if err == "" {
			err = "discovery is unavailable"
		}
		m.discovery = &peerDiscoveryEvent{RequestID: requestID, Status: "error", Error: err}
		m.publishEventsLocked()
		m.mu.Unlock()
		return errors.New(err)
	}
	m.peers = make(map[string]peerDevice)
	m.discoveryNonce, m.discoveryRequest = nonce, requestID
	m.discoveryDeadline = m.server.cfg.Now().Add(peerDiscoveryWait)
	m.refreshDiscoveryLocked("searching", "")
	connection, deadline := m.udp, m.discoveryDeadline
	m.mu.Unlock()

	packet, _ := json.Marshal(peerDatagram{Service: peerService, Version: peerProtocolVersion, Type: "discover", Nonce: nonce})
	targets, sent := discoveryBroadcasts(), 0
	for attempt := 1; attempt <= peerDiscoveryProbes; attempt++ {
		if attempt > 1 {
			time.Sleep(peerDiscoveryRetry)
		}
		for _, address := range targets {
			address.Port = m.server.cfg.PeerDiscoveryPort
			if _, err := connection.WriteToUDP(packet, address); err != nil {
				m.logf("peer discovery broadcast failed: attempt=%d destination=%s error=%v", attempt, address, err)
			} else {
				sent++
				m.logf("peer discovery broadcast sent: attempt=%d destination=%s", attempt, address)
			}
		}
	}
	if sent == 0 {
		m.mu.Lock()
		if m.discoveryNonce == nonce {
			m.refreshDiscoveryLocked("error", "could not send a UDP discovery broadcast")
		}
		m.mu.Unlock()
		return errors.New("could not send a UDP discovery broadcast")
	}
	m.logf("peer discovery started: targets=%d probes=%d successful=%d wait=%s",
		len(targets), peerDiscoveryProbes, sent, peerDiscoveryWait)
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		timer := time.NewTimer(deadline.Sub(m.server.cfg.Now()))
		defer timer.Stop()
		select {
		case <-timer.C:
			m.mu.Lock()
			if m.discoveryNonce == nonce {
				m.refreshDiscoveryLocked("ready", "")
				m.logf("peer discovery finished: devices=%d", len(m.peers))
			}
			m.mu.Unlock()
		case <-m.closed:
		}
	}()
	return nil
}

func discoveryBroadcasts() []*net.UDPAddr {
	addresses := []*net.UDPAddr{{IP: net.IPv4bcast}}
	interfaces, err := net.Interfaces()
	if err != nil {
		return addresses
	}
	seen := map[string]bool{net.IPv4bcast.String(): true}
	for _, networkInterface := range interfaces {
		if !usableDiscoveryInterface(networkInterface.Flags) {
			continue
		}
		values, _ := networkInterface.Addrs()
		for _, value := range values {
			network, ok := value.(*net.IPNet)
			if !ok {
				continue
			}
			ip := network.IP.To4()
			if ip == nil || len(network.Mask) != net.IPv4len {
				continue
			}
			broadcast := make(net.IP, net.IPv4len)
			for index := range broadcast {
				broadcast[index] = ip[index] | ^network.Mask[index]
			}
			if !seen[broadcast.String()] {
				seen[broadcast.String()] = true
				addresses = append(addresses, &net.UDPAddr{IP: broadcast})
			}
		}
	}
	return addresses
}

func usableDiscoveryInterface(flags net.Flags) bool {
	return flags&net.FlagUp != 0 && flags&net.FlagLoopback == 0
}

func (m *peerManager) refreshDiscoveryLocked(status, message string) {
	peers := make([]peerDisplayDevice, 0, len(m.peers))
	for _, device := range m.peers {
		peers = append(peers, peerDisplayDevice{Name: device.Name, Fingerprint: device.Fingerprint, Port: device.Port})
	}
	sort.Slice(peers, func(i, j int) bool {
		if peers[i].Name == peers[j].Name {
			return peers[i].Fingerprint < peers[j].Fingerprint
		}
		return peers[i].Name < peers[j].Name
	})
	m.discovery = &peerDiscoveryEvent{RequestID: m.discoveryRequest, Status: status, Peers: peers, Error: message}
	m.publishEventsLocked()
}

func (m *peerManager) accept(offerID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pruneLocked()
	offer := m.offers[offerID]
	if offer == nil || offer.Status != "pending" || m.incoming != offerID || !m.server.cfg.Now().Before(offer.ExpiresAt) {
		return errors.New("offer is unavailable")
	}
	if offer.Request.Item.Bytes > m.receiveFiles.MaxWriteBytes() {
		return zenfiles.ErrTooLarge
	}
	if err := ensureFilesystemSpace(m.receiveFiles, uint64(offer.Request.Item.Bytes)); err != nil {
		return err
	}
	sessionID, err := auth.RandomToken("zfm_peer_session_", 128)
	if err != nil {
		return err
	}
	sessionToken, err := auth.RandomToken("zfm_peer_transfer_", 192)
	if err != nil {
		return err
	}
	stageName, err := auth.RandomToken("zfm_peer_", 128)
	if err != nil {
		return err
	}
	if err := m.stage.Mkdir(stageName, 0o700); err != nil {
		return err
	}
	if offer.Request.Item.Type == "directory" {
		if err := m.stage.Mkdir(path.Join(stageName, "payload"), 0o700); err != nil {
			_ = m.stage.RemoveAll(stageName)
			return err
		}
		directories := make([]string, 0)
		for _, entry := range offer.Request.Entries {
			if entry.Type == "directory" && entry.Path != "." {
				directories = append(directories, entry.Path)
			}
		}
		sort.Slice(directories, func(i, j int) bool { return strings.Count(directories[i], "/") < strings.Count(directories[j], "/") })
		for _, directory := range directories {
			if err := m.stage.Mkdir(peerStagePath(stageName, directory), 0o700); err != nil {
				_ = m.stage.RemoveAll(stageName)
				return err
			}
		}
	}
	offer.Status, offer.SessionID, offer.SessionToken, offer.StageName = "accepted", sessionID, sessionToken, stageName
	offer.Received, offer.LastProgress = make(map[string]bool), m.server.cfg.Now()
	if m.incomingEvent != nil && m.incomingEvent.ID == offer.ID {
		m.incomingEvent.Status = "accepted"
	}
	m.publishEventsLocked()
	m.wg.Add(1)
	go m.watchIncoming(offer.ID)
	return nil
}

func (m *peerManager) watchIncoming(offerID string) {
	defer m.wg.Done()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			m.mu.Lock()
			offer := m.offers[offerID]
			if offer == nil || offer.Status != "accepted" {
				m.mu.Unlock()
				return
			}
			if m.server.cfg.Now().Sub(offer.LastProgress) >= progressTimeout {
				m.cleanupOfferLocked(offer)
				delete(m.offers, offer.ID)
				if m.incomingEvent != nil && m.incomingEvent.ID == offer.ID {
					m.incomingEvent.Status, m.incomingEvent.Error = "error", "transfer timed out"
				}
				m.publishEventsLocked()
				m.mu.Unlock()
				return
			}
			m.mu.Unlock()
		case <-m.closed:
			return
		}
	}
}

func (m *peerManager) decline(offerID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	offer := m.offers[offerID]
	if offer == nil || offer.Status != "pending" || m.incoming != offerID {
		return errors.New("offer is unavailable")
	}
	offer.Status = "declined"
	m.cleanupOfferLocked(offer)
	if m.incomingEvent != nil && m.incomingEvent.ID == offer.ID {
		m.incomingEvent.Status = "declined"
	}
	m.publishEventsLocked()
	return nil
}

func (m *peerManager) buildManifest(absolute string) (string, peerItem, []peerManifestEntry, error) {
	rootPath, err := m.server.peerFiles.PublicPathFromAbsolute(absolute)
	if err != nil {
		return "", peerItem{}, nil, errors.New("selected item is unavailable or outside the device storage root")
	}
	rootEntry, err := m.server.peerFiles.Entry(rootPath)
	if err != nil || rootEntry.Symlink || (!rootEntry.Regular && !rootEntry.Directory) || m.server.peerFiles.Pseudo(rootPath) {
		return "", peerItem{}, nil, zenfiles.ErrNotRegular
	}
	item := peerItem{Name: rootEntry.Name, Type: "file"}
	if rootEntry.Directory {
		item.Type = "directory"
	}
	entries := make([]peerManifestEntry, 0)
	var walk func(string, string) error
	walk = func(publicPath, relative string) error {
		if !validPeerRelativePath(relative) {
			return zenfiles.ErrInvalidPath
		}
		if len(entries) >= peerMaxEntries {
			return zenfiles.ErrWalkLimit
		}
		entry, err := m.server.peerFiles.Entry(publicPath)
		if err != nil || entry.Symlink || (!entry.Regular && !entry.Directory) || m.server.peerFiles.Pseudo(publicPath) {
			return zenfiles.ErrNotRegular
		}
		manifest := peerManifestEntry{ID: fmt.Sprintf("zfm_file_%08d", len(entries)), Path: relative, Type: "file", Size: entry.Size}
		if entry.Directory {
			manifest.Type, manifest.Size = "directory", 0
		} else {
			if entry.Size < 0 || entry.Size > peerMaxBytes-item.Bytes || entry.Size > m.server.peerFiles.MaxWriteBytes()-item.Bytes {
				return zenfiles.ErrTooLarge
			}
			item.Bytes += entry.Size
			item.FileCount++
		}
		entries = append(entries, manifest)
		if entry.Directory {
			listing, err := m.server.peerFiles.ListStrict(publicPath, true)
			if err != nil {
				return err
			}
			for _, child := range listing.Entries {
				childRelative := child.Name
				if relative != "." {
					childRelative = path.Join(relative, child.Name)
				}
				if err := walk(child.Path, childRelative); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(rootPath, "."); err != nil {
		return "", peerItem{}, nil, err
	}
	item.EntryCount = len(entries)
	if err := validatePeerItem(item); err != nil {
		return "", peerItem{}, nil, err
	}
	return rootPath, item, entries, nil
}

func (m *peerManager) send(requestID, fingerprint, encodedPath string) error {
	if !validPeerID(requestID) || !validPeerFingerprint(fingerprint) || len(encodedPath) > 8192 {
		return errors.New("invalid send command")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(encodedPath)
	if err != nil || len(decoded) == 0 || len(decoded) > 4096 || !utf8.Valid(decoded) {
		return errors.New("invalid send path")
	}
	m.logf("peer send source validation: selected=%q source-root=%q home=%q", string(decoded), m.server.peerFiles.Name(), m.server.cfg.Files.Name())
	fingerprint = strings.ToUpper(fingerprint)
	m.mu.Lock()
	peer, found := m.peers[fingerprint]
	if !found || m.server.cfg.Now().After(m.discoveryDeadline.Add(peerOfferTTL)) {
		m.mu.Unlock()
		return errors.New("peer is not in live discovery state")
	}
	if m.outgoing != nil && !terminalPeerStatus(m.outgoing.Status) {
		m.mu.Unlock()
		return errors.New("another outgoing transfer is active")
	}
	m.mu.Unlock()
	source, item, entries, err := m.buildManifest(string(decoded))
	if err != nil {
		return err
	}
	m.logf("peer send source resolved: selected=%q source=%q", string(decoded), source)
	ctx, cancel := context.WithCancel(context.Background())
	job := &peerOutgoing{ID: requestID, Peer: peer, Source: source, Item: item, Entries: entries, Status: "offering", cancel: cancel}
	m.mu.Lock()
	if m.outgoing != nil && !terminalPeerStatus(m.outgoing.Status) {
		m.mu.Unlock()
		cancel()
		return errors.New("another outgoing transfer is active")
	}
	m.outgoing = job
	m.publishEventsLocked()
	m.mu.Unlock()
	m.wg.Add(1)
	go m.runOutgoing(ctx, job)
	return nil
}

func terminalPeerStatus(status string) bool {
	return status == "complete" || status == "declined" || status == "canceled" || status == "error"
}

func (m *peerManager) runOutgoing(ctx context.Context, job *peerOutgoing) {
	defer m.wg.Done()
	client := pinnedPeerClient(job.Peer.Fingerprint)
	baseURL := "https://" + net.JoinHostPort(job.Peer.Address, strconv.Itoa(job.Peer.Port)) + "/api/peer/v1"
	payload, _ := json.Marshal(peerOfferRequest{Sender: peerIdentity{Name: m.name, Fingerprint: m.fingerprint, Port: m.port}, Item: job.Item, Entries: job.Entries})
	var offered struct {
		OfferID string `json:"offerId"`
		Token   string `json:"token"`
	}
	if err := peerJSON(ctx, client, http.MethodPost, baseURL+"/offers", "", bytes.NewReader(payload), int64(len(payload)), &offered); err != nil || !validPeerID(offered.OfferID) || !validPeerID(offered.Token) {
		m.failOutgoing(job, peerError(err, "receiver rejected the offer"))
		return
	}
	m.updateOutgoing(job, func() {
		job.Status, job.decisionID, job.decisionToken = "waiting", offered.OfferID, offered.Token
	})

	for {
		var decision struct {
			Status    string `json:"status"`
			SessionID string `json:"sessionId"`
			Token     string `json:"token"`
		}
		if err := peerJSON(ctx, client, http.MethodGet, baseURL+"/offers/"+offered.OfferID, offered.Token, nil, 0, &decision); err != nil {
			m.failOutgoing(job, peerError(err, "offer status failed"))
			return
		}
		switch decision.Status {
		case "pending":
			timer := time.NewTimer(peerDecisionPoll)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				m.finishCanceled(job)
				return
			}
		case "declined", "expired":
			m.updateOutgoing(job, func() { job.Status = "declined" })
			return
		case "accepted":
			if !validPeerID(decision.SessionID) || !validPeerID(decision.Token) {
				m.failOutgoing(job, "receiver returned invalid transfer credentials")
				return
			}
			m.updateOutgoing(job, func() {
				job.Status, job.sessionID, job.sessionToken = "sending", decision.SessionID, decision.Token
			})
			if err := m.uploadOutgoing(ctx, client, baseURL, job); err != nil {
				if errors.Is(err, context.Canceled) {
					m.finishCanceled(job)
				} else {
					m.failOutgoing(job, err.Error())
				}
				return
			}
			var completed struct {
				Destination string `json:"destination"`
			}
			if err := peerJSON(ctx, client, http.MethodPost, baseURL+"/transfers/"+decision.SessionID, decision.Token, nil, 0, &completed); err != nil {
				m.failOutgoing(job, peerError(err, "receiver could not publish the transfer"))
				return
			}
			m.updateOutgoing(job, func() { job.Status, job.Destination = "complete", completed.Destination })
			return
		default:
			m.failOutgoing(job, "receiver returned an invalid offer status")
			return
		}
	}
}

func (m *peerManager) uploadOutgoing(ctx context.Context, client *http.Client, baseURL string, job *peerOutgoing) error {
	for _, entry := range job.Entries {
		if entry.Type != "file" {
			continue
		}
		publicPath := job.Source
		if entry.Path != "." {
			publicPath = path.Join(job.Source, entry.Path)
		}
		file, info, err := m.server.peerFiles.OpenRegular(publicPath)
		if err != nil {
			return errors.New("source file became unavailable")
		}
		if info.Size() != entry.Size {
			file.Close()
			return errors.New("source file changed during transfer")
		}
		reader := &peerCountingReader{Reader: file, progress: func(count int64) { m.addOutgoingProgress(job, count) }}
		err = peerJSON(ctx, client, http.MethodPut, baseURL+"/transfers/"+job.sessionID+"/files/"+entry.ID,
			job.sessionToken, reader, entry.Size, nil)
		closeErr := file.Close()
		if err != nil || closeErr != nil {
			return errors.New(peerError(errors.Join(err, closeErr), "file transfer failed"))
		}
	}
	return nil
}

type peerCountingReader struct {
	io.Reader
	progress func(int64)
}

func (r *peerCountingReader) Read(buffer []byte) (int, error) {
	count, err := r.Reader.Read(buffer)
	if count > 0 {
		r.progress(int64(count))
	}
	return count, err
}

func peerJSON(ctx context.Context, client *http.Client, method, target, token string, body io.Reader, length int64, destination any) error {
	request, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return err
	}
	request.ContentLength = length
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 8<<10))
		return fmt.Errorf("peer returned HTTP %d", response.StatusCode)
	}
	if destination != nil {
		decoder := json.NewDecoder(io.LimitReader(response.Body, peerMaxMetadata))
		if err := decoder.Decode(destination); err != nil {
			return err
		}
	}
	return nil
}

func peerError(err error, fallback string) string {
	if err == nil {
		return fallback
	}
	if errors.Is(err, context.Canceled) {
		return "transfer canceled"
	}
	return fallback
}

func (m *peerManager) updateOutgoing(job *peerOutgoing, update func()) {
	m.mu.Lock()
	if m.outgoing == job {
		update()
		m.publishEventsLocked()
	}
	m.mu.Unlock()
}

func (m *peerManager) addOutgoingProgress(job *peerOutgoing, count int64) {
	m.server.touch()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.outgoing != job {
		return
	}
	job.SentBytes += count
	now := time.Now()
	if job.SentBytes == job.Item.Bytes || job.SentBytes-job.lastEventBytes >= 1<<20 || job.lastEvent.IsZero() || now.Sub(job.lastEvent) >= 250*time.Millisecond {
		job.lastEventBytes, job.lastEvent = job.SentBytes, now
		m.publishEventsLocked()
	}
}

func (m *peerManager) failOutgoing(job *peerOutgoing, message string) {
	m.updateOutgoing(job, func() { job.Status, job.Error = "error", message })
}

func (m *peerManager) finishCanceled(job *peerOutgoing) {
	m.updateOutgoing(job, func() { job.Status = "canceled" })
}

func (m *peerManager) cancel(jobID string) error {
	m.mu.Lock()
	if offer := m.offers[jobID]; offer != nil && m.incoming == jobID && offer.Status == "accepted" {
		m.cleanupOfferLocked(offer)
		delete(m.offers, offer.ID)
		if m.incomingEvent != nil && m.incomingEvent.ID == offer.ID {
			m.incomingEvent.Status = "canceled"
		}
		m.publishEventsLocked()
		m.mu.Unlock()
		return nil
	}
	job := m.outgoing
	if job == nil || job.ID != jobID || terminalPeerStatus(job.Status) {
		m.mu.Unlock()
		return errors.New("transfer is unavailable")
	}
	job.cancel()
	peer, offerID, decisionToken, sessionID, sessionToken := job.Peer, job.decisionID, job.decisionToken, job.sessionID, job.sessionToken
	job.Status = "canceled"
	m.publishEventsLocked()
	m.mu.Unlock()
	go func() {
		client := pinnedPeerClient(peer.Fingerprint)
		baseURL := "https://" + net.JoinHostPort(peer.Address, strconv.Itoa(peer.Port)) + "/api/peer/v1"
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if sessionID != "" {
			_ = peerJSON(ctx, client, http.MethodDelete, baseURL+"/transfers/"+sessionID, sessionToken, nil, 0, nil)
		} else if offerID != "" {
			_ = peerJSON(ctx, client, http.MethodDelete, baseURL+"/offers/"+offerID, decisionToken, nil, 0, nil)
		}
	}()
	return nil
}

func (s *Server) PeerCommand(command string) error {
	if s.peer == nil {
		return errors.New("peer sharing requires HTTPS")
	}
	if !s.peer.ready() {
		return errors.New("owner setup is incomplete")
	}
	s.touch()
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return errors.New("invalid peer command")
	}
	switch parts[0] {
	case "peer-discover":
		if len(parts) != 2 {
			return errors.New("invalid discover command")
		}
		return s.peer.discover(parts[1])
	case "peer-send":
		if len(parts) != 4 {
			return errors.New("invalid send command")
		}
		return s.peer.send(parts[1], parts[2], parts[3])
	case "peer-accept":
		if len(parts) != 2 || !validPeerID(parts[1]) {
			return errors.New("invalid accept command")
		}
		return s.peer.accept(parts[1])
	case "peer-decline":
		if len(parts) != 2 || !validPeerID(parts[1]) {
			return errors.New("invalid decline command")
		}
		return s.peer.decline(parts[1])
	case "peer-cancel":
		if len(parts) != 2 || !validPeerID(parts[1]) {
			return errors.New("invalid cancel command")
		}
		return s.peer.cancel(parts[1])
	case "peer-status":
		if len(parts) != 1 {
			return errors.New("invalid status command")
		}
		return nil
	default:
		return errors.New("unknown peer command")
	}
}
