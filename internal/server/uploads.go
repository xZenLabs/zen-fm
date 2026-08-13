package server

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/xZenLabs/zen-fm/internal/auth"
	zenfiles "github.com/xZenLabs/zen-fm/internal/files"
	"github.com/xZenLabs/zen-fm/internal/platform"
	"github.com/xZenLabs/zen-fm/internal/state"
)

const (
	tusVersion               = "1.0.0"
	defaultMaxUpload         = int64(2 << 30)
	defaultUploadExpiry      = 24 * time.Hour
	defaultUploadConcurrency = 4
	uploadDirectoryName      = ".zenfm-internal-uploads"
	diskReserve              = uint64(8 << 20)
	diskCheckReserve         = diskReserve * 2
	progressTimeout          = 30 * time.Second
)

var chmodUploadDirectory = os.Chmod

type uploadManager struct {
	server       *Server
	root         *os.Root
	capacity     *zenfiles.Root
	ownsCapacity bool
	maxLength    int64
	expiry       time.Duration
	slots        chan struct{}
	mu           sync.Mutex
	locks        map[string]*uploadLock
	createMu     sync.Mutex
	maxActive    int
	closeOnce    sync.Once
	// finalizeReader is a test seam for deterministic cancellation in the
	// streaming fallback. It is nil in production.
	finalizeReader func(io.Reader) io.Reader
}

type uploadLock struct {
	mutex sync.Mutex
	refs  int
}

func newUploadManager(server *Server, dir string, maxLength int64, expiry time.Duration, concurrency, maxActive int) (*uploadManager, error) {
	if maxLength <= 0 {
		maxLength = defaultMaxUpload
	}
	if rootLimit := server.cfg.Files.MaxWriteBytes(); maxLength > rootLimit {
		maxLength = rootLimit
	}
	if expiry <= 0 {
		expiry = defaultUploadExpiry
	}
	if concurrency <= 0 {
		concurrency = defaultUploadConcurrency
	}
	if concurrency > 16 {
		concurrency = 16
	}
	if maxActive <= 0 {
		maxActive = 64
	}
	if maxActive > 1024 {
		maxActive = 1024
	}
	var root *os.Root
	var capacity *zenfiles.Root
	ownsCapacity := false
	var err error
	if dir == "" {
		legacyDir := filepath.Join(server.cfg.Store.DataDir(), "uploads")
		legacyInUse, legacyErr := legacyUploadDirectoryInUse(server.cfg.Store, legacyDir, server.cfg.Now())
		if legacyErr != nil {
			return nil, legacyErr
		}
		if legacyInUse {
			dir = legacyDir
		} else {
			root, dir, err = server.cfg.Files.OpenInternalDirectory(uploadDirectoryName)
			capacity = server.cfg.Files
			if err != nil {
				dir = legacyDir
			} else {
				cleanupLegacyUploadDirectory(legacyDir, dir)
			}
		}
	}
	if root == nil {
		if err = os.MkdirAll(dir, 0o700); err == nil {
			err = platform.ModeChangeError(chmodUploadDirectory(dir, 0o700), server.cfg.ModeLessFilesystem)
		}
		if err == nil {
			root, err = os.OpenRoot(dir)
		}
		if err == nil {
			capacity, err = zenfiles.Open(dir, zenfiles.Options{})
			ownsCapacity = err == nil
		}
	}
	if err != nil {
		if root != nil {
			root.Close()
		}
		return nil, err
	}
	server.cfg.UploadDir = dir
	m := &uploadManager{server: server, root: root, capacity: capacity, ownsCapacity: ownsCapacity, maxLength: maxLength, expiry: expiry, slots: make(chan struct{}, concurrency), locks: make(map[string]*uploadLock), maxActive: maxActive}
	if err := m.startupCleanup(); err != nil {
		if ownsCapacity {
			capacity.Close()
		}
		root.Close()
		return nil, err
	}
	return m, nil
}

