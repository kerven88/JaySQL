package services

import (
	"path/filepath"
	"testing"
)

func TestMySQLPhysicalSupportedVersionFamily(t *testing.T) {
	cases := []struct {
		name        string
		server      string
		expected    string
		expectError bool
	}{
		{name: "mysql57", server: "5.7.44", expected: "5.7"},
		{name: "mysql80", server: "8.0.43", expected: "8.0"},
		{name: "mysql84", server: "8.4.3", expected: "8.4"},
		{name: "unsupported81", server: "8.1.0", expectError: true},
		{name: "mariadb", server: "10.6.18-MariaDB", expectError: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := mysqlPhysicalSupportedVersionFamily(tc.server)
			if tc.expectError {
				if err == nil {
					t.Fatalf("expected error for %s", tc.server)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.expected {
				t.Fatalf("expected %s, got %s", tc.expected, got)
			}
		})
	}
}

func TestSelectBundledMySQLXtraBackupBundlePrefersProfileManifest(t *testing.T) {
	root := t.TempDir()
	t.Setenv("GOINSIGHT_BACKUP_TOOL_HOME", root)

	genericDir := filepath.Join(root, "mysql", "8.0")
	profileDir := filepath.Join(root, "mysql", "8.0", "linux-x86_64-glibc")
	mustWriteExecutable(t, filepath.Join(genericDir, "bin", "xtrabackup"))
	mustWriteExecutable(t, filepath.Join(profileDir, "bin", "xtrabackup"))
	mustWriteFile(t, filepath.Join(profileDir, "manifest.json"), `{"target_os":"linux","target_arch":"x86_64","libc":"glibc","tools":["xtrabackup"]}`)

	bundle, err := selectBundledMySQLXtraBackupBundle("8.0.43", remoteRuntimeProfile{
		OS:   "linux",
		Arch: "x86_64",
		Libc: "glibc",
	}, []string{"xtrabackup"})
	if err != nil {
		t.Fatalf("select bundle failed: %v", err)
	}
	if bundle.RootDir != profileDir {
		t.Fatalf("expected profile bundle %s, got %s", profileDir, bundle.RootDir)
	}
}

func TestSelectBundledMySQLXtraBackupBundleFallsBackToGeneric(t *testing.T) {
	root := t.TempDir()
	t.Setenv("GOINSIGHT_BACKUP_TOOL_HOME", root)

	genericDir := filepath.Join(root, "mysql", "8.4")
	mustWriteExecutable(t, filepath.Join(genericDir, "bin", "xtrabackup"))

	bundle, err := selectBundledMySQLXtraBackupBundle("8.4.2", remoteRuntimeProfile{
		OS:   "linux",
		Arch: "arm64",
		Libc: "glibc",
	}, []string{"xtrabackup"})
	if err != nil {
		t.Fatalf("select generic bundle failed: %v", err)
	}
	if bundle.RootDir != genericDir {
		t.Fatalf("expected generic bundle %s, got %s", genericDir, bundle.RootDir)
	}
}

func TestExtractXtraBackupVersionLine(t *testing.T) {
	raw := "2026-09-13T18:03:40.065301+08:00 0 [Note] [MY-011825] [Xtrabackup] recognized server arguments: --datadir=/data/app/mysql8.0/data\nxtrabackup version 8.0.35-30 based on MySQL server 8.0.35 Linux (x86_64) (revision id: 6beb4b49)"
	got := extractXtraBackupVersionLine(raw)
	want := "xtrabackup version 8.0.35-30 based on MySQL server 8.0.35 Linux (x86_64) (revision id: 6beb4b49)"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestFinalizeRemoteMySQLPhysicalToolResolution(t *testing.T) {
	raw := "TOOL_USER=mysql\nTOOL_PATH=/usr/local/goinsight/tools/mysql/8.0/bin/xtrabackup\nVERSION_LINE=2026-09-13T18:03:40.065301+08:00 0 [Note] [MY-011825] [Xtrabackup] recognized server arguments: --datadir=/data/app/mysql8.0/data\nxtrabackup version 8.0.35-30 based on MySQL server 8.0.35 Linux (x86_64) (revision id: 6beb4b49)\nTOOL_ERROR="
	spec, toolUser, err := finalizeRemoteMySQLPhysicalToolResolution("8.0.43", mysqlPhysicalDriverConfig{}, raw, nil)
	if err != nil {
		t.Fatalf("expected resolution to pass, got %v", err)
	}
	if toolUser != "mysql" {
		t.Fatalf("unexpected tool user: %q", toolUser)
	}
	if spec == nil || spec.Path != "/usr/local/goinsight/tools/mysql/8.0/bin/xtrabackup" {
		t.Fatalf("unexpected spec: %#v", spec)
	}
	if spec.Version != "xtrabackup version 8.0.35-30 based on MySQL server 8.0.35 Linux (x86_64) (revision id: 6beb4b49)" {
		t.Fatalf("unexpected version line: %q", spec.Version)
	}
}

func TestValidateMySQLPhysicalToolCompatibilityAcceptsVerboseVersionOutput(t *testing.T) {
	spec := &localToolSpec{
		Path: "/usr/local/goinsight/tools/mysql/8.0/bin/xtrabackup",
		Version: "2026-09-13T18:03:40.065301+08:00 0 [Note] [MY-011825] [Xtrabackup] recognized server arguments: --datadir=/data/app/mysql8.0/data\n" +
			"xtrabackup version 8.0.35-30 based on MySQL server 8.0.35 Linux (x86_64) (revision id: 6beb4b49)",
	}
	if err := validateMySQLPhysicalToolCompatibility(spec, "8.0.43"); err != nil {
		t.Fatalf("expected compatibility check to pass, got %v", err)
	}
}
