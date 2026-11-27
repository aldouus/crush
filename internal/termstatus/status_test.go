package termstatus

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSequence(t *testing.T) {
	t.Parallel()
	p := 40
	cases := []struct {
		name string
		in   Report
		want string
	}{
		{
			"working",
			Report{State: StateWorking, App: AppCrush, Message: "Installing updates"},
			"\x1b]7501;state=working:app=crush:msg=" + b64("Installing updates") + "\x07",
		},
		{
			"blocked permission",
			Report{State: StateBlocked, Kind: KindPermission, App: AppCrush, Message: "Allow bash?"},
			"\x1b]7501;state=blocked:kind=permission:app=crush:msg=" + b64("Allow bash?") + "\x07",
		},
		{
			"progress",
			Report{State: StateWorking, ID: "us-east", Progress: &p, Title: "US East", Message: "Pushing image"},
			"\x1b]7501;state=working:id=us-east:progress=40:title=" + b64("US East") + ":msg=" + b64("Pushing image") + "\x07",
		},
		{"clear all", Report{State: StateClear}, "\x1b]7501;state=clear\x07"},
		{"clear id", Report{State: StateClear, ID: "eu-west", App: "x", Message: "y"}, "\x1b]7501;state=clear:id=eu-west\x07"},
		{"unknown state", Report{State: "busy"}, ""},
		{"bad id", Report{State: StateIdle, ID: "a b"}, ""},
		{"empty segment", Report{State: StateIdle, ID: "a//b"}, ""},
		{"too deep", Report{State: StateIdle, ID: "a/b/c/d/e/f/g/h/i"}, ""},
		{"long segment", Report{State: StateIdle, ID: strings.Repeat("a", 33)}, ""},
		{"kind ignored", Report{State: StateWorking, Kind: KindAuth}, "\x1b]7501;state=working\x07"},
		{"unknown kind omitted", Report{State: StateBlocked, Kind: "nope"}, "\x1b]7501;state=blocked\x07"},
		{"bad app omitted", Report{State: StateIdle, App: "my app"}, "\x1b]7501;state=idle\x07"},
		{
			"controls stripped",
			Report{State: StateError, Message: "a\nb\x1b]2;x"},
			"\x1b]7501;state=error:msg=" + b64("ab]2;x") + "\x07",
		},
		{"only controls", Report{State: StateError, Message: "\n\t"}, "\x1b]7501;state=error\x07"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, tc.in.Sequence())
			require.LessOrEqual(t, len(tc.in.Sequence()), maxSequence)
		})
	}
}

func TestProgressClamped(t *testing.T) {
	t.Parallel()
	hi, lo := 300, -5
	require.Contains(t, Report{State: StateWorking, Progress: &hi}.Sequence(), "progress=100")
	require.Contains(t, Report{State: StateBlocked, Progress: &lo}.Sequence(), "progress=0")
	require.NotContains(t, Report{State: StateDone, Progress: &hi}.Sequence(), "progress")
}

func TestMessageTruncated(t *testing.T) {
	t.Parallel()
	seq := Report{State: StateDone, Message: "x" + strings.Repeat("é", 1100)}.Sequence()
	enc := strings.TrimSuffix(strings.TrimPrefix(seq, "\x1b]7501;state=done:msg="), "\x07")
	dec, err := base64.StdEncoding.DecodeString(enc)
	require.NoError(t, err)
	require.LessOrEqual(t, len(dec), maxMsgDec)
	require.True(t, strings.HasPrefix("x"+strings.Repeat("é", 1100), string(dec)))
	require.LessOrEqual(t, len(enc), maxMsgEnc)
}

func TestEncodedCapKeepsMessage(t *testing.T) {
	t.Parallel()
	// 2048 bytes not divisible by 3 encode to 2736, over the encoded cap.
	seq := Report{State: StateDone, Message: strings.Repeat("a", maxMsgDec)}.Sequence()
	require.Contains(t, seq, "msg=")
	require.LessOrEqual(t, len(seq), maxSequence)
}

func TestQuery(t *testing.T) {
	t.Parallel()
	require.Equal(t, "\x1b]7501;?\x07", Query)
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
