// Package runid produces sortable, human-readable identifiers for AWO runs.
//
// IDs look like "20260521-143022-a1b2c3": a UTC timestamp (YYYYMMDD-HHMMSS)
// joined with a 6-character random hex suffix. They sort lexicographically
// by time, are filesystem-safe on Unix and Windows, and stay short enough to
// embed in branch names without truncation.
package runid

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"time"
)

// Pattern matches valid AWO run ids.
var Pattern = regexp.MustCompile(`^\d{8}-\d{6}-[0-9a-f]{6}$`)

// New returns a fresh run id using the current UTC time and a 3-byte
// random suffix.
func New() string {
	return newAt(time.Now().UTC())
}

func newAt(t time.Time) string {
	var b [3]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand on a healthy host doesn't fail; fall back to a
		// time-derived suffix so callers still get a unique-enough id.
		return t.Format("20060102-150405") + "-" + fmt.Sprintf("%06x", t.UnixNano()&0xFFFFFF)
	}
	return t.Format("20060102-150405") + "-" + hex.EncodeToString(b[:])
}

// Validate returns nil if id matches the canonical run-id shape.
func Validate(id string) error {
	if id == "" {
		return errors.New("runid: empty id")
	}
	if !Pattern.MatchString(id) {
		return fmt.Errorf("runid: invalid id %q (want YYYYMMDD-HHMMSS-XXXXXX)", id)
	}
	return nil
}
