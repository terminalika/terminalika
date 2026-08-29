package keystate

import (
	"bytes"
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v2"
)

// ReleaseMod marks a tcell key event as a key *release*. The Tty wrapper
// rewrites every release the terminal reports into a press of the same key
// carrying this modifier, so releases travel through tcell's own input
// parser and event queue and are therefore delivered in order with the
// presses (a side channel would race them). Nothing produces Hyper on a
// keyboard, so the marker never collides with a real key.
const ReleaseMod = tcell.ModHyper

// kittyReleaseMod is the kitty modifier-field value carrying ReleaseMod:
// 1 + the hyper bit (16); tcell decodes it as ModHyper.
const kittyReleaseMod = 1 + 16

// IsRelease reports whether ev is a marked key release.
func IsRelease(ev *tcell.EventKey) bool {
	return ev.Modifiers()&ReleaseMod != 0
}

// Unmark returns the released key as a plain key event.
func Unmark(ev *tcell.EventKey) *tcell.EventKey {
	return tcell.NewEventKey(ev.Key(), ev.Rune(), ev.Modifiers()&^ReleaseMod)
}

// tcell pushes the kitty "disambiguate" flag when it starts; with kitty
// support we upgrade that push to also ask for event types (flag 2), so the
// terminal reports repeats and releases. tcell pops the flags again when it
// stops, which restores the terminal no matter what was pushed.
var (
	tcellKittyPush = []byte("\x1b[>1u")
	ourKittyPush   = []byte("\x1b[>" + strconv.Itoa(FlagDisambiguate|FlagEventTypes) + "u")
)

// maxPendingSeq bounds how much input is held back while waiting for the
// rest of an escape sequence split across reads.
const maxPendingSeq = 64

// Tty wraps a tcell.Tty and rewrites the key release events in its input
// into ReleaseMod-marked presses before tcell parses them; key repeats are
// rewritten into plain presses tcell understands.
type Tty struct {
	tcell.Tty

	support Support

	buf     []byte // read buffer for the inner tty
	carry   []byte // filtered input not yet handed to tcell
	pending []byte // an incomplete escape sequence waiting for more input
}

// Wrap returns a Tty that translates key releases in inner's input
// according to what the terminal supports.
func Wrap(inner tcell.Tty, s Support) *Tty {
	return &Tty{
		Tty:     inner,
		support: s,
		buf:     make([]byte, 4096),
	}
}

// Write passes output through, upgrading tcell's kitty flag push so the
// terminal also reports key event types.
func (t *Tty) Write(b []byte) (int, error) {
	if t.support.Kitty && bytes.Contains(b, tcellKittyPush) {
		if _, err := t.Tty.Write(bytes.ReplaceAll(b, tcellKittyPush, ourKittyPush)); err != nil {
			return 0, err
		}
		return len(b), nil
	}
	return t.Tty.Write(b)
}

// Read returns translated input. It keeps reading while the translation
// has produced nothing yet (a sequence split across reads), so it never
// reports zero bytes for input it is still holding (tcell would take that
// for end of input).
func (t *Tty) Read(b []byte) (int, error) {
	for len(t.carry) == 0 {
		n, err := t.Tty.Read(t.buf)
		if n > 0 {
			t.carry = append(t.carry, t.filter(t.buf[:n])...)
		}
		if err != nil {
			if len(t.carry) == 0 {
				return 0, err
			}
			break
		}
		if n == 0 {
			return 0, nil
		}
	}
	n := copy(b, t.carry)
	t.carry = t.carry[n:]
	return n, nil
}

// filter scans input for CSI sequences and rewrites key releases into
// marked presses and key repeats into presses. An escape sequence cut off
// at the end of the input is held back until the next read.
func (t *Tty) filter(in []byte) []byte {
	data := append(t.pending, in...)
	t.pending = nil

	out := make([]byte, 0, len(data))
	for i := 0; i < len(data); {
		if data[i] != 0x1b {
			out = append(out, data[i])
			i++
			continue
		}
		if i+1 == len(data) {
			// A lone ESC at the end: probably the start of a sequence.
			t.pending = append(t.pending, data[i:]...)
			break
		}
		if data[i+1] != '[' {
			out = append(out, data[i], data[i+1])
			i += 2
			continue
		}

		end, complete := scanCSI(data, i)
		if !complete {
			if len(data)-i <= maxPendingSeq {
				t.pending = append(t.pending, data[i:]...)
				break
			}
			out = append(out, data[i:]...)
			break
		}

		out = append(out, t.translate(data[i:end])...)
		i = end
	}
	return out
}

