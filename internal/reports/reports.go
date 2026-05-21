// Package reports renders human-readable proof packs from RunReports.
package reports

import (
	"bytes"
	_ "embed"
	"text/template"

	"github.com/awo-dev/awo/internal/domain"
)

//go:embed templates/proof_pack.md.tmpl
var proofPackTmpl string

// RenderProofPack renders a markdown proof pack for the given run.
func RenderProofPack(r domain.RunReport) (string, error) {
	t, err := template.New("proof").Parse(proofPackTmpl)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, r); err != nil {
		return "", err
	}
	return buf.String(), nil
}
