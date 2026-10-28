package services

import (
	"strings"
	"testing"
)

func TestBackupRunDirSpecLayouts(t *testing.T) {
	cases := []struct {
		name string
		spec BackupRunDirSpec
		want string
	}{
		{
			name: "新规则: 数据源业务ID + 序号-runID",
			spec: BackupRunDirSpec{
				SourceInstanceID: 35,
				InstanceBizID:    "ds-9x2k7m4p1q8w",
				TaskID:           "bk-jco9p1nchmjb",
				RunID:            396,
				RunSeq:           12,
			},
			want: "ds-9x2k7m4p1q8w/bk-jco9p1nchmjb/000012-396",
		},
		{
			name: "历史数据: 数字实例ID + 纯runID",
			spec: BackupRunDirSpec{
				SourceInstanceID: 35,
				TaskID:           "bk-jco9p1nchmjb",
				RunID:            396,
			},
			want: "35/bk-jco9p1nchmjb/396",
		},
		{
			name: "业务ID缺失降级: 实例段回落数字, run段仍带序号",
			spec: BackupRunDirSpec{
				SourceInstanceID: 35,
				TaskID:           "bk-jco9p1nchmjb",
				RunID:            396,
				RunSeq:           3,
			},
			want: "35/bk-jco9p1nchmjb/000003-396",
		},
		{
			name: "序号超过六位不截断",
			spec: BackupRunDirSpec{
				SourceInstanceID: 1,
				InstanceBizID:    "ds-aaaa",
				TaskID:           "bk-bbbb",
				RunID:            1000000,
				RunSeq:           1000000,
			},
			want: "ds-aaaa/bk-bbbb/1000000-1000000",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.spec.RelDir(); got != tc.want {
				t.Fatalf("RelDir() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBackupRunDirSpecSanitizesTaskID(t *testing.T) {
	spec := BackupRunDirSpec{
		SourceInstanceID: 35,
		InstanceBizID:    "ds-9x2k7m4p1q8w",
		TaskID:           "../../etc/passwd",
		RunID:            7,
		RunSeq:           1,
	}
	rel := spec.RelDir()
	if strings.Contains(rel, "..") || strings.Contains(rel, "/etc/") {
		t.Fatalf("RelDir() 未过滤路径穿越: %q", rel)
	}
	if strings.Count(rel, "/") != 2 {
		t.Fatalf("RelDir() 层级数异常: %q", rel)
	}
}

func TestBackupRunDirSpecRelPathAndAbsDir(t *testing.T) {
	spec := BackupRunDirSpec{
		SourceInstanceID: 35,
		InstanceBizID:    "ds-9x2k7m4p1q8w",
		TaskID:           "bk-jco9p1nchmjb",
		RunID:            396,
		RunSeq:           12,
	}
	if got, want := spec.RelPath("backup.zip"), "ds-9x2k7m4p1q8w/bk-jco9p1nchmjb/000012-396/backup.zip"; got != want {
		t.Fatalf("RelPath() = %q, want %q", got, want)
	}
	if got, want := spec.AbsDir("/backup"), "/backup/ds-9x2k7m4p1q8w/bk-jco9p1nchmjb/000012-396"; got != want {
		t.Fatalf("AbsDir() = %q, want %q", got, want)
	}
}

func TestBackupRunDirSpecTaskRootCandidates(t *testing.T) {
	spec := BackupRunDirSpec{
		SourceInstanceID: 35,
		InstanceBizID:    "ds-9x2k7m4p1q8w",
		TaskID:           "bk-jco9p1nchmjb",
		RunID:            396,
		RunSeq:           12,
	}
	roots := spec.TaskRootCandidates("/backup")
	want := []string{
		"/backup/ds-9x2k7m4p1q8w/bk-jco9p1nchmjb",
		"/backup/35/bk-jco9p1nchmjb",
	}
	if len(roots) != len(want) {
		t.Fatalf("TaskRootCandidates() 数量 = %d, want %d (%v)", len(roots), len(want), roots)
	}
	for i := range want {
		if roots[i] != want[i] {
			t.Fatalf("TaskRootCandidates()[%d] = %q, want %q", i, roots[i], want[i])
		}
	}

	legacyOnly := BackupRunDirSpec{SourceInstanceID: 35, TaskID: "bk-jco9p1nchmjb", RunID: 396}
	if got := legacyOnly.TaskRootCandidates("/backup"); len(got) != 1 || got[0] != "/backup/35/bk-jco9p1nchmjb" {
		t.Fatalf("历史数据 TaskRootCandidates() 应只有单根, got %v", got)
	}
}
