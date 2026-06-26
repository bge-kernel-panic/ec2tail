package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/mmmorris1975/ssm-session-client/datachannel"
)

// Defensive remote terminal size; some agents gate output until a size is set. Matches the
// library's own contrived fallback (45x132). We never resize after this.
const (
	termRows = 45
	termCols = 132
)

// session wraps one instance's data channel and guarantees teardown happens at most once.
type session struct {
	host *host
	ch   *datachannel.SsmDataChannel
	once sync.Once
}

// teardown sends the protocol terminate then closes the websocket (order matters: the library's
// Close() only shuts the socket, so TerminateSession must go first while it is still open).
func (s *session) teardown() {
	s.once.Do(func() {
		_ = s.ch.TerminateSession()
		_ = s.ch.Close()
	})
}

// registry tracks live sessions so a signal handler can tear them all down, unblocking WriteTo.
type registry struct {
	mu       sync.Mutex
	sessions []*session
}

func (r *registry) add(s *session) {
	r.mu.Lock()
	r.sessions = append(r.sessions, s)
	r.mu.Unlock()
}

func (r *registry) teardownAll() {
	r.mu.Lock()
	snapshot := append([]*session(nil), r.sessions...)
	r.mu.Unlock()
	for _, s := range snapshot {
		s.teardown()
	}
}

// streamHost connects to one instance, runs tail, and pushes formatted lines to out until the
// session ends. It never returns an error: failures are reported as status lines on out.
func streamHost(ctx context.Context, cfg aws.Config, h *host, inst instance, globs []string, out chan<- outMsg, reg *registry, ran *int32) {
	ch := new(datachannel.SsmDataChannel)
	if err := ch.Open(cfg, &ssm.StartSessionInput{Target: aws.String(inst.id)}); err != nil {
		out <- outMsg{isErr: true, text: h.statusLine("✗", "failed to start session: "+err.Error())}
		return
	}

	s := &session{host: h, ch: ch}
	reg.add(s)
	defer s.teardown()
	incr(ran)

	_ = ch.SetTerminalSize(termRows, termCols)

	marker := newMarker()
	// exec replaces the shell with tail so there is no prompt redraw, and a clean exit closes the
	// session. 2>&1 folds remote tail errors (e.g. "cannot open") inline under this host's prefix.
	command := fmt.Sprintf("echo %s; exec tail -n 10 -f %s 2>&1\n", marker, strings.Join(globs, " "))
	if _, err := ch.Write([]byte(command)); err != nil {
		out <- outMsg{isErr: true, text: h.statusLine("✗", "failed to send command: "+err.Error())}
		return
	}

	lw := &lineWriter{host: h, out: out, marker: marker}
	_, err := ch.WriteTo(lw) // blocks, pumping the protocol, until the channel closes
	lw.flush()

	// A cancelled context means we tore the session down deliberately — stay silent. Anything else
	// is the remote side going away on its own.
	if ctx.Err() == nil && (err == nil || errors.Is(err, io.EOF)) {
		out <- outMsg{isErr: true, text: h.statusLine("✗", "session ended")}
	} else if ctx.Err() == nil && err != nil {
		out <- outMsg{isErr: true, text: h.statusLine("✗", "session ended: "+err.Error())}
	}
}

// lineWriter is the io.Writer sink for WriteTo. It suppresses everything up to and including the
// marker line (plugin banner, prompt, echoed command), then prefixes each subsequent line.
type lineWriter struct {
	host       *host
	out        chan<- outMsg
	marker     string
	buf        []byte
	markerSeen bool
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		line := bytes.TrimSuffix(w.buf[:i], []byte("\r")) // remote pty is cooked → emits \r\n
		w.buf = w.buf[i+1:]
		w.emit(line)
	}
	return len(p), nil
}

// flush emits any trailing partial line left when the session closes without a final newline.
func (w *lineWriter) flush() {
	if w.markerSeen && len(w.buf) > 0 {
		w.emit(bytes.TrimSuffix(w.buf, []byte("\r")))
		w.buf = nil
	}
}

func (w *lineWriter) emit(line []byte) {
	if !w.markerSeen {
		// Exact-line equality stops on echo's *output* (the bare marker), not the echoed command
		// line (which contains the marker plus the rest of the command).
		if string(bytes.TrimSpace(line)) == w.marker {
			w.markerSeen = true
		}
		return
	}
	w.out <- outMsg{text: w.host.logLine(string(line))}
}

// newMarker returns a distinctive per-session token that cannot collide with log content.
func newMarker() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "__EC2TAIL_" + hex.EncodeToString(b) + "__"
}
