package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/awo-dev/awo/internal/config"
	"github.com/spf13/cobra"
)

const (
	awoReadmeContent = `# .awo

This directory holds AWO orchestration state.

- runs/      structured artifacts for individual agent runs
- worktrees/ isolated git worktrees created by AWO

Both directories are ignored by git. Do not commit them.
AWO never auto-merges, auto-commits, or auto-pushes.
`

	claudeMdContent = `# CLAUDE.md

This repository uses AWO (Agent Worktree Orchestrator) to coordinate
Claude Code work inside isolated git worktrees.

## Ground rules for agents

- You are operating inside an AWO worktree. Treat it as your sandbox.
- Do not run destructive git commands.
- Do not commit, merge, push, or rewrite history.
- Stop and report when work is complete; verification is run separately.
- Do not modify files outside the worktree.

## Verification

A deterministic verification command is run after your work completes.
The exit code of that command is the source of truth for success.
Do not claim tests pass without running them.
`

	agentsMdContent = `# AGENTS.md

This file describes how AI agents should behave when working in this repo
under AWO (Agent Worktree Orchestrator).

## Modes

- single:           one agent does the work end-to-end.
- writer-reviewer:  one agent writes, another reviews and proposes changes.
- competitive:      multiple agents attempt the task; results are compared.

## Hard constraints

- No auto-commits.
- No auto-merges.
- No pushes.
- No deletions outside the AWO worktree.
- Verification commands are the only trusted signal of success.
`
)

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize AWO scaffolding in the current repo",
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			return runInit(cmd.OutOrStdout(), cwd)
		},
	}
}

func runInit(out io.Writer, root string) error {
	created := []string{}
	skipped := []string{}

	configPath := filepath.Join(root, "awo.config.json")
	if c, err := writeIfMissing(configPath, []byte(config.DefaultJSON())); err != nil {
		return err
	} else if c {
		created = append(created, "awo.config.json")
	} else {
		skipped = append(skipped, "awo.config.json (exists)")
	}

	awoDir := filepath.Join(root, ".awo")
	if err := os.MkdirAll(filepath.Join(awoDir, "runs"), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(awoDir, "worktrees"), 0o755); err != nil {
		return err
	}

	readmePath := filepath.Join(awoDir, "README.md")
	if c, err := writeIfMissing(readmePath, []byte(awoReadmeContent)); err != nil {
		return err
	} else if c {
		created = append(created, ".awo/README.md")
	} else {
		skipped = append(skipped, ".awo/README.md (exists)")
	}

	if c, err := ensureGitignore(filepath.Join(root, ".gitignore")); err != nil {
		return err
	} else if c {
		created = append(created, ".gitignore (entries added)")
	} else {
		skipped = append(skipped, ".gitignore (entries already present)")
	}

	for _, f := range []struct {
		path    string
		content string
	}{
		{filepath.Join(root, "CLAUDE.md"), claudeMdContent},
		{filepath.Join(root, "AGENTS.md"), agentsMdContent},
	} {
		if c, err := writeIfMissing(f.path, []byte(f.content)); err != nil {
			return err
		} else if c {
			created = append(created, filepath.Base(f.path))
		} else {
			skipped = append(skipped, filepath.Base(f.path)+" (exists, left untouched)")
		}
	}

	fmt.Fprintln(out, "AWO initialized.")
	if len(created) > 0 {
		fmt.Fprintln(out, "  created:")
		for _, c := range created {
			fmt.Fprintln(out, "    -", c)
		}
	}
	if len(skipped) > 0 {
		fmt.Fprintln(out, "  skipped:")
		for _, s := range skipped {
			fmt.Fprintln(out, "    -", s)
		}
	}
	return nil
}

func writeIfMissing(path string, content []byte) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return false, err
	}
	return true, nil
}

func ensureGitignore(path string) (bool, error) {
	wanted := []string{".awo/runs/", ".awo/worktrees/"}

	var existing string
	if b, err := os.ReadFile(path); err == nil {
		existing = string(b)
	} else if !os.IsNotExist(err) {
		return false, err
	}

	missing := []string{}
	for _, w := range wanted {
		if !containsLine(existing, w) {
			missing = append(missing, w)
		}
	}
	if len(missing) == 0 {
		return false, nil
	}

	var b strings.Builder
	b.WriteString(existing)
	if existing != "" && !strings.HasSuffix(existing, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("\n# AWO\n")
	for _, m := range missing {
		b.WriteString(m)
		b.WriteString("\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

func containsLine(haystack, line string) bool {
	for _, l := range strings.Split(haystack, "\n") {
		if strings.TrimSpace(l) == line {
			return true
		}
	}
	return false
}
