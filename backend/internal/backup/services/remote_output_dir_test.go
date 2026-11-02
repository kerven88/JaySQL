package services

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRemoteOutputDirectoryDoesNotTouchServerFilesystem(t *testing.T) {
	target := filepath.Join(t.TempDir(), "agent-only", "backup")
	got, err := resolveBackupOutputBaseDir(target)
	if err != nil || got != target {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("server touched remote path: %v", err)
	}
	// Even if this path is a file on the API server, the remote node may have a directory there.
	localFile := filepath.Join(t.TempDir(), "local-file")
	if err := os.WriteFile(localFile, []byte("local"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveBackupOutputBaseDir(localFile); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{"relative/path", "/"} {
		if _, err := resolveBackupOutputBaseDir(invalid); err == nil {
			t.Fatalf("accepted %q", invalid)
		}
	}
}
