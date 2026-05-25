package agents

import (
	"strings"
	"testing"
)

func TestDetectPermissionFailureNilOnEmpty(t *testing.T) {
	if f := DetectPermissionFailure("", ""); f != nil {
		t.Errorf("expected nil for empty input, got %+v", f)
	}
}

func TestDetectPermissionFailureNilOnUnrelated(t *testing.T) {
	stdout := "fine and dandy\nall good here\n"
	stderr := "info: connecting to api\n"
	if f := DetectPermissionFailure(stdout, stderr); f != nil {
		t.Errorf("expected nil for unrelated logs, got %+v", f)
	}
}

func TestDetectPermissionFailureMatchesPatterns(t *testing.T) {
	cases := []struct {
		name    string
		stdout  string
		stderr  string
		wantSrc string
		// substring expected in the matched lower-cased pattern
		wantPattern string
	}{
		{
			name:        "stderr permission required",
			stderr:      "Error: permission required to write file\n",
			wantSrc:     "stderr",
			wantPattern: "permission required",
		},
		{
			name:        "stdout requires approval",
			stdout:      "noise\nThe edit requires approval before it can run\n",
			wantSrc:     "stdout",
			wantPattern: "requires approval",
		},
		{
			name:        "stderr blocked by sandbox",
			stderr:      "write operation Blocked by Sandbox\n",
			wantSrc:     "stderr",
			wantPattern: "blocked by sandbox",
		},
		{
			name:        "stdout cannot proceed without approval",
			stdout:      "model: I cannot proceed without approval from the user\n",
			wantSrc:     "stdout",
			wantPattern: "cannot proceed without approval",
		},
		{
			name:        "stderr operation not permitted",
			stderr:      "fs: operation not permitted (EPERM)\n",
			wantSrc:     "stderr",
			wantPattern: "operation not permitted",
		},
		{
			name:        "stderr not permitted in non-interactive mode",
			stderr:      "tool use is Not Permitted In Non-Interactive Mode\n",
			wantSrc:     "stderr",
			wantPattern: "not permitted in non-interactive mode",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := DetectPermissionFailure(tc.stdout, tc.stderr)
			if f == nil {
				t.Fatalf("expected match")
			}
			if f.Source != tc.wantSrc {
				t.Errorf("Source=%q want %q", f.Source, tc.wantSrc)
			}
			if f.Pattern != tc.wantPattern {
				t.Errorf("Pattern=%q want %q", f.Pattern, tc.wantPattern)
			}
			if !strings.Contains(strings.ToLower(f.Sample), tc.wantPattern) {
				t.Errorf("Sample %q does not contain pattern %q", f.Sample, tc.wantPattern)
			}
			if strings.HasPrefix(f.Sample, " ") || strings.HasSuffix(f.Sample, "\n") {
				t.Errorf("Sample should be trimmed: %q", f.Sample)
			}
		})
	}
}

func TestDetectPermissionFailureStderrWinsOverStdout(t *testing.T) {
	// When both streams contain a hit, stderr is the canonical source —
	// most CLIs surface permission errors there and tests should know
	// to look at stderr first.
	f := DetectPermissionFailure(
		"stdout has permission denied somewhere",
		"stderr also says permission denied",
	)
	if f == nil {
		t.Fatal("expected match")
	}
	if f.Source != "stderr" {
		t.Errorf("Source=%q want stderr (stderr should win)", f.Source)
	}
}

func TestDetectPermissionFailureCaseInsensitive(t *testing.T) {
	f := DetectPermissionFailure("", "PERMISSION DENIED writing file\n")
	if f == nil {
		t.Fatal("expected match for upper-case input")
	}
	if f.Pattern != "permission denied" {
		t.Errorf("Pattern=%q want permission denied", f.Pattern)
	}
}
