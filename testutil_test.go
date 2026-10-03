package main

import (
	"os"
	"testing"
	"time"
)

// tempDir is t.TempDir for every test that writes files into it. On Windows the
// indexer or antivirus may still hold a handle on a just-written *.json(.tmp)
// after the file is deleted, and the removal of the directory then fails with "directory is not empty" although nothing is left
// in it (testing retries other errors, not this one). Nothing of ours is
// running by then, so removal is retried until the system lets go; a directory
// that really cannot be removed still fails the test.
func tempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "prichal-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		deadline := time.Now().Add(5 * time.Second)
		for {
			err := os.RemoveAll(dir)
			if err == nil {
				return
			}
			if time.Now().After(deadline) {
				t.Errorf("remove %s: %v", dir, err)
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	})
	return dir
}
