package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
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

const defaultPort = 53241

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
	if *listenAddress == "" {
		*listenAddress = fmt.Sprintf(":%d", defaultPort)
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
	diagnostics := log.New(stderr, "debug: ", 0)
	transport := "https"
	if *insecureHTTP {
		transport = "http"
	}
	diagnostics.Printf("server setup started: version=%s root=%q data-dir=%q listen=%q transport=%s control-socket=%q auto-stop=%s",
		version, *rootPath, *dataDir, *listenAddress, transport, *controlSocket, autoStop.String())
	if err := os.MkdirAll(*dataDir, 0o700); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	if err := platform.ModeChangeError(os.Chmod(*dataDir, 0o700), *modeLessFilesystem); err != nil {
		return fmt.Errorf("secure data directory: %w", err)
	}
	diagnostics.Printf("server setup: private data directory ready")
	store, err := state.Open(filepath.Join(*dataDir, "zenfm.db"), state.Options{ModeLessFilesystem: *modeLessFilesystem})
	if err != nil {
		return fmt.Errorf("open state store: %w", err)
	}
	defer store.Close()
	diagnostics.Printf("server setup: state store ready")
	root, err := zenfiles.Open(*rootPath, zenfiles.Options{})
	if err != nil {
		return fmt.Errorf("open filesystem root: %w", err)
	}
	defer root.Close()
	diagnostics.Printf("server setup: filesystem root ready")
	api, err := server.New(server.Config{
		Store: store, Files: root, StaticFS: webui.FS(), Version: version, SecureTransport: !*insecureHTTP,
		SessionIdle: *sessionIdle, SessionAbsolute: *sessionAbsolute,
		ModeLessFilesystem: *modeLessFilesystem,
		PublicExclusions:   []string{*certFile, *keyFile},
	})
	if err != nil {
		return fmt.Errorf("initialize HTTP API: %w", err)
	}
	defer api.Close()
	diagnostics.Printf("server setup: HTTP API ready")
	listener, err := net.Listen(listenNetwork(*listenAddress), *listenAddress)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	listener = diagnosticListener{Listener: listener, logger: diagnostics}
	defer listener.Close()
	diagnostics.Printf("server setup: TCP listener ready address=%s", listener.Addr())
	fingerprint := "-"
	var certificateManager *tlsutil.Manager
	if !*insecureHTTP {
		certificateManager, fingerprint, err = tlsutil.NewManagerWithOptions(
			*certFile, *keyFile, []string{listener.Addr().String()}, tlsutil.Options{ModeLessFilesystem: *modeLessFilesystem},
		)
		if err != nil {
			return fmt.Errorf("TLS certificate: %w", err)
		}
		diagnostics.Printf("server setup: TLS certificate ready")
	}
	scheme := "https"
	if *insecureHTTP {
		scheme = "http"
	}
	address := scheme + "://" + listener.Addr().String()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	httpServer := &http.Server{
		Handler: logRequests(api.Handler(), diagnostics), ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout: 60 * time.Second, MaxHeaderBytes: 64 << 10,
		ErrorLog: diagnostics,
	}
	if certificateManager != nil {
		httpServer.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: certificateManager.GetCertificate}
	}
	controlErrors := make(chan error, 1)
	controlServer := &control.Server{
		Path: *controlSocket, URL: address, Fingerprint: fingerprint, Stop: cancel,
		ModeLessFilesystem: *modeLessFilesystem, Logger: diagnostics,
	}
	diagnostics.Printf("server setup: starting local control socket")
	go func() { controlErrors <- controlServer.Run(ctx) }()
	serveErrors := make(chan error, 2)
	var redirectServer *http.Server
	var protocols *protocolDispatcher
	if *insecureHTTP {
		go func() { serveErrors <- httpServer.Serve(listener) }()
	} else {
		tlsListener, plainListener, dispatcher := splitProtocols(listener, diagnostics)
		protocols = dispatcher
		redirectServer = &http.Server{
			Handler: logRequests(httpsRedirectHandler(), diagnostics), ReadHeaderTimeout: 10 * time.Second,
			IdleTimeout: 60 * time.Second, MaxHeaderBytes: 64 << 10,
			ErrorLog: diagnostics,
		}
		go func() { serveErrors <- httpServer.ServeTLS(tlsListener, "", "") }()
		go func() { serveErrors <- redirectServer.Serve(plainListener) }()
	}
	if *autoStop > 0 {
		go watchIdle(ctx, func() {
			diagnostics.Printf("server stopping: inactivity timeout reached after %s", autoStop.String())
			cancel()
		}, api, *autoStop)
	}
	diagnostics.Printf("server setup complete: accepting connections on %s", address)
	fmt.Fprintf(stdout, "ZenFM %s listening on %s\n", version, address)
	if !*insecureHTTP {
		fmt.Fprintf(stdout, "TLS public-key fingerprint: %s\n", fingerprint)
	} else {
		fmt.Fprintln(stderr, "warning: insecure HTTP explicitly enabled")
	}
	select {
	case <-ctx.Done():
		diagnostics.Printf("server shutdown requested")
	case err := <-serveErrors:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			diagnostics.Printf("HTTP serving failed: %v", err)
			cancel()
			return err
		}
	case err := <-controlErrors:
		if err != nil {
			diagnostics.Printf("control socket failed: %v", err)
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
	if redirectServer != nil {
		if err := redirectServer.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown HTTP redirect: %w", err)
		}
	}
	if protocols != nil {
		_ = protocols.Close()
	}
	diagnostics.Printf("server shutdown complete")
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
