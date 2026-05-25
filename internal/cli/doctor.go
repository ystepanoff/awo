package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/awo-dev/awo/internal/execx"
	"github.com/awo-dev/awo/internal/gitx"
	"github.com/spf13/cobra"
)

type checkResult struct {
	name   string
	ok     bool
	detail string
}

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Run environment health checks for AWO",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDoctor(cmd.Context(), cmd.OutOrStdout())
		},
	}
}

func runDoctor(ctx context.Context, out io.Writer) error {
	if ctx == nil {
		ctx = context.Background()
	}

	results := []checkResult{
		checkOnPath("git"),
		checkInsideGitRepo(ctx),
		checkOnPath("claude"),
		checkOnPath("codex"),
		checkVersion(ctx, "git", "--version"),
		checkVersion(ctx, "claude", "--version"),
		checkVersion(ctx, "codex", "--version"),
		{name: "platform", ok: true, detail: fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH)},
	}

	var failed int
	for _, r := range results {
		marker := "ok  "
		if !r.ok {
			marker = "FAIL"
			failed++
		}
		fmt.Fprintf(out, "[%s] %-22s %s\n", marker, r.name, r.detail)
	}

	if failed > 0 {
		return fmt.Errorf("%d check(s) failed", failed)
	}
	return nil
}

func checkOnPath(bin string) checkResult {
	if !execx.CommandExists(bin) {
		return checkResult{name: bin + " on PATH", ok: false, detail: "not found"}
	}
	return checkResult{name: bin + " on PATH", ok: true, detail: "found"}
}

func checkInsideGitRepo(ctx context.Context) checkResult {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	top, err := gitx.GetRepoRoot(cctx, ".")
	if err != nil {
		return checkResult{name: "inside git repo", ok: false, detail: err.Error()}
	}
	return checkResult{name: "inside git repo", ok: true, detail: top}
}

func checkVersion(ctx context.Context, bin string, args ...string) checkResult {
	name := bin + " version"
	if !execx.CommandExists(bin) {
		return checkResult{name: name, ok: false, detail: "binary not on PATH"}
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	tmp, err := os.MkdirTemp("", "awo-doctor-*")
	if err != nil {
		return checkResult{name: name, ok: false, detail: "mkdir: " + err.Error()}
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	stdoutPath := filepath.Join(tmp, "stdout")
	res, err := execx.Run(cctx, execx.CommandSpec{
		Command:    bin,
		Args:       args,
		Timeout:    5 * time.Second,
		StdoutPath: stdoutPath,
		StderrPath: filepath.Join(tmp, "stderr"),
	})
	if err != nil {
		return checkResult{name: name, ok: false, detail: err.Error()}
	}
	if res.ExitCode != 0 {
		return checkResult{name: name, ok: false, detail: fmt.Sprintf("exit %d", res.ExitCode)}
	}
	body, _ := os.ReadFile(stdoutPath)
	v := strings.TrimSpace(strings.SplitN(string(body), "\n", 2)[0])
	if v == "" {
		v = "(empty output)"
	}
	return checkResult{name: name, ok: true, detail: v}
}
