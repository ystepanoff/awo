package agents

import (
	"bytes"
	"embed"
	"fmt"
	"text/template"
)

//go:embed templates/*.md.tmpl
var promptTemplates embed.FS

// PromptInput is the data passed to every prompt template.
type PromptInput struct {
	Task           string
	Mode           string
	WorktreePath   string
	ChangedFiles   []string
	Diff           string
	ProtectedPaths []string
	ExtraContext   map[string]string
}

// BuildWriterPrompt renders the writer prompt.
func BuildWriterPrompt(input PromptInput) (string, error) {
	return renderPrompt("templates/writer.md.tmpl", input)
}

// BuildReviewerPrompt renders the reviewer prompt.
func BuildReviewerPrompt(input PromptInput) (string, error) {
	return renderPrompt("templates/reviewer.md.tmpl", input)
}

// BuildCompetitorPrompt renders the competitor prompt.
func BuildCompetitorPrompt(input PromptInput) (string, error) {
	return renderPrompt("templates/competitor.md.tmpl", input)
}

func renderPrompt(name string, input PromptInput) (string, error) {
	if input.Task == "" {
		return "", fmt.Errorf("agents: empty task in PromptInput")
	}
	tmpl, err := template.ParseFS(promptTemplates, name)
	if err != nil {
		return "", fmt.Errorf("agents: parse %s: %w", name, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, input); err != nil {
		return "", fmt.Errorf("agents: execute %s: %w", name, err)
	}
	return buf.String(), nil
}
