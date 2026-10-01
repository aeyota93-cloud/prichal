package main

import (
	"encoding/json"
	"os"
)

// writeJSONAtomic saves v as JSON readable only by the panel. It writes a
// temporary file and renames it, so a crash never leaves half a file.
func writeJSONAtomic(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
