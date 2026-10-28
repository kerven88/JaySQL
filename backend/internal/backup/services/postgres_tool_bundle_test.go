package services

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSelectBundledPostgresToolBundlePrefersProfileManifest(t *testing.T) {
	root := t.TempDir()
	t.Setenv("GOINSIGHT_BACKUP_TOOL_HOME", root)

	genericDir := filepath.Join(root, "postgres", "16")
	profileDir := filepath.Join(root, "postgres", "16", "linux-x86_64-glibc")
	mustWriteExecutable(t, filepath.Join(genericDir, "bin", "pgbackrest"))
	mustWriteExecutable(t, filepath.Join(profileDir, "bin", "pgbackrest"))
	mustWriteFile(t, filepath.Join(profileDir, "manifest.json"), `{"target_os":"linux","target_arch":"x86_64","libc":"glibc","tools":["pgbackrest"]}`)

	bundle, err := selectBundledPostgresToolBundle("16.6", remoteRuntimeProfile{
		OS:   "linux",
		Arch: "x86_64",
		Libc: "glibc",
	}, []string{"pgbackrest"})
	if err != nil {
		t.Fatalf("select bundle failed: %v", err)
	}
	if bundle.RootDir != profileDir {
		t.Fatalf("expected profile bundle %s, got %s", profileDir, bundle.RootDir)
	}
}

func TestSelectBundledPostgresToolBundleFallsBackToGeneric(t *testing.T) {
	root := t.TempDir()
	t.Setenv("GOINSIGHT_BACKUP_TOOL_HOME", root)

	genericDir := filepath.Join(root, "postgres", "16")
	mustWriteExecutable(t, filepath.Join(genericDir, "bin", "pgbackrest"))
	mustWriteExecutable(t, filepath.Join(genericDir, "bin", "psql"))

	bundle, err := selectBundledPostgresToolBundle("16.3", remoteRuntimeProfile{
		OS:   "linux",
		Arch: "arm64",
		Libc: "glibc",
	}, []string{"pgbackrest", "psql"})
	if err != nil {
		t.Fatalf("select generic bundle failed: %v", err)
	}
	if bundle.RootDir != genericDir {
		t.Fatalf("expected generic bundle %s, got %s", genericDir, bundle.RootDir)
	}
}

func mustWriteExecutable(t *testing.T, path string) {
	t.Helper()
	mustWriteFileMode(t, path, "#!/bin/sh\nexit 0\n", 0o755)
}

func mustWriteFile(t *testing.T, path string, content string) {
	t.Helper()
	mustWriteFileMode(t, path, content, 0o644)
}

func mustWriteFileMode(t *testing.T, path string, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("write file failed: %v", err)
	}
}
