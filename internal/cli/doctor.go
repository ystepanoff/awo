package cli

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"strings"
	"time"

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
		checkOnPath(ctx, "git"),
		checkInsideGitRepo(ctx),
		checkOnPath(ctx, "claude"),
		checkOnPath(ctx, "codex"),
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

func checkOnPath(ctx context.Context, bin string) checkResult {
	_ = ctx
	p, err := exec.LookPath(bin)
	if err != nil {
		return checkResult{name: bin + " on PATH", ok: false, detail: "not found"}
	}
	return checkResult{name: bin + " on PATH", ok: true, detail: p}
}

func checkInsideGitRepo(ctx context.Context) checkResult {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	top, err := gitx.TopLevel(cctx, ".")
	if err != nil {
		return checkResult{name: "inside git repo", ok: false, detail: err.Error()}
	}
	return checkResult{name: "inside git repo", ok: true, detail: top}
}

func checkVersion(ctx context.Context, bin string, args ...string) checkResult {
	name := bin + " version"
	if _, err := exec.LookPath(bin); err != nil {
		return checkResult{name: name, ok: false, detail: "binary not on PATH"}
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, bin, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return checkResult{name: name, ok: false, detail: fmt.Sprintf("could not get version: %v", err)}
	}
	v := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	if v == "" {
		v = "(empty output)"
	}
	return checkResult{name: name, ok: true, detail: v}
}
