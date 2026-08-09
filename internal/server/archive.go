package server

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/xZenLabs/zen-fm/internal/auth"
	zenfiles "github.com/xZenLabs/zen-fm/internal/files"
)

const (
	maxArchiveEntries = 10_000
	maxArchiveBytes   = int64(2 << 30)
	maxArchiveTickets = 8
	archiveTicketTTL  = time.Minute
)

type archiveRequest struct {
	Paths  []string `json:"paths"`
	Format string   `json:"format"`
}

type archiveTicket struct {
	request   archiveRequest
	sessionID string
	expiresAt time.Time
}

type archiveItem struct {
	source string
	name   string
	entry  zenfiles.Entry
}

func (s *Server) archiveFiles(w http.ResponseWriter, r *http.Request) {
	var request archiveRequest
	if err := readJSON(w, r, &request, 1<<20); err != nil {
		problem(w, r, http.StatusBadRequest, "Invalid Request", "invalid archive request")
		return
	}
	if !validateArchiveRequest(w, r, request) {
		return
	}
	s.streamArchive(w, r, request)
}

func validateArchiveRequest(w http.ResponseWriter, r *http.Request, request archiveRequest) bool {
	if len(request.Paths) == 0 || len(request.Paths) > maxArchiveEntries {
		problem(w, r, http.StatusBadRequest, "Invalid Request", "archive must contain 1 to 10000 paths")
		return false
	}
	if request.Format != "zip" && request.Format != "tar" && request.Format != "tar.gz" {
		problem(w, r, http.StatusBadRequest, "Invalid Request", "archive format must be zip, tar, or tar.gz")
		return false
	}
	return true
}

func (s *Server) createArchiveTicket(w http.ResponseWriter, r *http.Request) {
	var request archiveRequest
	if err := readJSON(w, r, &request, 1<<20); err != nil {
		problem(w, r, http.StatusBadRequest, "Invalid Request", "invalid archive request")
		return
	}
	if !validateArchiveRequest(w, r, request) {
		return
	}
	token, err := auth.RandomToken("zfm_archive_", 192)
	if err != nil {
		internalError(w, r, err)
		return
	}
	now := s.cfg.Now()
	s.archiveMu.Lock()
	for key, ticket := range s.archiveLinks {
		if !now.Before(ticket.expiresAt) {
			delete(s.archiveLinks, key)
		}
	}
	if len(s.archiveLinks) >= maxArchiveTickets {
		s.archiveMu.Unlock()
		w.Header().Set("Retry-After", "1")
		problem(w, r, http.StatusTooManyRequests, "Rate Limited", "too many pending archive downloads")
		return
	}
	paths := append([]string(nil), request.Paths...)
	s.archiveLinks[token] = archiveTicket{
		request:   archiveRequest{Paths: paths, Format: request.Format},
		sessionID: principalFrom(r).session.ID,
		expiresAt: now.Add(archiveTicketTTL),
	}
	s.archiveMu.Unlock()
	writeJSON(w, http.StatusCreated, map[string]string{"url": "/api/v1/files/archive/" + token})
}

func (s *Server) downloadArchiveTicket(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("ticket")
	now := s.cfg.Now()
	sessionID := principalFrom(r).session.ID
	s.archiveMu.Lock()
	ticket, exists := s.archiveLinks[token]
	if exists && !now.Before(ticket.expiresAt) {
		delete(s.archiveLinks, token)
		exists = false
	}
	if exists && ticket.sessionID == sessionID {
		delete(s.archiveLinks, token)
	} else if exists {
		exists = false
	}
	s.archiveMu.Unlock()
	if !exists {
		problem(w, r, http.StatusNotFound, "Not Found", "archive download is unavailable or expired")
		return
	}
	s.streamArchive(w, r, ticket.request)
}

