package send

import (
	"bytes"
	"context"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestNormalizeAddr(t *testing.T) {
	for in, want := range map[string]string{
		"1.2.3.4":   "1.2.3.4:9100",
		"h:9101":    "h:9101",
		"[::1]":     "[::1]:9100",
		"[::1]:1":   "[::1]:1",
		"::1":       "[::1]:9100",
		"printer.l": "printer.l:9100",
	} {
		got, err := NormalizeAddr(in)
		if err != nil || got != want {
			t.Errorf("NormalizeAddr(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := NormalizeAddr(""); err == nil {
		t.Error("empty address should fail")
	}
}

func TestSendDeliversBytes(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan []byte, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			got <- nil
			return
		}
		b, _ := io.ReadAll(c) // returns at client half-close
		c.Close()
		got <- b
	}()
	w, err := Dial(context.Background(), ln.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	want := bytes.Repeat([]byte("ddst"), 100000)
	if _, err := w.Write(want); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if b := <-got; !bytes.Equal(b, want) {
		t.Fatalf("received %d bytes, want %d", len(b), len(want))
	}
}

func TestSendUnreachableFailsFast(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	start := time.Now()
	_, err = Dial(context.Background(), addr, time.Second)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), addr) {
		t.Errorf("error %q does not mention %s", err, addr)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("took %v", d)
	}
}

func TestSendPeerResetReturnsError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		c.(*net.TCPConn).SetLinger(0)
		c.Close()
	}()
	addr := ln.Addr().String()
	w, err := Dial(context.Background(), addr, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	chunk := make([]byte, 64*1024)
	done := make(chan error, 1)
	go func() {
		for i := 0; i < 160; i++ { // ~10 MB
			if _, err := w.Write(chunk); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected write error")
		}
		if !strings.Contains(err.Error(), addr) {
			t.Errorf("error %q does not mention %s", err, addr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("write hung")
	}
}

func TestCloseDoesNotHangOnSilentPeer(t *testing.T) {
	old := closeWait
	closeWait = 200 * time.Millisecond
	defer func() { closeWait = old }()
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err == nil {
			defer c.Close()
			time.Sleep(2 * time.Second) // never closes in time
		}
	}()
	w, err := Dial(context.Background(), ln.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	w.Close()
	if time.Since(start) > time.Second {
		t.Fatal("Close did not honour closeWait")
	}
}
