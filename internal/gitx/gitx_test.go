package gitx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeCall captures one git invocation for inspection.
type fakeCall struct {
	Cwd  string
	Args []string
}

// fakeResponse describes what the fake runner should return for a given
// argument key.
type fakeResponse struct {
	Stdout string
	Stderr string
	Exit   int
	Err    error
}

// fakeRunner is a swappable git Runner for tests. Responses keyed by the
// space-joined args; missing keys return Default.
type fakeRunner struct {
	Calls     []fakeCall
	Responses map[string]fakeResponse
	Default   fakeResponse
}

func (f *fakeRunner) run(_ context.Context, cwd string, args []string) (string, string, int, error) {
	f.Calls = append(f.Calls, fakeCall{Cwd: cwd, Args: append([]string(nil), args...)})
	key := strings.Join(args, " ")
	if r, ok := f.Responses[key]; ok {
		return r.Stdout, r.Stderr, r.Exit, r.Err
	}
	return f.Default.Stdout, f.Default.Stderr, f.Default.Exit, f.Default.Err
}

func withFake(t *testing.T, f *fakeRunner) {
	t.Helper()
	old := SetRunner(f.run)
	t.Cleanup(func() { SetRunner(old) })
}

// ----- branch naming ------------------------------------------------------

func TestBranchNameDefaultPrefix(t *testing.T) {
	got, err := BranchName("", "run123", "claude", "writer")
	if err != nil {
		t.Fatal(err)
	}
	if want := "awo/run123/claude-writer"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestBranchNameCustomPrefix(t *testing.T) {
	got, err := BranchName("awo-mvp", "rid", "codex", "reviewer")
	if err != nil {
		t.Fatal(err)
	}
	if want := "awo-mvp/rid/codex-reviewer"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestBranchNamePrefixTrailingSlash(t *testing.T) {
	got, err := BranchName("awo/", "rid", "claude", "competitor")
	if err != nil {
		t.Fatal(err)
	}
	if want := "awo/rid/claude-competitor"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestBranchNameRejectsNonAwoPrefix(t *testing.T) {
	if _, err := BranchName("feature", "rid", "claude", "writer"); err == nil {
		t.Fatal("expected error for non-awo prefix")
	}
}

func TestBranchNameRejectsEmptyParts(t *testing.T) {
	cases := [][3]string{
		{"", "claude", "writer"},
		{"rid", "", "writer"},
		{"rid", "claude", ""},
	}
	for _, c := range cases {
		if _, err := BranchName("awo", c[0], c[1], c[2]); err == nil {
			t.Errorf("expected error for empty: %+v", c)
		}
	}
}

func TestWorktreePath(t *testing.T) {
	root := t.TempDir()
	got, err := WorktreePath(root, "run1", "claude", "writer")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, ".awo", "worktrees", "run1", "claude-writer")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestWorktreePathRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	if _, err := WorktreePath(root, "../evil", "claude", "writer"); err == nil {
		t.Fatal("expected error for traversal in run id")
	}
	if _, err := WorktreePath(root, "rid", "../etc", "writer"); err == nil {
		t.Fatal("expected error for traversal in agent")
	}
}

// ----- porcelain parsing --------------------------------------------------

func TestParseWorktreeListPorcelain(t *testing.T) {
	in := `worktree /repo
HEAD abc123
branch refs/heads/main

worktree /repo/.awo/worktrees/run1/claude-writer
HEAD def456
branch refs/heads/awo/run1/claude-writer

worktree /repo/detached
HEAD ghi789
detached
`
	got := parseWorktreeListPorcelain(in)
	if len(got) != 3 {
		t.Fatalf("got %d entries want 3: %+v", len(got), got)
	}
	if got[0].Path != "/repo" || got[0].Branch != "main" || got[0].HEAD != "abc123" {
		t.Errorf("entry 0 wrong: %+v", got[0])
	}
	if got[1].Branch != "awo/run1/claude-writer" {
		t.Errorf("entry 1 branch wrong: %+v", got[1])
	}
	if got[2].Branch != "" {
		t.Errorf("detached entry should have empty branch: %+v", got[2])
	}
}

// ----- ListAwoWorktrees ---------------------------------------------------

func TestListAwoWorktreesFiltersByBaseAndPrefix(t *testing.T) {
	root := t.TempDir()
	stdout := "worktree " + root + "\nHEAD aaa\nbranch refs/heads/main\n\n" +
		"worktree " + filepath.Join(root, ".awo/worktrees/run1/claude-writer") + "\nHEAD bbb\nbranch refs/heads/awo/run1/claude-writer\n\n" +
		"worktree " + filepath.Join(root, ".awo/worktrees/run2/codex-reviewer") + "\nHEAD ccc\nbranch refs/heads/awo/run2/codex-reviewer\n\n" +
		"worktree " + filepath.Join(root, "other") + "\nHEAD ddd\nbranch refs/heads/feature/x\n"
	f := &fakeRunner{
		Responses: map[string]fakeResponse{
			"worktree list --porcelain": {Stdout: stdout},
		},
	}
	withFake(t, f)

	all, err := ListAwoWorktrees(context.Background(), ListWorktreesOptions{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("got %d AWO worktrees want 2: %+v", len(all), all)
	}
	if all[0].RunID != "run1" || all[0].Agent != "claude" || all[0].Role != "writer" {
		t.Errorf("entry 0 fields wrong: %+v", all[0])
	}
	if all[1].RunID != "run2" || all[1].Agent != "codex" || all[1].Role != "reviewer" {
		t.Errorf("entry 1 fields wrong: %+v", all[1])
	}

	filtered, err := ListAwoWorktrees(context.Background(), ListWorktreesOptions{
		RepoRoot: root, RunID: "run2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].RunID != "run2" {
		t.Fatalf("filter by run-id failed: %+v", filtered)
	}
}

// ----- RemoveWorktree -----------------------------------------------------

func TestRemoveWorktreeRefusesPathOutsideAwoBase(t *testing.T) {
	root := t.TempDir()
	f := &fakeRunner{}
	withFake(t, f)

	err := RemoveWorktree(context.Background(), RemoveWorktreeOptions{
		RepoRoot:     root,
		WorktreePath: filepath.Join(root, "src", "important"),
	})
	if err == nil {
		t.Fatal("expected refusal")
	}
	if !strings.Contains(err.Error(), "outside") {
		t.Errorf("unexpected error: %v", err)
	}
	if len(f.Calls) != 0 {
		t.Fatalf("git should not be called when path is unsafe; got %+v", f.Calls)
	}
}

func TestRemoveWorktreeRefusesParentTraversal(t *testing.T) {
	root := t.TempDir()
	f := &fakeRunner{}
	withFake(t, f)
	err := RemoveWorktree(context.Background(), RemoveWorktreeOptions{
		RepoRoot:     root,
		WorktreePath: filepath.Join(root, ".awo/worktrees", "..", "..", "etc"),
	})
	if err == nil {
		t.Fatal("expected refusal for parent traversal")
	}
	if len(f.Calls) != 0 {
		t.Fatalf("git should not be called; got %+v", f.Calls)
	}
}

func TestRemoveWorktreeAllowsAwoPathAndDoesNotDeleteBranch(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, ".awo", "worktrees", "run1", "claude-writer")
	f := &fakeRunner{}
	withFake(t, f)

	if err := RemoveWorktree(context.Background(), RemoveWorktreeOptions{
		RepoRoot: root, WorktreePath: target, Force: true,
	}); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(f.Calls) != 1 {
		t.Fatalf("expected 1 git call, got %d: %+v", len(f.Calls), f.Calls)
	}
	got := f.Calls[0].Args
	for _, a := range got {
		if a == "branch" || strings.Contains(a, "branch -d") || strings.Contains(a, "branch -D") {
			t.Fatalf("MVP must not delete branches; got args=%v", got)
		}
	}
	want := []string{"worktree", "remove", "--force", target}
	if !equal(got, want) {
		t.Fatalf("args=%v want %v", got, want)
	}
}

// ----- CreateWorktree -----------------------------------------------------

func TestCreateWorktreeBuildsCorrectArgs(t *testing.T) {
	root := t.TempDir()
	f := &fakeRunner{
		Responses: map[string]fakeResponse{
			"rev-parse HEAD": {Stdout: "deadbeef\n"},
		},
	}
	withFake(t, f)

	info, err := CreateWorktree(context.Background(), CreateWorktreeOptions{
		RepoRoot: root,
		RunID:    "run1",
		Agent:    "claude",
		Role:     "writer",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if info.Branch != "awo/run1/claude-writer" {
		t.Errorf("branch=%q", info.Branch)
	}
	wantPath := filepath.Join(root, ".awo", "worktrees", "run1", "claude-writer")
	if info.Path != wantPath {
		t.Errorf("path=%q want %q", info.Path, wantPath)
	}
	if info.HEAD != "deadbeef" {
		t.Errorf("head=%q", info.HEAD)
	}

	// Verify the worktree-add invocation, ignoring the trailing rev-parse.
	if len(f.Calls) == 0 {
		t.Fatal("no git calls recorded")
	}
	add := f.Calls[0]
	if add.Cwd != root {
		t.Errorf("cwd=%q want %q", add.Cwd, root)
	}
	wantArgs := []string{"worktree", "add", "-b", "awo/run1/claude-writer", wantPath, "HEAD"}
	if !equal(add.Args, wantArgs) {
		t.Errorf("args=%v want %v", add.Args, wantArgs)
	}
}

func TestCreateWorktreeUsesBaseBranchAndDoesNotFetch(t *testing.T) {
	root := t.TempDir()
	f := &fakeRunner{
		Responses: map[string]fakeResponse{
			"rev-parse HEAD": {Stdout: "abc\n"},
		},
	}
	withFake(t, f)

	if _, err := CreateWorktree(context.Background(), CreateWorktreeOptions{
		RepoRoot: root, RunID: "rid", Agent: "codex", Role: "competitor",
		BaseBranch: "main",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	for _, c := range f.Calls {
		for _, a := range c.Args {
			if a == "fetch" || a == "pull" || strings.Contains(a, "remote") {
				t.Fatalf("CreateWorktree must not talk to remote; got %v", c.Args)
			}
		}
	}
	if f.Calls[0].Args[len(f.Calls[0].Args)-1] != "main" {
		t.Errorf("base branch not threaded through: %v", f.Calls[0].Args)
	}
}

// ----- introspection ------------------------------------------------------

func TestGetCurrentBranchDetached(t *testing.T) {
	f := &fakeRunner{
		Responses: map[string]fakeResponse{
			"symbolic-ref --quiet --short HEAD": {Exit: 1},
		},
	}
	withFake(t, f)
	got, err := GetCurrentBranch(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("expected empty for detached HEAD, got %q", got)
	}
}

func TestGetCurrentBranchHappy(t *testing.T) {
	f := &fakeRunner{
		Responses: map[string]fakeResponse{
			"symbolic-ref --quiet --short HEAD": {Stdout: "main\n"},
		},
	}
	withFake(t, f)
	got, err := GetCurrentBranch(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "main" {
		t.Fatalf("got %q", got)
	}
}

func TestIsInsideGitRepoTrue(t *testing.T) {
	f := &fakeRunner{
		Responses: map[string]fakeResponse{
			"rev-parse --is-inside-work-tree": {Stdout: "true\n"},
		},
	}
	withFake(t, f)
	if !IsInsideGitRepo(context.Background(), "") {
		t.Fatal("expected true")
	}
}

func TestIsInsideGitRepoFalse(t *testing.T) {
	f := &fakeRunner{
		Default: fakeResponse{Exit: 128, Stderr: "fatal: not a git repository"},
	}
	withFake(t, f)
	if IsInsideGitRepo(context.Background(), "") {
		t.Fatal("expected false")
	}
}

func TestEnsureCleanEnoughReturnsWarnings(t *testing.T) {
	f := &fakeRunner{
		Responses: map[string]fakeResponse{
			"status --porcelain": {Stdout: " M file_a.go\n?? file_b.go\n"},
		},
	}
	withFake(t, f)
	warns, err := EnsureCleanEnough(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 2 {
		t.Fatalf("expected 2 warnings, got %d: %v", len(warns), warns)
	}
}

func TestEnsureCleanEnoughClean(t *testing.T) {
	f := &fakeRunner{
		Responses: map[string]fakeResponse{
			"status --porcelain": {Stdout: ""},
		},
	}
	withFake(t, f)
	warns, err := EnsureCleanEnough(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Fatalf("expected no warnings, got %v", warns)
	}
}

// ----- diff helpers -------------------------------------------------------

func TestGetChangedFilesParsesPorcelain(t *testing.T) {
	f := &fakeRunner{
		Responses: map[string]fakeResponse{
			"status --porcelain": {Stdout: " M a/x.go\nA  b/y.go\n?? c/z.go\nR  old.go -> new.go\n M a/x.go\n"},
		},
	}
	withFake(t, f)
	got, err := GetChangedFiles(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a/x.go", "b/y.go", "c/z.go", "new.go"}
	if !equal(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestApplyPatchRejectsMissingFile(t *testing.T) {
	f := &fakeRunner{}
	withFake(t, f)
	if err := ApplyPatch(context.Background(), "", filepath.Join(t.TempDir(), "nope.patch")); err == nil {
		t.Fatal("expected error for missing patch")
	}
	if len(f.Calls) != 0 {
		t.Fatalf("git should not be called: %+v", f.Calls)
	}
}

func TestApplyPatchHappy(t *testing.T) {
	dir := t.TempDir()
	patch := filepath.Join(dir, "p.patch")
	if err := os.WriteFile(patch, []byte("--- a\n+++ b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &fakeRunner{}
	withFake(t, f)
	if err := ApplyPatch(context.Background(), dir, patch); err != nil {
		t.Fatal(err)
	}
	if len(f.Calls) != 1 {
		t.Fatalf("expected 1 call, got %+v", f.Calls)
	}
	want := []string{"apply", patch}
	if !equal(f.Calls[0].Args, want) {
		t.Fatalf("args=%v want %v", f.Calls[0].Args, want)
	}
}

// ----- helpers ------------------------------------------------------------

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
