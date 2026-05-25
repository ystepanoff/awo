// Package examples builds small, self-contained fixture repos that
// users can dogfood AWO against without risk to their real codebase.
//
// The fixture is generated deterministically — file content is fixed,
// git commit metadata is set from constants so the user's identity
// never leaks into the fixture's history. AWO never touches the outer
// repo's git state from this package: every git invocation has
// cmd.Dir pinned to the fixture directory.
package examples

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
)

// SampleGoAppName is the directory name (under the fixtures root) that
// holds the sample Go-module fixture.
const SampleGoAppName = "sample-go-app"

// fixtureMarkerName is dropped into the fixture root so that
// CreateSampleGoApp can tell, on a force-overwrite, whether the
// directory actually came from us — and refuse to delete unrelated
// user data that happens to live at the same path.
const fixtureMarkerName = ".awo-fixture"

// SampleGoAppResult describes what CreateSampleGoApp did.
type SampleGoAppResult struct {
	Path           string
	Files          []string
	Replaced       bool
	GitCommit      string
	BranchName     string
}

// CreateSampleGoApp materializes the sample Go fixture under
// targetDir/sample-go-app and initializes a git repo with a single
// initial commit inside that directory only.
//
// targetDir is the parent directory (typically <repo>/.awo/fixtures);
// CreateSampleGoApp creates it if missing.
//
// If the fixture directory already exists, CreateSampleGoApp checks
// for the AWO fixture marker. With force=false it always refuses.
// With force=true it refuses if the marker is missing (so we never
// delete unrelated user data) and otherwise removes the directory and
// regenerates it.
//
// out receives progress lines; pass io.Discard to silence.
func CreateSampleGoApp(targetDir string, force bool, out io.Writer) (SampleGoAppResult, error) {
	if targetDir == "" {
		return SampleGoAppResult{}, errors.New("examples: empty target dir")
	}
	if out == nil {
		out = io.Discard
	}

	dir := filepath.Join(targetDir, SampleGoAppName)
	res := SampleGoAppResult{Path: dir}

	switch existing, err := classifyTarget(dir); {
	case err != nil:
		return res, err
	case existing == targetMissing:
		// proceed
	case existing == targetIsAwoFixture:
		if !force {
			return res, fmt.Errorf("examples: fixture already exists at %s; pass --force to overwrite", dir)
		}
		if err := os.RemoveAll(dir); err != nil {
			return res, fmt.Errorf("examples: remove existing fixture: %w", err)
		}
		res.Replaced = true
	case existing == targetExistsForeign:
		return res, fmt.Errorf("examples: %s exists and is not an AWO fixture; refusing to touch it", dir)
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return res, err
	}

	names := make([]string, 0, len(sampleGoAppFiles))
	for n := range sampleGoAppFiles {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, name := range names {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(sampleGoAppFiles[name]), 0o644); err != nil {
			return res, fmt.Errorf("examples: write %s: %w", path, err)
		}
		res.Files = append(res.Files, name)
	}

	commit, branch, err := initFixtureRepo(dir)
	if err != nil {
		return res, err
	}
	res.GitCommit = commit
	res.BranchName = branch

	fmt.Fprintf(out, "  fixture: %s (%d files, branch %s)\n", dir, len(res.Files), branch)
	return res, nil
}

type targetState int

const (
	targetMissing targetState = iota
	targetIsAwoFixture
	targetExistsForeign
)

func classifyTarget(dir string) (targetState, error) {
	info, err := os.Stat(dir)
	if os.IsNotExist(err) {
		return targetMissing, nil
	}
	if err != nil {
		return targetMissing, err
	}
	if !info.IsDir() {
		return targetExistsForeign, nil
	}
	if _, err := os.Stat(filepath.Join(dir, fixtureMarkerName)); err == nil {
		return targetIsAwoFixture, nil
	} else if !os.IsNotExist(err) {
		return targetMissing, err
	}
	return targetExistsForeign, nil
}

// initFixtureRepo runs git init / add / commit inside dir only. Author
// and committer metadata come from fixed constants so the fixture's
// commit doesn't carry the user's identity. Returns the new commit
// hash and the current branch name.
func initFixtureRepo(dir string) (string, string, error) {
	env := append(os.Environ(),
		"GIT_AUTHOR_NAME=AWO Fixture",
		"GIT_AUTHOR_EMAIL=fixture@awo.local",
		"GIT_COMMITTER_NAME=AWO Fixture",
		"GIT_COMMITTER_EMAIL=fixture@awo.local",
	)

	steps := [][]string{
		{"init", "-q"},
		{"add", "."},
		{"commit", "-q", "-m", "Initial fixture commit"},
	}
	for _, args := range steps {
		c := exec.Command("git", args...)
		c.Dir = dir
		c.Env = env
		if out, err := c.CombinedOutput(); err != nil {
			return "", "", fmt.Errorf("examples: git %s: %v: %s", args[0], err, out)
		}
	}

	commit, err := readGitOutput(dir, env, "rev-parse", "HEAD")
	if err != nil {
		return "", "", err
	}
	branch, err := readGitOutput(dir, env, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", "", err
	}
	return commit, branch, nil
}

func readGitOutput(dir string, env []string, args ...string) (string, error) {
	c := exec.Command("git", args...)
	c.Dir = dir
	c.Env = env
	out, err := c.Output()
	if err != nil {
		return "", fmt.Errorf("examples: git %v: %w", args, err)
	}
	return trimTrailingNewline(string(out)), nil
}

func trimTrailingNewline(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
