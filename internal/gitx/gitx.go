// Package gitx wraps the subset of the git CLI that AWO uses.
//
// Hard rules:
//   - AWO never deletes branches.
//   - AWO refuses to remove worktrees outside <repo-root>/.awo/worktrees/.
//   - AWO never talks to a remote: no fetch, no push.
//   - All git invocations are funnelled through a swappable Runner so the
//     package can be exercised by tests with no real git on the host.
package gitx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/awo-dev/awo/internal/execx"
	"github.com/awo-dev/awo/internal/safety"
)

const (
	// DefaultBranchPrefix is the conventional prefix for AWO branches.
	DefaultBranchPrefix = "awo"
	// AwoWorktreesSubdir is the worktree root, relative to the repo root.
	AwoWorktreesSubdir = ".awo/worktrees"
)

// Runner runs `git <args>` with cwd as the working directory. cwd may be
// empty for commands that don't need one (like `git --version`).
type Runner func(ctx context.Context, cwd string, args []string) (stdout, stderr string, exitCode int, err error)

var defaultRunner Runner = execGitRunner

// SetRunner swaps the package's git Runner and returns the old one. Tests
// use this to inject a fake runner; production code never calls it.
func SetRunner(r Runner) Runner {
	old := defaultRunner
	defaultRunner = r
	return old
}

func runGit(ctx context.Context, cwd string, args ...string) (string, string, int, error) {
	return defaultRunner(ctx, cwd, args)
}

func execGitRunner(ctx context.Context, cwd string, args []string) (string, string, int, error) {
	tmp, err := os.MkdirTemp("", "awo-gitx-*")
	if err != nil {
		return "", "", -1, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	stdoutPath := filepath.Join(tmp, "stdout")
	stderrPath := filepath.Join(tmp, "stderr")
	res, err := execx.Run(ctx, execx.CommandSpec{
		Command:    "git",
		Args:       args,
		Cwd:        cwd,
		StdoutPath: stdoutPath,
		StderrPath: stderrPath,
	})
	if err != nil {
		return "", "", -1, err
	}
	so, _ := os.ReadFile(stdoutPath)
	se, _ := os.ReadFile(stderrPath)
	return string(so), string(se), res.ExitCode, nil
}

// ----- repo introspection -------------------------------------------------

// Version returns the output of `git --version`.
func Version(ctx context.Context) (string, error) {
	so, se, code, err := runGit(ctx, "", "--version")
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("git --version exited %d: %s", code, strings.TrimSpace(se))
	}
	return strings.TrimSpace(so), nil
}

// GetRepoRoot returns the absolute path of the repo root containing cwd.
func GetRepoRoot(ctx context.Context, cwd string) (string, error) {
	so, se, code, err := runGit(ctx, cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("not a git repository (rev-parse: %s)", strings.TrimSpace(se))
	}
	return strings.TrimSpace(so), nil
}

// IsInsideGitRepo reports whether cwd is inside a git work tree.
func IsInsideGitRepo(ctx context.Context, cwd string) bool {
	so, _, code, err := runGit(ctx, cwd, "rev-parse", "--is-inside-work-tree")
	if err != nil || code != 0 {
		return false
	}
	return strings.TrimSpace(so) == "true"
}

// GetCurrentBranch returns the current branch name. For a detached HEAD
// it returns an empty string and a nil error.
func GetCurrentBranch(ctx context.Context, cwd string) (string, error) {
	so, se, code, err := runGit(ctx, cwd, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return "", err
	}
	if code != 0 {
		// Detached HEAD: --quiet returns exit 1 with empty output.
		if strings.TrimSpace(se) == "" {
			return "", nil
		}
		return "", fmt.Errorf("git symbolic-ref exited %d: %s", code, strings.TrimSpace(se))
	}
	return strings.TrimSpace(so), nil
}

// GetCurrentHeadSHA returns HEAD's commit SHA.
func GetCurrentHeadSHA(ctx context.Context, cwd string) (string, error) {
	so, se, code, err := runGit(ctx, cwd, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("git rev-parse HEAD exited %d: %s", code, strings.TrimSpace(se))
	}
	return strings.TrimSpace(so), nil
}

