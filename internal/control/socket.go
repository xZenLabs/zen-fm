// Package control implements the owner-only local lifecycle socket used by the
// KOReader plugin.
package control

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/xZenLabs/zen-fm/internal/platform"
)

var chmodControlSocket = os.Chmod

type Server struct {
	Path               string
	URL                string
	Fingerprint        string
	Stop               func()
	Command            func(string) error
	ModeLessFilesystem bool
	Logger             *log.Logger

	mu       sync.Mutex
	listener *net.UnixListener
	original os.FileInfo
}

func (s *Server) Run(ctx context.Context) error {
	if s.Path == "" || s.Stop == nil {
		return errors.New("control socket path and stop callback are required")
	}
	if err := removeStaleSocket(s.Path); err != nil {
		return err
	}
	address, err := net.ResolveUnixAddr("unix", s.Path)
	if err != nil {
		return err
	}
	listener, err := net.ListenUnix("unix", address)
	if err != nil {
		return fmt.Errorf("listen on control socket: %w", err)
	}
	if err := s.secureSocket(); err != nil {
		listener.Close()
		return err
	}
	info, err := os.Lstat(s.Path)
	if err != nil {
		listener.Close()
		return err
	}
	s.mu.Lock()
	s.listener, s.original = listener, info
	s.mu.Unlock()
	if s.Logger != nil {
		s.Logger.Printf("control socket ready: path=%q", s.Path)
	}
	defer s.closeAndClean()
	go func() {
		<-ctx.Done()
		s.mu.Lock()
		if s.listener != nil {
			_ = s.listener.Close()
		}
		s.mu.Unlock()
	}()
	for {
		connection, err := listener.AcceptUnix()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		s.handle(connection)
	}
}

func (s *Server) secureSocket() error {
	if err := platform.ModeChangeError(chmodControlSocket(s.Path, 0o600), s.ModeLessFilesystem); err != nil {
		return fmt.Errorf("secure control socket: %w", err)
	}
	return nil
}

func (s *Server) handle(connection *net.UnixConn) {
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(2 * time.Second))
	reader := bufio.NewReader(io.LimitReader(connection, 8193))
	line, err := reader.ReadString('\n')
	if err != nil || len(line) > 8192 {
		_, _ = io.WriteString(connection, "error invalid-command\n")
		return
	}
	command := strings.TrimSpace(line)
	action := command
	if separator := strings.IndexByte(action, ' '); separator >= 0 {
		action = action[:separator]
	}
	if s.Logger != nil {
		switch {
		case command == "stop":
			s.Logger.Printf("control request received: command=stop")
		case command == "status":
		case strings.HasPrefix(action, "peer-"):
			s.Logger.Printf("control request received: command=%s", action)
		default:
			s.Logger.Printf("control request received: invalid command")
		}
	}
	switch command {
	case "status":
		fingerprint := s.Fingerprint
		if fingerprint == "" {
			fingerprint = "-"
		}
		_, _ = fmt.Fprintf(connection, "ok running %s %s\n", s.URL, fingerprint)
	case "stop":
		_, _ = io.WriteString(connection, "ok stopping\n")
		s.Stop()
	default:
		if !strings.HasPrefix(command, "peer-") {
			_, _ = io.WriteString(connection, "error unknown-command\n")
		} else {
			var err error
			if s.Command == nil {
				err = errors.New("peer command handler is unavailable")
			} else {
				err = s.Command(command)
			}
			if err == nil {
				_, _ = io.WriteString(connection, "ok\n")
				if s.Logger != nil {
					s.Logger.Printf("control request completed: command=%s", action)
				}
			} else {
				_, _ = io.WriteString(connection, "error rejected\n")
				if s.Logger != nil {
					s.Logger.Printf("control request rejected: command=%s error=%v", action, err)
				}
			}
		}
	}
}

func (s *Server) closeAndClean() {
	s.mu.Lock()
	listener, original := s.listener, s.original
	s.listener = nil
	s.mu.Unlock()
	if listener != nil {
		_ = listener.Close()
	}
	current, err := os.Lstat(s.Path)
	if err == nil && original != nil && os.SameFile(original, current) {
		_ = os.Remove(s.Path)
	}
}

func removeStaleSocket(name string) error {
	info, err := os.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return errors.New("control path exists and is not a socket")
	}
	connection, dialErr := net.DialTimeout("unix", name, 250*time.Millisecond)
	if dialErr == nil {
		_ = connection.Close()
		return errors.New("an active ZenFM control socket already exists")
	}
	if !errors.Is(dialErr, syscall.ECONNREFUSED) && !errors.Is(dialErr, os.ErrNotExist) {
		return fmt.Errorf("probe existing control socket: %w", dialErr)
	}
	if err := os.Remove(name); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("remove stale control socket: %w", err)
	}
	return nil
}
