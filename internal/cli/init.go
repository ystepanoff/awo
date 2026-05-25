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

// AWO-managed sections in markdown / gitignore files are bracketed by
// these markers so `awo init --force` can refresh them without touching
// user-written content around them. Markers are fixed strings so prior
// runs remain detectable even after content drifts.
const (
	mdMarkerBegin = "<!-- AWO:BEGIN -->"
	mdMarkerEnd   = "<!-- AWO:END -->"
	giMarkerBegin = "# AWO BEGIN"
	giMarkerEnd   = "# AWO END"
)

const (
	awoReadmeContent = `# .awo

This directory holds AWO orchestration state.

- runs/      structured artifacts for individual agent runs
- worktrees/ isolated git worktrees created by AWO

Both directories are ignored by git. Do not commit them.
AWO never auto-merges, auto-commits, or auto-pushes.
`

	// awoInstructionsBody is the text that goes between the AWO markers
	// in CLAUDE.md and AGENTS.md. It must stay concise — these files are
	// loaded into every agent's context on every run.
	awoInstructionsBody = `This repo uses AWO (Agent Worktree Orchestrator) for agent orchestration.

- Do not commit unless explicitly asked.
- Do not push.
- Do not merge.
- Keep patches focused.
- Prefer tests.
- Do not modify protected paths unless required by the task.
- Summarize tests and risks at the end of your work.
- AWO will run deterministic verification commands separately.
`
)

// gitignoreEntries are the AWO-owned lines kept inside the
// "# AWO BEGIN" / "# AWO END" block in .gitignore.
var gitignoreEntries = []string{".awo/runs/", ".awo/worktrees/"}

func newInitCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize AWO scaffolding in the current repo",
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			return runInit(cmd.OutOrStdout(), cwd, force)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite AWO-managed sections in existing files")
	return cmd
}

// runInit writes AWO scaffolding under root. Without force, existing
// files are never modified — the caller is told they were skipped.
// With force, AWO-owned files (awo.config.json, .awo/README.md) are
// rewritten in full and AWO-managed sections inside CLAUDE.md /
// AGENTS.md / .gitignore are refreshed in place; user content outside
// the marker block is left untouched.
func runInit(out io.Writer, root string, force bool) error {
	created := []string{}
	updated := []string{}
	skipped := []string{}

	// Whole-file owned by AWO: overwritten on --force.
	for _, f := range []struct {
		path  string
		body  string
		label string
	}{
		{filepath.Join(root, "awo.config.json"), config.DefaultJSON(), "awo.config.json"},
		{filepath.Join(root, ".awo", "README.md"), awoReadmeContent, ".awo/README.md"},
	} {
		c, u, err := writeOwnedFile(f.path, []byte(f.body), force)
		if err != nil {
			return err
		}
		switch {
		case c:
			created = append(created, f.label)
		case u:
			updated = append(updated, f.label+" (overwritten with --force)")
		default:
			skipped = append(skipped, f.label+" (exists)")
		}
	}

	awoDir := filepath.Join(root, ".awo")
	if err := os.MkdirAll(filepath.Join(awoDir, "runs"), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(awoDir, "worktrees"), 0o755); err != nil {
		return err
	}

	// Marker-managed: AWO owns only the section between markers.
	for _, f := range []struct {
		path  string
		body  string
		label string
	}{
		{filepath.Join(root, "CLAUDE.md"), awoInstructionsBody, "CLAUDE.md"},
		{filepath.Join(root, "AGENTS.md"), awoInstructionsBody, "AGENTS.md"},
	} {
		state, err := writeMarkerSection(f.path, f.body, force)
		if err != nil {
			return err
		}
		switch state {
		case sectionCreatedFile:
			created = append(created, f.label)
		case sectionAppendedBlock:
			updated = append(updated, f.label+" (AWO section appended)")
		case sectionReplacedBlock:
			updated = append(updated, f.label+" (AWO section refreshed with --force)")
		case sectionSkipped:
			skipped = append(skipped, f.label+" (exists, AWO section already present)")
		}
	}

	// .gitignore: idempotent merge. With --force, the marker block is
	// rewritten; without it, missing entries are appended.
	switch state, err := updateGitignore(filepath.Join(root, ".gitignore"), force); {
	case err != nil:
		return err
	case state == sectionCreatedFile:
		created = append(created, ".gitignore")
	case state == sectionAppendedBlock:
		updated = append(updated, ".gitignore (AWO entries added)")
	case state == sectionReplacedBlock:
		updated = append(updated, ".gitignore (AWO entries refreshed with --force)")
	case state == sectionSkipped:
		skipped = append(skipped, ".gitignore (AWO entries already present)")
	}

	fmt.Fprintln(out, "AWO initialized.")
	if len(created) > 0 {
		fmt.Fprintln(out, "  created:")
		for _, c := range created {
			fmt.Fprintln(out, "    -", c)
		}
	}
	if len(updated) > 0 {
		fmt.Fprintln(out, "  updated:")
		for _, u := range updated {
			fmt.Fprintln(out, "    -", u)
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

// writeOwnedFile writes content to path. If the file does not exist, it
// is created (created=true). If it exists and force is set, it is
// overwritten (updated=true). Otherwise it is left alone.
func writeOwnedFile(path string, content []byte, force bool) (created, updated bool, err error) {
	if _, err := os.Stat(path); err == nil {
		if !force {
			return false, false, nil
		}
		if err := writeFileWithDir(path, content); err != nil {
			return false, false, err
		}
		return false, true, nil
	} else if !os.IsNotExist(err) {
		return false, false, err
	}
	if err := writeFileWithDir(path, content); err != nil {
		return false, false, err
	}
	return true, false, nil
}

func writeFileWithDir(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, content, 0o644)
}

// sectionState describes what writeMarkerSection / updateGitignore did.
type sectionState int

const (
	sectionSkipped sectionState = iota
	sectionCreatedFile
	sectionAppendedBlock
	sectionReplacedBlock
)

// writeMarkerSection ensures path contains an AWO-managed section
// surrounded by mdMarkerBegin / mdMarkerEnd markers.
//
//   - If path is missing, it's created with a fresh marker block.
//   - If path exists and force is false, the file is left alone — we
//     don't append or modify anything; the user's file is theirs.
//   - If path exists and force is true: when an AWO marker block is
//     present, it is replaced in place; otherwise a fresh block is
//     appended after the user's content.
func writeMarkerSection(path, body string, force bool) (sectionState, error) {
	existing, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if err := writeFileWithDir(path, []byte(renderMarkerBlock(body)+"\n")); err != nil {
			return sectionSkipped, err
		}
		return sectionCreatedFile, nil
	}
	if err != nil {
		return sectionSkipped, err
	}
	if !force {
		return sectionSkipped, nil
	}

	src := string(existing)
	begin := strings.Index(src, mdMarkerBegin)
	end := strings.Index(src, mdMarkerEnd)
	hasMarkers := begin >= 0 && end > begin

	if hasMarkers {
		endLineEnd := end + len(mdMarkerEnd)
		replaced := src[:begin] + renderMarkerBlock(body) + src[endLineEnd:]
		if err := os.WriteFile(path, []byte(replaced), 0o644); err != nil {
			return sectionSkipped, err
		}
		return sectionReplacedBlock, nil
	}

	var b strings.Builder
	b.WriteString(src)
	if !strings.HasSuffix(src, "\n") {
		b.WriteString("\n")
	}
	if !strings.HasSuffix(src, "\n\n") {
		b.WriteString("\n")
	}
	b.WriteString(renderMarkerBlock(body))
	b.WriteString("\n")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return sectionSkipped, err
	}
	return sectionAppendedBlock, nil
}