// WorkingTreeStatus returns raw `git status --porcelain` output for cwd.
func WorkingTreeStatus(ctx context.Context, cwd string) (string, error) {
	so, se, code, err := runGit(ctx, cwd, "status", "--porcelain")
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("git status exited %d: %s", code, strings.TrimSpace(se))
	}
	return so, nil
}

// EnsureCleanEnough returns one warning string per dirty path. An empty
// slice means clean. The function never blocks: it surfaces warnings, the
// caller decides what to do with them.
func EnsureCleanEnough(ctx context.Context, cwd string) ([]string, error) {
	s, err := WorkingTreeStatus(ctx, cwd)
	if err != nil {
		return nil, err
	}
	var warnings []string
	for _, line := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		warnings = append(warnings, line)
	}
	return warnings, nil
}

// ----- worktree management ------------------------------------------------

// CreateWorktreeOptions controls CreateWorktree.
type CreateWorktreeOptions struct {
	RepoRoot     string
	RunID        string
	Agent        string
	Role         string
	BaseBranch   string // empty means HEAD
	BranchPrefix string // empty falls back to DefaultBranchPrefix
}

// WorktreeInfo describes one AWO-managed worktree.
type WorktreeInfo struct {
	Path   string `json:"path"`
	Branch string `json:"branch"`
	HEAD   string `json:"head,omitempty"`
	RunID  string `json:"runId,omitempty"`
	Agent  string `json:"agent,omitempty"`
	Role   string `json:"role,omitempty"`
}

// BranchName builds the AWO branch name for a (run, agent, role) triple.
// The prefix must start with DefaultBranchPrefix. Empty parts are
// rejected.
func BranchName(prefix, runID, agent, role string) (string, error) {
	if runID == "" || agent == "" || role == "" {
		return "", errors.New("gitx: BranchName: runID, agent, role required")
	}
	p := normalizePrefix(prefix)
	if !strings.HasPrefix(p, DefaultBranchPrefix) {
		return "", fmt.Errorf("gitx: branchPrefix must start with %q (got %q)", DefaultBranchPrefix, p)
	}
	return fmt.Sprintf("%s/%s/%s-%s", p, runID, agent, role), nil
}

// WorktreePath builds the absolute worktree path for a (run, agent, role)
// triple under the repo root. Path components are validated by SafeJoin.
func WorktreePath(repoRoot, runID, agent, role string) (string, error) {
	if runID == "" || agent == "" || role == "" {
		return "", errors.New("gitx: WorktreePath: runID, agent, role required")
	}
	return safety.SafeJoin(repoRoot, AwoWorktreesSubdir, runID, agent+"-"+role)
}

func normalizePrefix(p string) string {
	p = strings.TrimRight(p, "/")
	if p == "" {
		return DefaultBranchPrefix
	}
	return p
}

// CreateWorktree creates a new AWO worktree and a fresh branch from
// BaseBranch (or HEAD when BaseBranch is empty). It does NOT fetch.
func CreateWorktree(ctx context.Context, opts CreateWorktreeOptions) (*WorktreeInfo, error) {
	if opts.RepoRoot == "" {
		return nil, errors.New("gitx: CreateWorktree: empty RepoRoot")
	}
	branch, err := BranchName(opts.BranchPrefix, opts.RunID, opts.Agent, opts.Role)
	if err != nil {
		return nil, err
	}
	wtPath, err := WorktreePath(opts.RepoRoot, opts.RunID, opts.Agent, opts.Role)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(wtPath), 0o755); err != nil {
		return nil, fmt.Errorf("gitx: mkdir worktree parent: %w", err)
	}

	base := opts.BaseBranch
	if base == "" {
		base = "HEAD"
	}
	args := []string{"worktree", "add", "-b", branch, wtPath, base}
	so, se, code, err := runGit(ctx, opts.RepoRoot, args...)
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, fmt.Errorf("git worktree add exited %d: %s\n%s",
			code, strings.TrimSpace(se), strings.TrimSpace(so))
	}

	head, herr := GetCurrentHeadSHA(ctx, wtPath)
	if herr != nil {
		head = ""
	}
	return &WorktreeInfo{
		Path:   wtPath,
		Branch: branch,
		HEAD:   head,
		RunID:  opts.RunID,
		Agent:  opts.Agent,
		Role:   opts.Role,
	}, nil
}

