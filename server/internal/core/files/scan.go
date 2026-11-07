package files

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

// Verdict is the result of a virus scan.
type Verdict struct {
	Infected  bool
	Signature string
}

// Scanner scans content for malware. The server uses ClamAV when
// OPENBEKCI_CLAMAV_ADDR is set; without a scanner files go straight to ready.
type Scanner interface {
	Scan(ctx context.Context, r io.Reader) (Verdict, error)
}

// ClamAV scans through clamd's INSTREAM command over TCP or a unix socket.
type ClamAV struct {
	// Addr is host:port, or "unix:/path/to/clamd.sock".
	Addr string
	// Timeout bounds one scan; zero means two minutes.
	Timeout time.Duration
}

var _ Scanner = ClamAV{}

const clamChunk = 64 << 10

// Scan implements Scanner.
func (c ClamAV) Scan(ctx context.Context, r io.Reader) (Verdict, error) {
	timeout := c.Timeout
	if timeout == 0 {
		timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	network, addr := "tcp", c.Addr
	if rest, ok := strings.CutPrefix(c.Addr, "unix:"); ok {
		network, addr = "unix", rest
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, network, addr)
	if err != nil {
		return Verdict{}, fmt.Errorf("clamav: connect: %w", err)
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	if _, err := conn.Write([]byte("zINSTREAM\x00")); err != nil {
		return Verdict{}, fmt.Errorf("clamav: send command: %w", err)
	}
	buf := make([]byte, clamChunk)
	var size [4]byte
	for {
		n, rerr := r.Read(buf)
		if n > 0 {
			binary.BigEndian.PutUint32(size[:], uint32(n)) //nolint:gosec // n <= clamChunk
			if _, err := conn.Write(size[:]); err != nil {
				return Verdict{}, fmt.Errorf("clamav: send data: %w", err)
			}
			if _, err := conn.Write(buf[:n]); err != nil {
				return Verdict{}, fmt.Errorf("clamav: send data: %w", err)
			}
		}
		if errors.Is(rerr, io.EOF) {
			break
		}
		if rerr != nil {
			return Verdict{}, fmt.Errorf("clamav: read content: %w", rerr)
		}
	}
	if _, err := conn.Write([]byte{0, 0, 0, 0}); err != nil {
		return Verdict{}, fmt.Errorf("clamav: finish stream: %w", err)
	}
	resp, err := io.ReadAll(io.LimitReader(conn, 4096))
	if err != nil {
		return Verdict{}, fmt.Errorf("clamav: read response: %w", err)
	}
	return parseClamResponse(string(resp))
}

// parseClamResponse reads "stream: OK", "stream: <signature> FOUND" or
// "<message> ERROR" (terminated by NUL or newline).
func parseClamResponse(s string) (Verdict, error) {
	s = strings.TrimRight(s, "\x00\r\n ")
	switch {
	case strings.HasSuffix(s, " OK"):
		return Verdict{}, nil
	case strings.HasSuffix(s, " FOUND"):
		sig := strings.TrimSuffix(s, " FOUND")
		sig = strings.TrimSpace(strings.TrimPrefix(sig, "stream:"))
		return Verdict{Infected: true, Signature: sig}, nil
	}
	return Verdict{}, fmt.Errorf("clamav: unexpected response %q", s)
}
