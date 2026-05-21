package execx

import (
	"context"
	"runtime"
	"testing"
)

func TestRunCapturesStdout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix-only test")
	}
	res, err := Run(context.Background(), "sh", []string{"-c", "printf hello"}, RunOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit=%d", res.ExitCode)
	}
	if res.Stdout != "hello" {
		t.Fatalf("stdout=%q", res.Stdout)
	}
}

func TestRunNonZeroExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix-only test")
	}
	res, err := Run(context.Background(), "sh", []string{"-c", "exit 7"}, RunOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ExitCode != 7 {
		t.Fatalf("exit=%d", res.ExitCode)
	}
}