// ListWorktreesOptions controls ListAwoWorktrees.
type ListWorktreesOptions struct {
	RepoRoot     string
	RunID        string // optional filter
	BranchPrefix string // empty falls back to DefaultBranchPrefix
}

// ListAwoWorktrees returns AWO-managed worktrees under RepoRoot. It only
// returns entries whose path lies under <repo-root>/.awo/worktrees AND
// whose branch starts with BranchPrefix.
func ListAwoWorktrees(ctx context.Context, opts ListWorktreesOptions) ([]WorktreeInfo, error) {
	if opts.RepoRoot == "" {
		return nil, errors.New("gitx: ListAwoWorktrees: empty RepoRoot")
	}
	so, se, code, err := runGit(ctx, opts.RepoRoot, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, fmt.Errorf("git worktree list exited %d: %s", code, strings.TrimSpace(se))
	}
	raw := parseWorktreeListPorcelain(so)
	awoBase, err := safety.SafeJoin(opts.RepoRoot, AwoWorktreesSubdir)
	if err != nil {
		return nil, err
	}
	prefix := normalizePrefix(opts.BranchPrefix)

	var out []WorktreeInfo
	for _, w := range raw {
		if !safety.IsSubpath(awoBase, w.Path) {
			continue
		}
		if w.Branch == "" || !strings.HasPrefix(w.Branch, prefix+"/") {
			continue
		}
		rid, agent, role := parseAwoWorktreeLeaf(opts.RepoRoot, w.Path)
		if rid == "" {
			continue
		}
		if opts.RunID != "" && rid != opts.RunID {
			continue
		}
		out = append(out, WorktreeInfo{
			Path:   w.Path,
			Branch: w.Branch,
			HEAD:   w.HEAD,
			RunID:  rid,
			Agent:  agent,
			Role:   role,
		})
	}
	return out, nil
}

// RemoveWorktreeOptions controls RemoveWorktree.
type RemoveWorktreeOptions struct {
	RepoRoot     string
	WorktreePath string
	Force        bool
}

// RemoveWorktree removes a worktree. It refuses to remove anything that is
// not strictly inside <RepoRoot>/.awo/worktrees/ — protecting the user's
// real worktrees from accidental deletion. It NEVER deletes the branch.
func RemoveWorktree(ctx context.Context, opts RemoveWorktreeOptions) error {
	if opts.RepoRoot == "" {
		return errors.New("gitx: RemoveWorktree: empty RepoRoot")
	}
	if opts.WorktreePath == "" {
		return errors.New("gitx: RemoveWorktree: empty WorktreePath")
	}
	awoBase, err := safety.SafeJoin(opts.RepoRoot, AwoWorktreesSubdir)
	if err != nil {
		return fmt.Errorf("gitx: RemoveWorktree: derive awo base: %w", err)
	}
	if err := safety.MustBeUnder(awoBase, opts.WorktreePath); err != nil {
		return fmt.Errorf("gitx: refusing to remove worktree outside %q: %w", awoBase, err)
	}
	args := []string{"worktree", "remove"}
	if opts.Force {
		args = append(args, "--force")
	}
	args = append(args, opts.WorktreePath)
	_, se, code, err := runGit(ctx, opts.RepoRoot, args...)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("git worktree remove exited %d: %s", code, strings.TrimSpace(se))
	}
	return nil
}

// ----- diff helpers -------------------------------------------------------

