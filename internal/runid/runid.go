// Package runid produces sortable, human-readable identifiers for AWO runs.
package runid

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// New returns an id like "20260521T143015-7f3a" using UTC time and a
// short random suffix.
func New() string {
	return newAt(time.Now().UTC())
}

func newAt(t time.Time) string {
	var b [2]byte
	_, _ = rand.Read(b[:])
	return t.Format("20060102T150405") + "-" + hex.EncodeToString(b[:])
}
