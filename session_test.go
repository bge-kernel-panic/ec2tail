package main

import (
	"testing"
)

// drain collects the stdout-bound text from a lineWriter run, draining the channel concurrently so
// Write never blocks on the buffered channel.
func collect(t *testing.T, marker string, chunks ...string) []string {
	t.Helper()
	out := make(chan outMsg, 64)
	lw := &lineWriter{host: &host{name: "h", width: 1}, out: out, marker: marker}
	for _, c := range chunks {
		if _, err := lw.Write([]byte(c)); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	lw.flush()
	close(out)

	var got []string
	for msg := range out {
		if !msg.isErr {
			got = append(got, msg.text)
		}
	}
	return got
}

func TestLineWriter(t *testing.T) {
	const marker = "__EC2TAIL_test__"

	t.Run("givenBannerAndPromptBeforeMarker_whenWritten_thenSuppressedUntilMarkerLine", func(t *testing.T) {
		got := collect(t, marker,
			"Starting session with SessionId: foo\r\n",
			"$ echo "+marker+"; exec tail -f x\r\n", // echoed command line — contains marker, not equal
			marker+"\r\n", // echo output — bare marker, stops suppression
			"first log line\r\n",
		)
		if len(got) != 1 || got[0] != "h │ first log line" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("givenShellLineEditorEscapesAbsorbedByLeadingEcho_whenWritten_thenMarkerMatchesCleanly", func(t *testing.T) {
		// Models the double-echo command against a real sh-5.2 session. The bracketed-paste-disable
		// escape + CR (observed in the trace) land on the leading echo's blank line; the marker
		// then arrives alone on its own line. The echoed command line also contains the marker and
		// must not trip the exact match.
		got := collect(t, marker,
			"\x1b[?2004hsh-5.2$ \r\x1b[K\rsh-5.2$ echo; echo "+marker+"; exec tail -n 10 -f /tmp/x 2>&1\r\n",
			"\x1b[?2004l\r\r\n", // pollution + leading echo's blank line, discarded pre-marker
			marker+"\r\n",
			"10.0.0.1 - - GET /healthcheck 200\r\n",
		)
		if len(got) != 1 || got[0] != "h │ 10.0.0.1 - - GET /healthcheck 200" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("givenCRLFLineEndings_whenWritten_thenCarriageReturnTrimmed", func(t *testing.T) {
		got := collect(t, marker, marker+"\r\n", "alpha\r\n", "beta\r\n")
		want := []string{"h │ alpha", "h │ beta"}
		if !equal(got, want) {
			t.Fatalf("got %q want %q", got, want)
		}
	})

	t.Run("givenLineSplitAcrossChunks_whenWritten_thenReassembledIntoOneLine", func(t *testing.T) {
		got := collect(t, marker, marker+"\n", "hel", "lo wor", "ld\n")
		if len(got) != 1 || got[0] != "h │ hello world" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("givenTrailingPartialLineAtClose_whenFlushed_thenEmitted", func(t *testing.T) {
		got := collect(t, marker, marker+"\n", "no newline at end")
		if len(got) != 1 || got[0] != "h │ no newline at end" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("givenMarkerNeverSeen_whenFlushed_thenNothingEmitted", func(t *testing.T) {
		got := collect(t, marker, "banner line\r\n", "partial leftover")
		if len(got) != 0 {
			t.Fatalf("expected no output, got %q", got)
		}
	})
}

func TestTagListSet(t *testing.T) {
	t.Run("givenKeyValue_whenSet_thenParsed", func(t *testing.T) {
		var tl tagList
		if err := tl.Set("env=prod"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(tl) != 1 || tl[0] != (tag{key: "env", value: "prod"}) {
			t.Fatalf("got %+v", tl)
		}
	})

	t.Run("givenValueWithEquals_whenSet_thenSplitOnFirstEquals", func(t *testing.T) {
		var tl tagList
		if err := tl.Set("k=a=b"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if tl[0] != (tag{key: "k", value: "a=b"}) {
			t.Fatalf("got %+v", tl)
		}
	})

	t.Run("givenNoEquals_whenSet_thenError", func(t *testing.T) {
		var tl tagList
		if err := tl.Set("bogus"); err == nil {
			t.Fatal("expected error for missing '='")
		}
	})

	t.Run("givenEmptyKey_whenSet_thenError", func(t *testing.T) {
		var tl tagList
		if err := tl.Set("=v"); err == nil {
			t.Fatal("expected error for empty key")
		}
	})
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