func renderMarkerBlock(body string) string {
	body = strings.TrimRight(body, "\n")
	return mdMarkerBegin + "\n" + body + "\n" + mdMarkerEnd
}

// updateGitignore keeps the AWO entries in path, using "# AWO BEGIN" /
// "# AWO END" markers when possible. The pre-marker form (bare lines
// emitted by older init runs) is still recognized so legacy files don't
// trigger duplicate entries.
func updateGitignore(path string, force bool) (sectionState, error) {
	existing, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if err := os.WriteFile(path, []byte(renderGitignoreBlock()+"\n"), 0o644); err != nil {
			return sectionSkipped, err
		}
		return sectionCreatedFile, nil
	}
	if err != nil {
		return sectionSkipped, err
	}

	src := string(existing)
	begin := strings.Index(src, giMarkerBegin)
	end := strings.Index(src, giMarkerEnd)
	hasMarkers := begin >= 0 && end > begin

	if hasMarkers {
		if !force {
			return sectionSkipped, nil
		}
		endLineEnd := end + len(giMarkerEnd)
		replaced := src[:begin] + renderGitignoreBlock() + src[endLineEnd:]
		if err := os.WriteFile(path, []byte(replaced), 0o644); err != nil {
			return sectionSkipped, err
		}
		return sectionReplacedBlock, nil
	}

	missing := []string{}
	for _, e := range gitignoreEntries {
		if !containsLine(src, e) {
			missing = append(missing, e)
		}
	}
	if len(missing) == 0 {
		return sectionSkipped, nil
	}

	var b strings.Builder
	b.WriteString(src)
	if src != "" && !strings.HasSuffix(src, "\n") {
		b.WriteString("\n")
	}
	if src != "" && !strings.HasSuffix(src, "\n\n") {
		b.WriteString("\n")
	}
	b.WriteString(renderGitignoreBlock())
	b.WriteString("\n")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return sectionSkipped, err
	}
	return sectionAppendedBlock, nil
}

func renderGitignoreBlock() string {
	var b strings.Builder
	b.WriteString(giMarkerBegin)
	b.WriteString("\n")
	for _, e := range gitignoreEntries {
		b.WriteString(e)
		b.WriteString("\n")
	}
	b.WriteString(giMarkerEnd)
	return b.String()
}

func containsLine(haystack, line string) bool {
	for _, l := range strings.Split(haystack, "\n") {
		if strings.TrimSpace(l) == line {
			return true
		}
	}
	return false
}
