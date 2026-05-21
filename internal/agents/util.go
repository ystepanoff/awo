package agents

import (
	"io"
	"strings"
)

func stringReader(s string) io.Reader {
	if s == "" {
		return nil
	}
	return strings.NewReader(s)
}
