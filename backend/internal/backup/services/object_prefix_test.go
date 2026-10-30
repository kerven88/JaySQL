package services

import "testing"

func TestObjectPrefixPaths(t *testing.T) {
	for _, prefix := range []string{"", "backups/mysql", "/backups/mysql/", "中文/备份"} {
		if err := validateObjectStoragePrefix(prefix); err != nil {
			t.Fatalf("%q: %v", prefix, err)
		}
	}
	for _, prefix := range []string{"../backups", "a/../b", "a/./b", "s3://bucket/path", "a\\b", "a\nb"} {
		if validateObjectStoragePrefix(prefix) == nil {
			t.Fatalf("accepted %q", prefix)
		}
	}
	spec := BackupRunDirSpec{ObjectPrefix: "/backups/mysql/", InstanceBizID: "ds-example", TaskID: "task-example", RunID: 123, RunSeq: 1}
	if got := spec.RelPath("backup.sql"); got != "backups/mysql/ds-example/task-example/000001-123/backup.sql" {
		t.Fatal(got)
	}
	spec.ObjectPrefix = ""
	if got := spec.RelPath("backup.sql"); got != "ds-example/task-example/000001-123/backup.sql" {
		t.Fatal(got)
	}
}
