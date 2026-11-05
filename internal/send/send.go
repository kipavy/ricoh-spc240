// Package send streams a finished print job to a printer's raw TCP port.
package send

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

const (
	defaultPort        = "9100"
	defaultDialTimeout = 10 * time.Second
)

// Tunable by tests.
var (
	writeTimeout = 30 * time.Second
	closeWait    = 10 * time.Second
)

// NormalizeAddr turns "host", "host:port" or "[v6]" into host:port,
// defaulting the port to 9100.
func NormalizeAddr(addr string) (string, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return "", fmt.Errorf("empty printer address")
	}
	if _, _, err := net.SplitHostPort(addr); err == nil {
		return addr, nil
	}
	host := strings.TrimSuffix(strings.TrimPrefix(addr, "["), "]")
	if host == "" {
		return "", fmt.Errorf("invalid printer address %q", addr)
	}
	return net.JoinHostPort(host, defaultPort), nil
}

type conn struct {
	c    *net.TCPConn
	addr string
}

// Dial connects to the printer. timeout bounds the connection attempt
// (10 s when 0); ctx can cancel it earlier.
func Dial(ctx context.Context, addr string, timeout time.Duration) (io.WriteCloser, error) {
	full, err := NormalizeAddr(addr)
	if err != nil {
		return nil, err
	}
	if timeout <= 0 {
		timeout = defaultDialTimeout
	}
	d := net.Dialer{Timeout: timeout}
	c, err := d.DialContext(ctx, "tcp", full)
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", full, err)
	}
	return &conn{c: c.(*net.TCPConn), addr: full}, nil
}

func (w *conn) Write(p []byte) (int, error) {
	w.c.SetWriteDeadline(time.Now().Add(writeTimeout))
	n, err := w.c.Write(p)
	if err != nil {
		return n, fmt.Errorf("write to %s: %w", w.addr, err)
	}
	return n, nil
}

// Close half-closes, waits for the peer to finish (bounded), then closes.
func (w *conn) Close() error {
	defer w.c.Close()
	if err := w.c.CloseWrite(); err != nil {
		return fmt.Errorf("close %s: %w", w.addr, err)
	}
	w.c.SetReadDeadline(time.Now().Add(closeWait))
	// A timeout or reset here is harmless: all data was already written.
	io.Copy(io.Discard, w.c)
	return nil
}