// GetChangedFiles returns paths reported by `git status --porcelain` in
// worktreePath, including staged, unstaged, and untracked entries.
func GetChangedFiles(ctx context.Context, worktreePath string) ([]string, error) {
	so, se, code, err := runGit(ctx, worktreePath, "status", "--porcelain")
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, fmt.Errorf("git status exited %d: %s", code, strings.TrimSpace(se))
	}
	var out []string
	seen := map[string]struct{}{}
	for _, line := range strings.Split(strings.TrimRight(so, "\n"), "\n") {
		if len(line) < 4 {
			continue
		}
		path := line[3:]
		if i := strings.Index(path, " -> "); i >= 0 {
			path = path[i+4:]
		}
		path = strings.Trim(path, `"`)
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if _, dup := seen[path]; dup {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	return out, nil
}

// GetDiffPatch returns a unified diff of worktreePath against HEAD.
// Untracked files are NOT included (matching plain `git diff` semantics).
func GetDiffPatch(ctx context.Context, worktreePath string) (string, error) {
	so, se, code, err := runGit(ctx, worktreePath, "diff", "HEAD", "--no-color")
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("git diff exited %d: %s", code, strings.TrimSpace(se))
	}
	return so, nil
}

// GetDiffStat returns `git diff --stat` for worktreePath against HEAD.
func GetDiffStat(ctx context.Context, worktreePath string) (string, error) {
	so, se, code, err := runGit(ctx, worktreePath, "diff", "HEAD", "--stat", "--no-color")
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("git diff --stat exited %d: %s", code, strings.TrimSpace(se))
	}
	return so, nil
}

// ApplyPatch applies the patch at patchPath inside worktreePath. It does
// not commit and does not stage; the patch is left in the working tree.
func ApplyPatch(ctx context.Context, worktreePath, patchPath string) error {
	if patchPath == "" {
		return errors.New("gitx: ApplyPatch: empty patchPath")
	}
	if _, err := os.Stat(patchPath); err != nil {
		return fmt.Errorf("gitx: ApplyPatch: %w", err)
	}
	_, se, code, err := runGit(ctx, worktreePath, "apply", patchPath)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("git apply exited %d: %s", code, strings.TrimSpace(se))
	}
	return nil
}

// ----- porcelain parsing --------------------------------------------------

type rawWorktree struct {
	Path   string
	HEAD   string
	Branch string
}

// parseWorktreeListPorcelain parses the output of
// `git worktree list --porcelain`.
//
// Each entry is a block of "key value" lines, separated by blank lines.
// Branches are reported as "branch refs/heads/<name>". Detached entries
// have a "detached" line instead and we leave Branch empty.
func parseWorktreeListPorcelain(s string) []rawWorktree {
	var out []rawWorktree
	var cur *rawWorktree
	flush := func() {
		if cur != nil && cur.Path != "" {
			out = append(out, *cur)
		}
		cur = nil
	}
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		sp := strings.SplitN(line, " ", 2)
		key := sp[0]
		val := ""
		if len(sp) > 1 {
			val = sp[1]
		}
		if key == "worktree" {
			flush()
			cur = &rawWorktree{Path: val}
			continue
		}
		if cur == nil {
			continue
		}
		switch key {
		case "HEAD":
			cur.HEAD = val
		case "branch":
			cur.Branch = strings.TrimPrefix(val, "refs/heads/")
		}
	}
	flush()
	return out
}

// parseAwoWorktreeLeaf turns an absolute worktree path into (runID, agent,
// role) when it lives under <repoRoot>/.awo/worktrees/<runID>/<agent>-<role>.
// Returns empty strings when the path doesn't match that shape.
func parseAwoWorktreeLeaf(repoRoot, wtPath string) (runID, agent, role string) {
	base, err := safety.SafeJoin(repoRoot, AwoWorktreesSubdir)
	if err != nil {
		return
	}
	absBase, err := filepath.Abs(base)
	if err != nil {
		return
	}
	absWT, err := filepath.Abs(wtPath)
	if err != nil {
		return
	}
	rel, err := filepath.Rel(absBase, absWT)
	if err != nil {
		return
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) < 2 {
		return
	}
	runID = parts[0]
	leaf := parts[1]
	if i := strings.Index(leaf, "-"); i > 0 {
		agent = leaf[:i]
		role = leaf[i+1:]
	}
	return
}
