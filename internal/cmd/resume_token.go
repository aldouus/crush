package cmd

import (
	"os"

	"github.com/charmbracelet/x/ansi"
)

// programResumeToken is the terminal's OSC 7577 token, taken out of the
// process environment at startup by [takeProgramResumeToken].
var programResumeToken struct {
	value string
	ok    bool
}

// takeProgramResumeToken moves OSC7577_TOKEN out of the process
// environment. The token lets its holder make the terminal run any
// command on restart, so no child may inherit it; with it unset, every
// spawn that relies on the implicit os.Environ() is safe. Only the TUI
// renderer gets it back, via [tuiEnviron].
func takeProgramResumeToken() {
	v, ok := os.LookupEnv(ansi.ProgramResumeTokenEnv)
	if !ok {
		return
	}
	programResumeToken.value, programResumeToken.ok = v, true
	_ = os.Unsetenv(ansi.ProgramResumeTokenEnv)
}

// tuiEnviron is the environment handed to bubbletea: the process
// environment plus the resume token, if the terminal supplied one.
func tuiEnviron() []string {
	env := os.Environ()
	if programResumeToken.ok {
		env = append(env, ansi.ProgramResumeTokenEnv+"="+programResumeToken.value)
	}
	return env
}
