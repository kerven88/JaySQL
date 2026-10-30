package main

import "testing"

func TestObjectStorageRunPrefix(t *testing.T) {
	job := &agentJob{InstanceBizID: "ds-example", TaskID: "task-example", RunID: 123, RunSeq: 1, StorageTarget: &agentStorageTarget{BasePath: "/backups/mysql/"}}
	spec := objectStorageRunDirSpec(job)
	for _, file := range []string{"backup.sql", "run.log", "metadata.json", "checksums.sha256"} {
		if got := spec.RelPath(file); got != "backups/mysql/ds-example/task-example/000001-123/"+file {
			t.Fatal(got)
		}
	}
	job.StorageTarget.BasePath = ""
	if got := objectStorageRunDirSpec(job).RelPath("backup.sql"); got != "ds-example/task-example/000001-123/backup.sql" {
		t.Fatal(got)
	}
}