func legacyUploadDirectoryInUse(store *state.Store, legacyDir string, now time.Time) (bool, error) {
	uploads, err := store.Uploads()
	if err != nil {
		return false, err
	}
	for _, upload := range uploads {
		if !validUploadID(upload.ID) || now.Unix() >= upload.ExpiresAt {
			continue
		}
		info, statErr := os.Lstat(filepath.Join(legacyDir, partialName(upload.ID)))
		if statErr == nil && info.Mode().IsRegular() && info.Size() >= upload.Offset {
			return true, nil
		}
		if statErr != nil && !errors.Is(statErr, fs.ErrNotExist) {
			return false, statErr
		}
	}
	return false, nil
}

func cleanupLegacyUploadDirectory(legacyDir, activeDir string) {
	legacyAbs, legacyErr := filepath.Abs(legacyDir)
	activeAbs, activeErr := filepath.Abs(activeDir)
	if legacyErr != nil || activeErr != nil || legacyAbs == activeAbs {
		return
	}
	entries, err := os.ReadDir(legacyAbs)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if validPartialName(entry.Name()) {
			_ = os.Remove(filepath.Join(legacyAbs, entry.Name()))
		}
	}
	_ = os.Remove(legacyAbs)
}

func (m *uploadManager) startupCleanup() error {
	if err := m.pruneExpired(); err != nil {
		return err
	}
	active, err := m.server.cfg.Store.Uploads()
	if err != nil {
		return err
	}
	wanted := make(map[string]bool, len(active))
	for _, upload := range active {
		partial, openErr := m.root.OpenFile(partialName(upload.ID), os.O_WRONLY|uploadOpenFlags(), 0)
		if openErr != nil {
			_, _ = m.server.cfg.Store.DeleteUpload(upload.ID)
			continue
		}
		info, statErr := partial.Stat()
		if statErr != nil || !info.Mode().IsRegular() || info.Size() < upload.Offset {
			_ = partial.Close()
			_, _ = m.server.cfg.Store.DeleteUpload(upload.ID)
			_ = m.removePartial(upload.ID)
			continue
		}
		if info.Size() > upload.Offset {
			_ = partial.Truncate(upload.Offset)
		}
		_ = partial.Close()
		wanted[partialName(upload.ID)] = true
	}
	directory, err := m.root.Open(".")
	if err != nil {
		return err
	}
	entries, err := directory.ReadDir(-1)
	closeErr := directory.Close()
	if err != nil || closeErr != nil {
		return errors.Join(err, closeErr)
	}
	for _, entry := range entries {
		name := entry.Name()
		if validPartialName(name) && !wanted[name] {
			if err := m.root.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}

func (m *uploadManager) pruneExpired() error {
	expired, err := m.server.cfg.Store.PruneExpired()
	if err != nil {
		return err
	}
	for _, upload := range expired {
		unlock := m.lock(upload.ID)
		err := m.removePartial(upload.ID)
		unlock()
		if err != nil {
			return err
		}
	}
	return nil
}

func (m *uploadManager) close() {
	m.closeOnce.Do(func() {
		if m.ownsCapacity {
			_ = m.capacity.Close()
		}
		_ = m.root.Close()
	})
}

func (m *uploadManager) lock(id string) func() {
	m.mu.Lock()
	entry := m.locks[id]
	if entry == nil {
		entry = &uploadLock{}
		m.locks[id] = entry
	}
	entry.refs++
	m.mu.Unlock()
	entry.mutex.Lock()
	return func() {
		entry.mutex.Unlock()
		m.mu.Lock()
		entry.refs--
		if entry.refs == 0 && m.locks[id] == entry {
			delete(m.locks, id)
		}
		m.mu.Unlock()
	}
}

func (m *uploadManager) acquire() bool {
	select {
	case m.slots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (m *uploadManager) release() { <-m.slots }

func (m *uploadManager) ensureDisk(required uint64) error {
	return m.ensureDiskWithReserve(required, diskReserve)
}

func (m *uploadManager) ensureDiskWithReserve(required, reserve uint64) error {
	usage, err := m.capacity.Usage()
	if err != nil {
		return err
	}
	available := uint64(0)
	if usage.Total > usage.Used {
		available = usage.Total - usage.Used
	}
	if required > ^uint64(0)-reserve || available < required+reserve {
		return zenfiles.ErrTooLarge
	}
	return nil
}

func (m *uploadManager) removePartial(id string) error {
	if !validUploadID(id) {
		return zenfiles.ErrInvalidPath
	}
	err := m.root.Remove(partialName(id))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

func (s *Server) uploadOptions(w http.ResponseWriter, _ *http.Request) {
	tusHeaders(w, s.uploads.maxLength)
	w.Header().Set("Tus-Version", tusVersion)
	w.Header().Set("Tus-Extension", "creation,termination")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) createUpload(w http.ResponseWriter, r *http.Request) {
	tusHeaders(w, s.uploads.maxLength)
	if !validTusRequest(r) {
		problem(w, r, http.StatusPreconditionFailed, "TUS Version Required", "Tus-Resumable must be 1.0.0")
		return
	}
	length, err := strconv.ParseInt(r.Header.Get("Upload-Length"), 10, 64)
	if err != nil || length < 0 {
		problem(w, r, http.StatusBadRequest, "Invalid Upload", "Upload-Length must be a non-negative integer")
		return
	}
	if length > s.uploads.maxLength {
		problem(w, r, http.StatusRequestEntityTooLarge, "Too Large", "declared upload length exceeds the configured maximum")
		return
	}
	s.uploads.createMu.Lock()
	defer s.uploads.createMu.Unlock()
	if err := s.uploads.pruneExpired(); err != nil {
		internalError(w, r, err)
		return
	}
	active, err := s.cfg.Store.Uploads()
	if err != nil {
		internalError(w, r, err)
		return
	}
	if len(active) >= s.uploads.maxActive {
		w.Header().Set("Retry-After", "60")
		problem(w, r, http.StatusTooManyRequests, "Rate Limited", "too many active uploads")
		return
	}
	metadata, err := parseUploadMetadata(r.Header.Get("Upload-Metadata"))
	if err != nil {
		problem(w, r, http.StatusBadRequest, "Invalid Upload", err.Error())
		return
	}
	target := metadata["path"]
	overwrite := false
	if value, exists := metadata["overwrite"]; exists {
		if value != "true" && value != "false" {
			problem(w, r, http.StatusBadRequest, "Invalid Upload", "overwrite metadata must be true or false")
			return
		}
		overwrite = value == "true"
	}
	clean, err := zenfiles.Normalize(target)
	if err != nil || clean == "." {
		problem(w, r, http.StatusBadRequest, "Invalid Path", "upload path is invalid")
		return
	}
	if existing, err := s.cfg.Files.Entry(clean); err == nil {
		if !overwrite {
			problem(w, r, http.StatusConflict, "Conflict", "upload destination already exists")
			return
		}
		if !existing.Regular {
			problem(w, r, http.StatusUnsupportedMediaType, "Unsupported File", "only regular files can be explicitly replaced")
			return
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		mapError(w, r, err)
		return
	}
	if err := s.uploads.ensureDisk(uint64(length)); err != nil {
		mapError(w, r, err)
		return
	}
	if err := ensureFilesystemSpace(s.cfg.Files, uint64(length)); err != nil {
		mapError(w, r, err)
		return
	}
	id, err := auth.RandomToken("", 128)
	if err != nil {
		internalError(w, r, err)
		return
	}
	partial, err := s.uploads.root.OpenFile(partialName(id), os.O_WRONLY|os.O_CREATE|os.O_EXCL|uploadOpenFlags(), 0o600)
	if err != nil {
		internalError(w, r, err)
		return
	}
	if err := partial.Close(); err != nil {
		_ = s.uploads.removePartial(id)
		internalError(w, r, err)
		return
	}
	now := s.cfg.Now()
	upload := state.Upload{ID: id, Path: zenfiles.PublicPath(clean), Length: length, CreatedAt: now.Unix(), ExpiresAt: now.Add(s.uploads.expiry).Unix(), Overwrite: overwrite}
	if err := s.cfg.Store.CreateUpload(upload); err != nil {
		_ = s.uploads.removePartial(id)
		internalError(w, r, err)
		return
	}
	if length == 0 {
		if err := s.finalizeUpload(r.Context(), upload); err != nil {
			_, _ = s.cfg.Store.DeleteUpload(id)
			_ = s.uploads.removePartial(id)
			mapError(w, r, err)
			return
		}
		_, _ = s.cfg.Store.DeleteUpload(id)
		_ = s.uploads.removePartial(id)
	}
	w.Header().Set("Location", "/api/v1/uploads/"+id)
	w.Header().Set("Upload-Expires", time.Unix(upload.ExpiresAt, 0).UTC().Format(http.TimeFormat))
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) headUpload(w http.ResponseWriter, r *http.Request) {
	tusHeaders(w, s.uploads.maxLength)
	if !validTusRequest(r) {
		problem(w, r, http.StatusPreconditionFailed, "TUS Version Required", "Tus-Resumable must be 1.0.0")
		return
	}
	upload, err := s.cfg.Store.Upload(r.PathValue("uploadId"))
	if err != nil {
		uploadNotFound(w, r)
		return
	}
	w.Header().Set("Upload-Length", strconv.FormatInt(upload.Length, 10))
	w.Header().Set("Upload-Offset", strconv.FormatInt(upload.Offset, 10))
	w.Header().Set("Upload-Expires", time.Unix(upload.ExpiresAt, 0).UTC().Format(http.TimeFormat))
	w.WriteHeader(http.StatusOK)
}

func (s *Server) patchUpload(w http.ResponseWriter, r *http.Request) {
	tusHeaders(w, s.uploads.maxLength)
	if !validTusRequest(r) {
		problem(w, r, http.StatusPreconditionFailed, "TUS Version Required", "Tus-Resumable must be 1.0.0")
		return
	}
	if strings.SplitN(r.Header.Get("Content-Type"), ";", 2)[0] != "application/offset+octet-stream" {
		problem(w, r, http.StatusUnsupportedMediaType, "Unsupported Media Type", "TUS chunks require application/offset+octet-stream")
		return
	}
	requestedOffset, err := strconv.ParseInt(r.Header.Get("Upload-Offset"), 10, 64)
	if err != nil || requestedOffset < 0 {
		problem(w, r, http.StatusBadRequest, "Invalid Upload", "Upload-Offset is invalid")
		return
	}
	id := r.PathValue("uploadId")
	if !validUploadID(id) {
		uploadNotFound(w, r)
		return
	}
	if !s.uploads.acquire() {
		w.Header().Set("Retry-After", "1")
		problem(w, r, http.StatusTooManyRequests, "Rate Limited", "too many concurrent uploads")
		return
	}
	defer s.uploads.release()
	unlock := s.uploads.lock(id)
	defer unlock()
	upload, err := s.cfg.Store.Upload(id)
	if err != nil {
		_ = s.uploads.removePartial(id)
		uploadNotFound(w, r)
		return
	}
	if requestedOffset != upload.Offset {
		w.Header().Set("Upload-Offset", strconv.FormatInt(upload.Offset, 10))
		problem(w, r, http.StatusConflict, "Offset Conflict", "Upload-Offset does not match server state")
		return
	}
	remaining := upload.Length - upload.Offset
	if r.ContentLength > remaining {
		problem(w, r, http.StatusRequestEntityTooLarge, "Too Large", "chunk exceeds declared upload length")
		return
	}
	if r.ContentLength > 0 {
		if err := s.uploads.ensureDisk(uint64(r.ContentLength)); err != nil {
			mapError(w, r, err)
			return
		}
	}
	partial, err := s.uploads.root.OpenFile(partialName(id), os.O_WRONLY|os.O_APPEND|uploadOpenFlags(), 0)
	if err != nil {
		uploadNotFound(w, r)
		return
	}
	info, err := partial.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != upload.Offset {
		_ = partial.Close()
		problem(w, r, http.StatusConflict, "Upload State Conflict", "partial upload state is inconsistent")
		return
	}
	reader := &progressReader{writer: w, reader: io.LimitReader(r.Body, remaining+1), timeout: progressTimeout, context: r.Context(), touch: s.touch}
	writer := &diskCheckedWriter{manager: s.uploads, writer: partial}
	written, copyErr := io.CopyBuffer(writer, reader, make([]byte, 128*1024))
	if copyErr == nil {
		copyErr = partial.Sync()
	}
	closeErr := partial.Close()
	if copyErr != nil || closeErr != nil || written > remaining {
		if file, openErr := s.uploads.root.OpenFile(partialName(id), os.O_WRONLY|uploadOpenFlags(), 0); openErr == nil {
			_ = file.Truncate(upload.Offset)
			_ = file.Close()
		}
		if written > remaining || errors.Is(copyErr, zenfiles.ErrTooLarge) {
			problem(w, r, http.StatusRequestEntityTooLarge, "Too Large", "chunk exceeds upload or disk limit")
		} else {
			problem(w, r, http.StatusRequestTimeout, "Upload Interrupted", "chunk made no progress or was interrupted")
		}
		return
	}
	newOffset := upload.Offset + written
	if newOffset == upload.Length {
		complete := upload
		complete.Offset = newOffset
		if err := s.finalizeUpload(r.Context(), complete); err != nil {
			s.truncateUploadPartial(id, upload.Offset)
			mapError(w, r, err)
			return
		}
		_, _ = s.cfg.Store.DeleteUpload(id)
		_ = s.uploads.removePartial(id)
	} else {
		if _, err := s.cfg.Store.AdvanceUpload(id, upload.Offset, newOffset); err != nil {
			s.truncateUploadPartial(id, upload.Offset)
			internalError(w, r, err)
			return
		}
	}
	w.Header().Set("Upload-Offset", strconv.FormatInt(newOffset, 10))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) truncateUploadPartial(id string, offset int64) {
	if file, err := s.uploads.root.OpenFile(partialName(id), os.O_WRONLY|uploadOpenFlags(), 0); err == nil {
		_ = file.Truncate(offset)
		_ = file.Close()
	}
}

func ensureFilesystemSpace(root *zenfiles.Root, required uint64) error {
	usage, err := root.Usage()
	if err != nil {
		return err
	}
	available := uint64(0)
	if usage.Total > usage.Used {
		available = usage.Total - usage.Used
	}
	if required > ^uint64(0)-diskReserve || available < required+diskReserve {
		return zenfiles.ErrTooLarge
	}
	return nil
}

func (s *Server) finalizeUpload(ctx context.Context, upload state.Upload) error {
	partial, err := s.uploads.root.OpenFile(partialName(upload.ID), os.O_RDONLY|uploadOpenFlags(), 0)
	if err != nil {
		return err
	}
	info, err := partial.Stat()
	if err != nil {
		partial.Close()
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != upload.Length {
		partial.Close()
		return state.ErrConflict
	}
	if err := ctx.Err(); err != nil {
		partial.Close()
		return err
	}
	if s.uploads.finalizeReader == nil {
		if err := partial.Close(); err != nil {
			return err
		}
		moved, err := s.cfg.Files.PublishTemporary(s.uploads.root, partialName(upload.ID), upload.Path, upload.Overwrite)
		if err != nil || moved {
			return err
		}
		if err := ensureFilesystemSpace(s.cfg.Files, uint64(upload.Length)); err != nil {
			return err
		}
		partial, err = s.uploads.root.OpenFile(partialName(upload.ID), os.O_RDONLY|uploadOpenFlags(), 0)
		if err != nil {
			return err
		}
	}
	defer partial.Close()
	var source io.Reader = partial
	if s.uploads.finalizeReader != nil {
		source = s.uploads.finalizeReader(source)
	}
	reader := &progressReader{reader: source, context: ctx, touch: s.touch}
	_, err = s.cfg.Files.WriteContext(ctx, upload.Path, reader, upload.Overwrite)
	return err
}

func (s *Server) cancelUpload(w http.ResponseWriter, r *http.Request) {
	tusHeaders(w, s.uploads.maxLength)
	if !validTusRequest(r) {
		problem(w, r, http.StatusPreconditionFailed, "TUS Version Required", "Tus-Resumable must be 1.0.0")
		return
	}
	id := r.PathValue("uploadId")
	if !validUploadID(id) {
		uploadNotFound(w, r)
		return
	}
	unlock := s.uploads.lock(id)
	defer unlock()
	if _, err := s.cfg.Store.Upload(id); err != nil {
		uploadNotFound(w, r)
		return
	}
	if err := s.uploads.removePartial(id); err != nil {
		internalError(w, r, err)
		return
	}
	deleted, err := s.cfg.Store.DeleteUpload(id)
	if err != nil {
		internalError(w, r, err)
		return
	}
	if !deleted {
		uploadNotFound(w, r)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func tusHeaders(w http.ResponseWriter, maxLength int64) {
	w.Header().Set("Tus-Resumable", tusVersion)
	w.Header().Set("Tus-Max-Size", strconv.FormatInt(maxLength, 10))
	w.Header().Set("Cache-Control", "no-store")
}

func validTusRequest(r *http.Request) bool { return r.Header.Get("Tus-Resumable") == tusVersion }

func parseUploadMetadata(header string) (map[string]string, error) {
	if len(header) > 8<<10 {
		return nil, errors.New("Upload-Metadata is too large")
	}
	values := make(map[string]string)
	if strings.TrimSpace(header) == "" {
		return values, nil
	}
	for _, field := range strings.Split(header, ",") {
		parts := strings.Fields(strings.TrimSpace(field))
		if len(parts) != 2 || !validMetadataKey(parts[0]) {
			return nil, errors.New("Upload-Metadata is invalid")
		}
		if _, duplicate := values[parts[0]]; duplicate {
			return nil, errors.New("Upload-Metadata contains a duplicate key")
		}
		decoded, err := base64.StdEncoding.DecodeString(parts[1])
		if err != nil || len(decoded) > 4096 || strings.ContainsRune(string(decoded), 0) {
			return nil, errors.New("Upload-Metadata value is invalid")
		}
		values[parts[0]] = string(decoded)
	}
	return values, nil
}

func validMetadataKey(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if character < 'a' || character > 'z' {
			return false
		}
	}
	return true
}

func validUploadID(id string) bool {
	if len(id) < 16 || len(id) > 64 {
		return false
	}
	for _, character := range id {
		if character != '-' && character != '_' && (character < 'A' || character > 'Z') && (character < 'a' || character > 'z') && (character < '0' || character > '9') {
			return false
		}
	}
	return true
}

func partialName(id string) string { return id + ".part" }

func validPartialName(name string) bool {
	return strings.HasSuffix(name, ".part") && validUploadID(strings.TrimSuffix(name, ".part"))
}

func uploadNotFound(w http.ResponseWriter, r *http.Request) {
	problem(w, r, http.StatusNotFound, "Not Found", "upload not found")
}

type diskCheckedWriter struct {
	manager *uploadManager
	writer  io.Writer
	since   int64
	checked bool
}

func (w *diskCheckedWriter) Write(p []byte) (int, error) {
	if diskCheckDue(w.checked, w.since, len(p), uploadDiskCheckStride(cap(w.manager.slots))) {
		if err := w.manager.ensureDiskWithReserve(uint64(len(p)), diskCheckReserve); err != nil {
			return 0, err
		}
		w.checked = true
		w.since = 0
	}
	n, err := w.writer.Write(p)
	w.since += int64(n)
	return n, err
}

type progressReader struct {
	writer  http.ResponseWriter
	reader  io.Reader
	timeout time.Duration
	context context.Context
	touch   func()
}

func (r *progressReader) Read(p []byte) (int, error) {
	if r.context != nil {
		if err := r.context.Err(); err != nil {
			return 0, err
		}
	}
	if r.writer != nil {
		controller := http.NewResponseController(r.writer)
		_ = controller.SetReadDeadline(time.Now().Add(r.timeout))
	}
	n, err := r.reader.Read(p)
	if n > 0 && r.touch != nil {
		r.touch()
	}
	return n, err
}

type diskCheckedReader struct {
	manager *uploadManager
	reader  io.Reader
	since   int64
	checked bool
}

func (r *diskCheckedReader) Read(p []byte) (int, error) {
	if diskCheckDue(r.checked, r.since, len(p), uploadDiskCheckStride(cap(r.manager.slots))) {
		if err := r.manager.ensureDiskWithReserve(uint64(len(p)), diskCheckReserve); err != nil {
			return 0, err
		}
		r.checked = true
		r.since = 0
	}
	n, err := r.reader.Read(p)
	r.since += int64(n)
	return n, err
}

func uploadDiskCheckStride(concurrency int) int64 {
	if concurrency <= 0 {
		concurrency = defaultUploadConcurrency
	}
	return max(int64(diskReserve/uint64(concurrency)), 1)
}

func diskCheckDue(checked bool, since int64, next int, stride int64) bool {
	if !checked || since >= stride {
		return true
	}
	return int64(next) >= stride-since
}
