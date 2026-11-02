package guardian

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"os"
	"strings"
)

// ensureDir makes a folder only Guardian can read.
func ensureDir(path string) error { return os.MkdirAll(path, 0o700) }

// shortHash is a cheap fingerprint for change detection, not security.
func shortHash(s string) string {
	if s == "" {
		return ""
	}
	h := fnv.New32a()
	h.Write([]byte(s))
	return fmt.Sprintf("%08x", h.Sum32())
}

// randomID is 16 random hex digits.
func randomID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
