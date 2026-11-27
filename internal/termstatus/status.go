// Package termstatus writes the Program Status Protocol (OSC 7501).
//
// A report tells the terminal whether a program is idle, working, waiting
// on the user, finished, or failed. Presentation is the terminal's job.
// See https://www.superlogical.com/rex/docs/build/program-status.
package termstatus

import (
	"encoding/base64"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

// State is the required state of a record.
type State string

const (
	StateIdle    State = "idle"
	StateWorking State = "working"
	StateDone    State = "done"
	StateBlocked State = "blocked"
	StateError   State = "error"
	StateClear   State = "clear"
)

// Kind says what a blocked record is waiting for.
type Kind string

const (
	KindPermission Kind = "permission"
	KindQuestion   Kind = "question"
	KindAuth       Kind = "auth"
)

// AppCrush is the stable program identifier.
const AppCrush = "crush"

// Protocol limits. A report that breaks one is discarded whole.
const (
	maxSequence = 4096
	maxKey      = 16
	maxMsgEnc   = 2732
	maxMsgDec   = 2048
	maxTitleEnc = 256
	maxTitleDec = 192
	maxApp      = 32
	maxID       = 128
	maxSegment  = 32
	maxDepth    = 8
)

// Report is one OSC 7501 report. A missing field is omitted, and the
// terminal replaces the addressed record with exactly the keys present.
type Report struct {
	State State
	// ID addresses a record. Empty means the root record.
	ID string
	// Kind is meaningful only with StateBlocked.
	Kind Kind
	// Progress is 0–100. Set only with StateWorking or StateBlocked;
	// leaving it nil means indeterminate.
	Progress *int
	App      string
	Title    string
	Message  string
}

// Query is the feature-detection request. A supporting terminal replies
// with OSC 7501 ; ? ST; terminals that do not know the OSC ignore it.
const Query = "\x1b]7501;?\x07"

// Sequence renders r, or "" when the report must be discarded whole.
func (r Report) Sequence() string {
	if !knownState(r.State) {
		return ""
	}
	if r.ID != "" && !validID(r.ID) {
		return ""
	}
	pairs := []string{"state=" + string(r.State)}
	if r.ID != "" {
		pairs = append(pairs, "id="+r.ID)
	}
	if r.State != StateClear {
		if r.State == StateBlocked && knownKind(r.Kind) {
			pairs = append(pairs, "kind="+string(r.Kind))
		}
		if r.Progress != nil && (r.State == StateWorking || r.State == StateBlocked) {
			n := *r.Progress
			if n < 0 {
				n = 0
			}
			if n > 100 {
				n = 100
			}
			pairs = append(pairs, "progress="+strconv.Itoa(n))
		}
		if validSegment(r.App) {
			pairs = append(pairs, "app="+r.App)
		}
		if v := encodeText(r.Title, maxTitleDec, maxTitleEnc); v != "" {
			pairs = append(pairs, "title="+v)
		}
		if v := encodeText(r.Message, maxMsgDec, maxMsgEnc); v != "" {
			pairs = append(pairs, "msg="+v)
		}
	}
	seq := "\x1b]7501;" + strings.Join(pairs, ":") + "\x07"
	if len(seq) > maxSequence {
		return ""
	}
	return seq
}

// Write writes the report. A discarded report writes nothing.
func Write(w io.Writer, r Report) error {
	seq := r.Sequence()
	if seq == "" {
		return nil
	}
	_, err := io.WriteString(w, seq)
	return err
}

func knownState(s State) bool {
	switch s {
	case StateIdle, StateWorking, StateDone, StateBlocked, StateError, StateClear:
		return true
	default:
		return false
	}
}

func knownKind(k Kind) bool {
	switch k {
	case KindPermission, KindQuestion, KindAuth:
		return true
	default:
		return false
	}
}

func validID(id string) bool {
	if len(id) > maxID {
		return false
	}
	parts := strings.Split(id, "/")
	if len(parts) > maxDepth {
		return false
	}
	for _, p := range parts {
		if !validSegment(p) {
			return false
		}
	}
	return true
}

func validSegment(s string) bool {
	if s == "" || len(s) > maxSegment {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case c == '_' || c == '.' || c == '+' || c == '-':
		default:
			return false
		}
	}
	return true
}

// encodeText strips controls, truncates, and base64-encodes free text.
// Empty input returns "" so the key is omitted.
func encodeText(s string, decLimit, encLimit int) string {
	s = strings.ToValidUTF8(s, "")
	var b strings.Builder
	for _, r := range s {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			continue
		}
		b.WriteRune(r)
	}
	s = truncateRunes(b.String(), decLimit, encLimit)
	if s == "" {
		return ""
	}
	return base64.StdEncoding.EncodeToString([]byte(s))
}

// truncateRunes drops whole runes until the text fits both caps. Base64
// grows by 4/3 and rounds up, so the decoded cap alone is not enough.
func truncateRunes(s string, decLimit, encLimit int) string {
	for len(s) > decLimit || (s != "" && len(base64.StdEncoding.EncodeToString([]byte(s))) > encLimit) {
		_, size := utf8.DecodeLastRuneInString(s)
		if size <= 0 {
			return ""
		}
		s = s[:len(s)-size]
	}
	return s
}
