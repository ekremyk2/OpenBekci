package files

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// fakeClamd speaks enough of clamd's INSTREAM protocol: it checks the framing
// and answers FOUND when the content holds "EICAR", OK otherwise.
func fakeClamd(t *testing.T, reply func(content []byte) string) (addr string, commands <-chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	cmds := make(chan string, 8)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
				cmd := make([]byte, len("zINSTREAM\x00"))
				if _, err := io.ReadFull(conn, cmd); err != nil {
					return
				}
				cmds <- string(cmd)
				var content bytes.Buffer
				for {
					var size [4]byte
					if _, err := io.ReadFull(conn, size[:]); err != nil {
						return
					}
					n := binary.BigEndian.Uint32(size[:])
					if n == 0 {
						break
					}
					if _, err := io.CopyN(&content, conn, int64(n)); err != nil {
						return
					}
				}
				_, _ = conn.Write([]byte(reply(content.Bytes())))
			}()
		}
	}()
	return ln.Addr().String(), cmds
}

func TestClamAVInstream(t *testing.T) {
	addr, cmds := fakeClamd(t, func(content []byte) string {
		if bytes.Contains(content, []byte("EICAR")) {
			return "stream: Eicar-Test-Signature FOUND\x00"
		}
		return "stream: OK\x00"
	})
	c := ClamAV{Addr: addr}

	v, err := c.Scan(context.Background(), strings.NewReader("a perfectly fine document"))
	if err != nil || v.Infected {
		t.Fatalf("clean content: %+v, %v", v, err)
	}
	if got := <-cmds; got != "zINSTREAM\x00" {
		t.Errorf("command = %q", got)
	}

	// content larger than one chunk, with the marker past the first chunk
	big := append(bytes.Repeat([]byte("a"), 3*clamChunk+17), []byte("EICAR")...)
	v, err = c.Scan(context.Background(), bytes.NewReader(big))
	if err != nil || !v.Infected || v.Signature != "Eicar-Test-Signature" {
		t.Fatalf("infected content: %+v, %v", v, err)
	}

	// an empty stream is still a valid stream
	if v, err = c.Scan(context.Background(), strings.NewReader("")); err != nil || v.Infected {
		t.Fatalf("empty content: %+v, %v", v, err)
	}
}

func TestClamAVErrors(t *testing.T) {
	// nothing listens
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	dead := ln.Addr().String()
	_ = ln.Close()
	if _, err := (ClamAV{Addr: dead}).Scan(context.Background(), strings.NewReader("x")); err == nil {
		t.Error("an unreachable scanner must be an error, not a clean verdict")
	}
	// clamd answers with an error (for example INSTREAM size limit exceeded)
	addr, _ := fakeClamd(t, func([]byte) string { return "INSTREAM size limit exceeded. ERROR\x00" })
	if _, err := (ClamAV{Addr: addr}).Scan(context.Background(), strings.NewReader("x")); err == nil {
		t.Error("an ERROR reply must be an error")
	}
	// a cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (ClamAV{Addr: addr}).Scan(ctx, strings.NewReader("x")); err == nil {
		t.Error("a cancelled scan must fail")
	}
}

func TestParseClamResponse(t *testing.T) {
	for in, want := range map[string]Verdict{
		"stream: OK\x00":                         {},
		"stream: OK\n":                           {},
		"stream: Win.Test.EICAR_HDB-1 FOUND\x00": {Infected: true, Signature: "Win.Test.EICAR_HDB-1"},
	} {
		got, err := parseClamResponse(in)
		if err != nil || got != want {
			t.Errorf("parse(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "stream: something ERROR", "garbage"} {
		if _, err := parseClamResponse(in); err == nil {
			t.Errorf("parse(%q) should fail", in)
		}
	}
}
