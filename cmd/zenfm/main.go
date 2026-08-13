package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/xZenLabs/zen-fm/internal/auth"
	"github.com/xZenLabs/zen-fm/internal/control"
	zenfiles "github.com/xZenLabs/zen-fm/internal/files"
	"github.com/xZenLabs/zen-fm/internal/platform"
	"github.com/xZenLabs/zen-fm/internal/server"
	"github.com/xZenLabs/zen-fm/internal/state"
	"github.com/xZenLabs/zen-fm/internal/tlsutil"
	"github.com/xZenLabs/zen-fm/internal/webui"
)

var version = "dev"

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	var err error
	switch args[0] {
	case "serve":
		err = runServe(args[1:], stdout, stderr)
	case "reset-login", "reset-password":
		err = runReset(args[1:], stdout, stderr)
	case "version", "--version", "-version":
		_, err = fmt.Fprintln(stdout, version)
	case "help", "--help", "-h":
		usage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", args[0])
		usage(stderr)
		return 2
	}
	if err != nil {
		fmt.Fprintf(stderr, "zenfm: %v\n", err)
		return 1
	}
	return 0
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "Usage: zenfm <serve|reset-login|version> [options]")
}

func listenNetwork(address string) string {
	host, _, err := net.SplitHostPort(address)
	if err == nil && net.ParseIP(host).To4() != nil {
		return "tcp4"
	}
	return "tcp"
}

func runServe(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(stderr)
	rootPath := flags.String("root", platform.DefaultRoot(), "filesystem root")
	dataDir := flags.String("data-dir", platform.DefaultDataDir(), "private ZenFM state directory")
	listenAddress := flags.String("listen", "", "TCP listen address")
	certFile := flags.String("tls-cert", "", "TLS certificate path")
	keyFile := flags.String("tls-key", "", "TLS private key path")
	controlSocket := flags.String("control-socket", "", "local plugin control socket")
	autoStop := flags.Duration("auto-stop", 0, "stop after this duration without authenticated activity")
	sessionIdle := flags.Duration("session-idle", 2*time.Hour, "browser session idle lifetime")
	sessionAbsolute := flags.Duration("session-absolute", 12*time.Hour, "browser session absolute lifetime")
	insecureHTTP := flags.Bool("insecure-http", false, "explicitly serve unencrypted HTTP")
	modeLessFilesystem := flags.Bool("mode-less-filesystem", false, "allow storage without Unix file modes")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *autoStop < 0 || *sessionIdle <= 0 || *sessionAbsolute <= 0 {
		return errors.New("invalid serve arguments")
	}
	if err := os.MkdirAll(*dataDir, 0o700); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	if err := platform.ModeChangeError(os.Chmod(*dataDir, 0o700), *modeLessFilesystem); err != nil {
		return fmt.Errorf("secure data directory: %w", err)
	}
	if *listenAddress == "" {
		if *insecureHTTP {
			*listenAddress = ":8080"
		} else {
			*listenAddress = ":8443"
		}
	}
	if *controlSocket == "" {
		*controlSocket = filepath.Join(*dataDir, "zenfm.sock")
	}
	if *certFile == "" {
		*certFile = filepath.Join(*dataDir, "tls", "cert.pem")
	}
	if *keyFile == "" {
		*keyFile = filepath.Join(*dataDir, "tls", "key.pem")
	}
	store, err := state.Open(filepath.Join(*dataDir, "zenfm.db"), state.Options{ModeLessFilesystem: *modeLessFilesystem})
	if err != nil {
		return err
	}
	defer store.Close()
	root, err := zenfiles.Open(*rootPath, zenfiles.Options{})
	if err != nil {
		return err
	}
	defer root.Close()
	if !root.Advanced() {
		if err := root.ExcludeAbsolute(*certFile); err != nil {
			return fmt.Errorf("protect TLS certificate: %w", err)
		}
		if err := root.ExcludeAbsolute(*keyFile); err != nil {
			return fmt.Errorf("protect TLS private key: %w", err)
		}
	}
	api, err := server.New(server.Config{
		Store: store, Files: root, StaticFS: webui.FS(), Version: version, SecureTransport: !*insecureHTTP,
		SessionIdle: *sessionIdle, SessionAbsolute: *sessionAbsolute,
		ModeLessFilesystem: *modeLessFilesystem,
	})
	if err != nil {
		return err
	}
	defer api.Close()
	listener, err := net.Listen(listenNetwork(*listenAddress), *listenAddress)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer listener.Close()
	fingerprint := "-"
	var certificateManager *tlsutil.Manager
	if !*insecureHTTP {
		certificateManager, fingerprint, err = tlsutil.NewManagerWithOptions(
			*certFile, *keyFile, []string{listener.Addr().String()}, tlsutil.Options{ModeLessFilesystem: *modeLessFilesystem},
		)
		if err != nil {
			return fmt.Errorf("TLS certificate: %w", err)
		}
	}
	scheme := "https"
	if *insecureHTTP {
		scheme = "http"
	}
	address := scheme + "://" + listener.Addr().String()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	httpServer := &http.Server{
		Handler: api.Handler(), ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout: 60 * time.Second, MaxHeaderBytes: 64 << 10,
	}
	if certificateManager != nil {
		httpServer.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: certificateManager.GetCertificate}
	}
	controlErrors := make(chan error, 1)
	controlServer := &control.Server{
		Path: *controlSocket, URL: address, Fingerprint: fingerprint, Stop: cancel,
		ModeLessFilesystem: *modeLessFilesystem,
	}
	go func() { controlErrors <- controlServer.Run(ctx) }()
	serveErrors := make(chan error, 1)
	go func() {
		if *insecureHTTP {
			serveErrors <- httpServer.Serve(listener)
		} else {
			serveErrors <- httpServer.ServeTLS(listener, "", "")
		}
	}()
	if *autoStop > 0 {
		go watchIdle(ctx, cancel, api, *autoStop)
	}
	fmt.Fprintf(stdout, "ZenFM %s listening on %s\n", version, address)
	if !*insecureHTTP {
		fmt.Fprintf(stdout, "TLS public-key fingerprint: %s\n", fingerprint)
	} else {
		fmt.Fprintln(stderr, "warning: insecure HTTP explicitly enabled")
	}
	select {
	case <-ctx.Done():
	case err := <-serveErrors:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			cancel()
			return err
		}
	case err := <-controlErrors:
		if err != nil {
			cancel()
			return err
		}
	}
	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}

type activitySource interface{ LastActivity() time.Time }

func watchIdle(ctx context.Context, stop context.CancelFunc, activity activitySource, timeout time.Duration) {
	interval := timeout / 4
	if interval < 50*time.Millisecond {
		interval = 50 * time.Millisecond
	}
	if interval > time.Second {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if now.Sub(activity.LastActivity()) >= timeout {
				stop()
				return
			}
		}
	}
}

func runReset(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("reset-login", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataDir := flags.String("data-dir", platform.DefaultDataDir(), "private ZenFM state directory")
	modeLessFilesystem := flags.Bool("mode-less-filesystem", false, "allow storage without Unix file modes")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("invalid reset-login arguments")
	}
	store, err := state.Open(filepath.Join(*dataDir, "zenfm.db"), state.Options{ModeLessFilesystem: *modeLessFilesystem})
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.ResetLogin(auth.DefaultPasswordParams); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Login reset to setup-only %s / %s; all sessions and API tokens were revoked.\n", state.SetupUsername, state.SetupPassword)
	return nil
}
