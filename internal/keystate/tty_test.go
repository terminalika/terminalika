package keystate

import (
	"io"
	"testing"

	"github.com/gdamore/tcell/v2"
)

// scriptTty is a tcell.Tty whose reads return one scripted chunk each.
type scriptTty struct {
	tcell.Tty
	chunks  [][]byte
	written []byte
}

func (s *scriptTty) Read(b []byte) (int, error) {
	if len(s.chunks) == 0 {
		return 0, io.EOF
	}
	n := copy(b, s.chunks[0])
	if n < len(s.chunks[0]) {
		s.chunks[0] = s.chunks[0][n:]
	} else {
		s.chunks = s.chunks[1:]
	}
	return n, nil
}

func (s *scriptTty) Write(b []byte) (int, error) {
	s.written = append(s.written, b...)
	return len(b), nil
}

func (s *scriptTty) Close() error { return nil }

func newScript(chunks ...string) *scriptTty {
	s := &scriptTty{}
	for _, c := range chunks {
		s.chunks = append(s.chunks, []byte(c))
	}
	return s
}

// readAll drains the wrapped tty into a string.
func readAll(t *testing.T, tty *Tty) string {
	t.Helper()
	var out []byte
	buf := make([]byte, 16)
	for {
		n, err := tty.Read(buf)
		out = append(out, buf[:n]...)
		if err == io.EOF {
			return string(out)
		}
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
	}
}

func TestPlainInputPassesThrough(t *testing.T) {
	tty := Wrap(newScript("hello \x1b[A\x1b[119;1u"), Support{Kitty: true})
	if got := readAll(t, tty); got != "hello \x1b[A\x1b[119;1u" {
		t.Fatalf("output = %q", got)
	}
}

func TestKittyReleaseIsMarkedInPlace(t *testing.T) {
	tty := Wrap(newScript("w\x1b[119;1:3ux"), Support{Kitty: true})
	if got := readAll(t, tty); got != "w\x1b[119;17ux" {
		t.Fatalf("output = %q, want the release rewritten with the hyper marker, in order", got)
	}
}

func TestKittyRepeatIsRewrittenAsPress(t *testing.T) {
	tty := Wrap(newScript("\x1b[119;1:2u\x1b[1;1:2A\x1b[1;1:1B\x1b[119;2:2u"), Support{Kitty: true})
	if got := readAll(t, tty); got != "\x1b[119;1u\x1b[1;1A\x1b[1;1B\x1b[119;2u" {
		t.Fatalf("output = %q", got)
	}
}

func TestKittyReleaseKeepsModifiersAndDropsAlternates(t *testing.T) {
	tty := Wrap(newScript("\x1b[119:87;2:3u\x1b[1;5:3A\x1b[3;1:3~\x1b[13;1:3u\x1b[119;1:3;119u"), Support{Kitty: true})
	if got := readAll(t, tty); got != "\x1b[119;18u\x1b[1;21A\x1b[3;17~\x1b[13;17u\x1b[119;17u" {
		t.Fatalf("output = %q", got)
	}
}

func TestArrowReleaseSplitAcrossReads(t *testing.T) {
	tty := Wrap(newScript("\x1b[1;1", ":3A", "x"), Support{Kitty: true})
	if got := readAll(t, tty); got != "\x1b[1;17Ax" {
		t.Fatalf("output = %q", got)
	}
}

func TestReadWaitsForTheRestOfASplitSequence(t *testing.T) {
	tty := Wrap(newScript("\x1b[115;1", ":3u"), Support{Kitty: true})
	buf := make([]byte, 32)
	n, err := tty.Read(buf)
	if err != nil || string(buf[:n]) != "\x1b[115;17u" {
		t.Fatalf("Read = %q %v, want the whole rewritten sequence", buf[:n], err)
	}
}

func TestWithoutKittyNothingIsTouched(t *testing.T) {
	tty := Wrap(newScript("\x1b[119;1:3u"), Support{})
	if got := readAll(t, tty); got != "\x1b[119;1:3u" {
		t.Fatalf("output = %q, want the sequence untouched", got)
	}
}

func TestWin32KeyUpIsRewrittenAsMarkedRelease(t *testing.T) {
	tty := Wrap(newScript("\x1b[87;17;119;1;0;1_\x1b[87;17;87;0;0;1_\x1b[38;72;0;0;0;1_\x1b[16;42;0;0;0;1_"), Support{Win32: true})
	if got := readAll(t, tty); got != "\x1b[87;17;119;1;0;1_\x1b[119;17u\x1b[1;17A" {
		t.Fatalf("output = %q, want key-down untouched, 'w' and Up releases marked, shift key-up dropped", got)
	}
}

func TestWin32SequencesPassThroughWithoutSupport(t *testing.T) {
	tty := Wrap(newScript("\x1b[87;17;119;0;0;1_"), Support{Kitty: true})
	if got := readAll(t, tty); got != "\x1b[87;17;119;0;0;1_" {
		t.Fatalf("output = %q, want the sequence untouched", got)
	}
}

func TestWriteUpgradesTcellKittyPush(t *testing.T) {
	inner := newScript()
	tty := Wrap(inner, Support{Kitty: true})
	if _, err := tty.Write([]byte("\x1b[>4;2m\x1b[>1u\x1b[?9001h")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if string(inner.written) != "\x1b[>4;2m\x1b[>3u\x1b[?9001h" {
		t.Fatalf("written = %q", inner.written)
	}

	inner = newScript()
	tty = Wrap(inner, Support{Win32: true})
	_, _ = tty.Write([]byte("\x1b[>1u"))
	if string(inner.written) != "\x1b[>1u" {
		t.Fatalf("written = %q, want the push untouched without kitty support", inner.written)
	}
}

func TestReleaseMarkerHelpers(t *testing.T) {
	press := tcell.NewEventKey(tcell.KeyRune, 'w', tcell.ModNone)
	release := tcell.NewEventKey(tcell.KeyRune, 'w', ReleaseMod)
	if IsRelease(press) || !IsRelease(release) {
		t.Fatal("IsRelease should only see the marked event")
	}
	plain := Unmark(release)
	if plain.Key() != tcell.KeyRune || plain.Rune() != 'w' || plain.Modifiers() != tcell.ModNone {
		t.Fatalf("Unmark = %+v, want plain w", plain)
	}
}

func TestParseReply(t *testing.T) {
	cases := []struct {
		reply string
		kitty bool
		flags int
	}{
		{"\x1b[?1u\x1b[?62;22c", true, 1},
		{"\x1b[?0u\x1b[?6c", true, 0},
		{"\x1b[?62;4c", false, 0},
		{"", false, 0},
	}
	for _, c := range cases {
		s := parseReply([]byte(c.reply))
		if s.Kitty != c.kitty || s.Flags != c.flags {
			t.Fatalf("parseReply(%q) = %+v", c.reply, s)
		}
		if s.Releases() != c.kitty {
			t.Fatalf("parseReply(%q).Releases() = %v", c.reply, s.Releases())
		}
	}
}