func (s *Server) streamArchive(w http.ResponseWriter, r *http.Request, request archiveRequest) {
	if !s.acquireHeavy(w, r) {
		return
	}
	defer s.releaseHeavy()
	items, err := s.archiveManifest(r.Context(), request.Paths)
	if err != nil {
		mapError(w, r, err)
		return
	}
	if len(items) == 0 {
		problem(w, r, http.StatusBadRequest, "Invalid Request", "archive contains no safe regular files or directories")
		return
	}
	filename := "zenfm." + request.Format
	contentType := "application/x-tar"
	if request.Format == "zip" {
		contentType = "application/zip"
	} else if request.Format == "tar.gz" {
		contentType = "application/gzip"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	stream := &progressResponseWriter{ResponseWriter: w, timeout: progressTimeout, touch: s.touch}
	if err := s.writeArchive(r.Context(), stream, request.Format, items); err != nil {
		// Headers may already be committed. Returning truncation is safer than
		// appending a JSON error to an archive stream.
		return
	}
}

func (s *Server) archiveManifest(ctx context.Context, selected []string) ([]archiveItem, error) {
	seenSources := make(map[string]bool)
	seenNames := make(map[string]bool)
	items := make([]archiveItem, 0)
	var total int64
	var add func(string, string) error
	add = func(source, archiveName string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(items) >= maxArchiveEntries {
			return zenfiles.ErrWalkLimit
		}
		if s.cfg.Files.Pseudo(source) {
			return nil
		}
		entry, err := s.cfg.Files.Entry(source)
		if err != nil {
			return err
		}
		if entry.Symlink || (!entry.Regular && !entry.Directory) {
			return nil
		}
		if err := validateArchiveName(archiveName); err != nil {
			return err
		}
		if seenNames[archiveName] {
			return zenfiles.ErrConflict
		}
		seenNames[archiveName] = true
		items = append(items, archiveItem{source: source, name: archiveName, entry: entry})
		if entry.Regular {
			if entry.Size > maxArchiveBytes-total {
				return zenfiles.ErrTooLarge
			}
			total += entry.Size
			return nil
		}
		listing, err := s.cfg.Files.List(source, true)
		if err != nil {
			return err
		}
		for _, child := range listing.Entries {
			if child.Symlink || (!child.Regular && !child.Directory) || s.cfg.Files.Pseudo(child.Path) {
				continue
			}
			if err := add(child.Path, path.Join(archiveName, child.Name)); err != nil {
				return err
			}
		}
		return nil
	}
	for _, source := range selected {
		clean, err := zenfiles.Normalize(source)
		if err != nil || clean == "." {
			return nil, zenfiles.ErrInvalidPath
		}
		if seenSources[clean] {
			return nil, zenfiles.ErrConflict
		}
		seenSources[clean] = true
		if err := add(zenfiles.PublicPath(clean), path.Base(clean)); err != nil {
			return nil, err
		}
	}
	return items, nil
}

func validateArchiveName(name string) error {
	if name == "" || strings.Contains(name, "\\") || strings.ContainsRune(name, 0) || path.IsAbs(name) || path.Clean(name) != name {
		return zenfiles.ErrInvalidPath
	}
	for _, component := range strings.Split(name, "/") {
		if component == "" || component == "." || component == ".." {
			return zenfiles.ErrInvalidPath
		}
	}
	return nil
}

func (s *Server) writeArchive(ctx context.Context, destination io.Writer, format string, items []archiveItem) error {
	switch format {
	case "zip":
		writer := zip.NewWriter(destination)
		err := s.writeZIP(ctx, writer, items)
		return errors.Join(err, writer.Close())
	case "tar":
		writer := tar.NewWriter(destination)
		err := s.writeTAR(ctx, writer, items)
		return errors.Join(err, writer.Close())
	case "tar.gz":
		gzipWriter, err := gzip.NewWriterLevel(destination, gzip.BestSpeed)
		if err != nil {
			return err
		}
		tarWriter := tar.NewWriter(gzipWriter)
		writeErr := s.writeTAR(ctx, tarWriter, items)
		return errors.Join(writeErr, tarWriter.Close(), gzipWriter.Close())
	default:
		return fmt.Errorf("unsupported archive format %q", format)
	}
}

func (s *Server) writeZIP(ctx context.Context, writer *zip.Writer, items []archiveItem) error {
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return err
		}
		header := &zip.FileHeader{Name: item.name, Method: zip.Deflate, Modified: item.entry.ModifiedAt}
		if item.entry.Directory {
			header.Name += "/"
			header.Method = zip.Store
			header.SetMode(fs.ModeDir | 0o750)
		} else {
			header.SetMode(0o640)
		}
		entryWriter, err := writer.CreateHeader(header)
		if err != nil {
			return err
		}
		if item.entry.Regular {
			if err := s.copyArchiveFile(ctx, entryWriter, item); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Server) writeTAR(ctx context.Context, writer *tar.Writer, items []archiveItem) error {
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return err
		}
		header := &tar.Header{Name: item.name, Mode: 0o640, Size: item.entry.Size, ModTime: item.entry.ModifiedAt, Typeflag: tar.TypeReg, Format: tar.FormatPAX}
		if item.entry.Directory {
			header.Name += "/"
			header.Mode, header.Size, header.Typeflag = 0o750, 0, tar.TypeDir
		}
		if err := writer.WriteHeader(header); err != nil {
			return err
		}
		if item.entry.Regular {
			if err := s.copyArchiveFile(ctx, writer, item); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Server) copyArchiveFile(ctx context.Context, writer io.Writer, item archiveItem) error {
	file, info, err := s.cfg.Files.OpenRegular(item.source)
	if err != nil {
		return err
	}
	defer file.Close()
	if info.Size() != item.entry.Size {
		return zenfiles.ErrConflict
	}
	written, err := copyWithContext(ctx, writer, io.LimitReader(file, item.entry.Size))
	if err != nil {
		return err
	}
	if written != item.entry.Size {
		return io.ErrUnexpectedEOF
	}
	return nil
}
