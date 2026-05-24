package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunCmdHelpHasExamples(t *testing.T) {
	root := NewRootCmd()
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"run", "--help"})

	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"awo run \"add tests for calculator\"",
		"--mode single",
		"--agent claude",
		"--mode writer-reviewer",
		"--primary claude",
		"--reviewer codex",
		"--verify",
		"--dry-run",
		"--keep-worktrees",
		"--live-output",
		"--max-changed-files",
		"--base-branch",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("run --help missing %q\n%s", want, out)
		}
	}
}
