package main

import (
	"bufio"
	"log"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"
)

const protocolSniffTimeout = 10 * time.Second

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }

type connectionListener struct {
	addr           net.Addr
	connections    chan net.Conn
	dispatcherDone <-chan struct{}
	closed         chan struct{}
	closeOnce      sync.Once
}

func (l *connectionListener) Accept() (net.Conn, error) {
	select {
	case connection := <-l.connections:
		return connection, nil
	case <-l.closed:
		return nil, net.ErrClosed
	case <-l.dispatcherDone:
		return nil, net.ErrClosed
	}
}

func (l *connectionListener) Close() error {
	l.closeOnce.Do(func() { close(l.closed) })
	return nil
}

func (l *connectionListener) Addr() net.Addr { return l.addr }

type protocolDispatcher struct {
	listener  net.Listener
	tls       *connectionListener
	plain     *connectionListener
	logger    *log.Logger
	done      chan struct{}
	closeOnce sync.Once
}

func splitProtocols(listener net.Listener, logger *log.Logger) (net.Listener, net.Listener, *protocolDispatcher) {
	dispatcher := &protocolDispatcher{listener: listener, logger: logger, done: make(chan struct{})}
	dispatcher.tls = &connectionListener{
		addr: listener.Addr(), connections: make(chan net.Conn),
		dispatcherDone: dispatcher.done, closed: make(chan struct{}),
	}
	dispatcher.plain = &connectionListener{
		addr: listener.Addr(), connections: make(chan net.Conn),
		dispatcherDone: dispatcher.done, closed: make(chan struct{}),
	}
	go dispatcher.accept()
	return dispatcher.tls, dispatcher.plain, dispatcher
}

func (d *protocolDispatcher) accept() {
	for {
		connection, err := d.listener.Accept()
		if err != nil {
			_ = d.Close()
			return
		}
		go d.dispatch(connection)
	}
}

func (d *protocolDispatcher) dispatch(connection net.Conn) {
	reader := bufio.NewReader(connection)
	_ = connection.SetReadDeadline(time.Now().Add(protocolSniffTimeout))
	first, err := reader.Peek(1)
	_ = connection.SetReadDeadline(time.Time{})
	if err != nil {
		if d.logger != nil {
			d.logger.Printf("connection protocol detection failed: remote=%s error=%v", connection.RemoteAddr(), err)
		}
		_ = connection.Close()
		return
	}
	target := d.plain
	if first[0] == 0x16 { // A supported TLS connection starts with a handshake record.
		target = d.tls
	}
	wrapped := &bufferedConn{Conn: connection, reader: reader}
	select {
	case target.connections <- wrapped:
	case <-target.closed:
		_ = connection.Close()
	case <-d.done:
		_ = connection.Close()
	}
}

func (d *protocolDispatcher) Close() error {
	var err error
	d.closeOnce.Do(func() {
		close(d.done)
		err = d.listener.Close()
	})
	return err
}

func httpsRedirectHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host == "" {
			http.Error(w, "missing Host header", http.StatusBadRequest)
			return
		}
		target := url.URL{
			Scheme: "https", Host: r.Host, Path: r.URL.Path, RawPath: r.URL.RawPath,
			ForceQuery: r.URL.ForceQuery, RawQuery: r.URL.RawQuery,
		}
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, target.String(), http.StatusTemporaryRedirect)
	})
}