// scanCSI finds the end of the CSI sequence starting at data[i] (ESC '[').
// It returns the index just past the final byte, or false when the sequence
// is not complete yet.
func scanCSI(data []byte, i int) (int, bool) {
	k := i + 2
	for k < len(data) && data[k] >= 0x30 && data[k] <= 0x3f {
		k++
	}
	for k < len(data) && data[k] >= 0x20 && data[k] <= 0x2f {
		k++
	}
	if k < len(data) && data[k] >= 0x40 && data[k] <= 0x7e {
		return k + 1, true
	}
	return k, false
}

// translate rewrites one complete CSI sequence when it is a key event with
// an event type; anything else passes through untouched.
func (t *Tty) translate(seq []byte) []byte {
	final := seq[len(seq)-1]
	params := string(seq[2 : len(seq)-1])

	switch {
	case final == '_':
		if !t.support.Win32 {
			return seq
		}
		return translateWin32(seq, params)
	case t.support.Kitty && (final == 'u' || strings.IndexByte("ABCDHFPQS~", final) >= 0):
		return translateKitty(seq, params, final)
	}
	return seq
}

// translateKitty handles a kitty keyboard protocol key event carrying an
// event type (CSI key ; mods:event [; text] final). A release becomes the
// same sequence with the release marker in its modifier field; a press or
// repeat loses the event type so tcell parses it as an ordinary press.
func translateKitty(seq []byte, params string, final byte) []byte {
	fields := strings.Split(params, ";")
	if len(fields) < 2 || !strings.Contains(fields[1], ":") {
		return seq
	}
	modsEvent := strings.SplitN(fields[1], ":", 2)
	event, _ := strconv.Atoi(modsEvent[1])
	mods, _ := strconv.Atoi(modsEvent[0])
	if mods < 1 {
		mods = 1
	}
	if event == 3 {
		mods += kittyReleaseMod - 1
	}
	fields[1] = strconv.Itoa(mods)
	// The key field may carry alternate keys (base:shifted:layout); keep the
	// base key only so tcell sees the same key on press and release.
	fields[0] = strings.SplitN(fields[0], ":", 2)[0]
	return []byte("\x1b[" + strings.Join(fields[:2], ";") + string(final))
}

// translateWin32 handles a win32-input-mode key event
// (CSI Vk ; Sc ; Uc ; Kd ; Cs ; Rc _). A key-up (Kd 0) becomes a marked
// CSI-u (or arrow) release; everything else passes through to tcell.
func translateWin32(seq []byte, params string) []byte {
	fields := strings.Split(params, ";")
	for len(fields) < 6 {
		fields = append(fields, "")
	}
	keyDown, _ := strconv.Atoi(fields[3])
	if keyDown != 0 {
		return seq
	}
	vk, _ := strconv.Atoi(fields[0])
	uc, _ := strconv.Atoi(fields[2])

	mods := strconv.Itoa(kittyReleaseMod)
	switch vk {
	case 0x25:
		return []byte("\x1b[1;" + mods + "D")
	case 0x26:
		return []byte("\x1b[1;" + mods + "A")
	case 0x27:
		return []byte("\x1b[1;" + mods + "C")
	case 0x28:
		return []byte("\x1b[1;" + mods + "B")
	case 0x0d:
		return []byte("\x1b[13;" + mods + "u")
	case 0x1b:
		return []byte("\x1b[27;" + mods + "u")
	}
	if uc >= 'A' && uc <= 'Z' {
		uc += 'a' - 'A'
	}
	if uc >= 32 && uc < 0xd800 {
		return []byte("\x1b[" + strconv.Itoa(uc) + ";" + mods + "u")
	}
	return nil
}
