package services

import "testing"

// 备份集存储目标目录删除护栏：必须绝对路径、必须包含 <instance>/<task>/<runID> 三段、
// 且末段为合法批次目录名（纯 runID 或「序号-runID」），避免误删 base_path 或更高层级目录。
func TestIsSafeBackupSetRemovalPath(t *testing.T) {
	cases := []struct {
		name   string
		full   string
		rel    string
		expect bool
	}{
		{"正常批次目录", "/backup/31/bk-45rw2lhqtetz/394", "31/bk-45rw2lhqtetz/394", true},
		{"另一实例批次目录", "/backup/35/bk-o43dfcpfqw3q/391", "35/bk-o43dfcpfqw3q/391", true},
		{"新规则批次目录(序号-runID)", "/backup/ds-9x2k7m4p1q8w/bk-45rw2lhqtetz/000001-399", "ds-9x2k7m4p1q8w/bk-45rw2lhqtetz/000001-399", true},
		{"不得删除 base_path 本身", "/backup/", "31/bk-45rw2lhqtetz/394", false},
		{"末段非数字", "/backup/31/bk-45rw2lhqtetz", "31/bk-45rw2lhqtetz", false},
		{"rel 不在 full 尾部", "/backup/x/y", "31/bk-45rw2lhqtetz/394", false},
		{"非绝对路径", "backup/31/bk-45rw2lhqtetz/394", "31/bk-45rw2lhqtetz/394", false},
		{"根目录", "/", "31/bk-45rw2lhqtetz/394", false},
		{"层级不足", "/backup/31/394", "31/394", false},
		{"目录下子文件不参与目录删除", "/backup/31/t/394/all.sql", "31/t/394/all.sql", false},
		{"含 .. 的相对段被拒绝", "/backup/31/t/394", "../31/t/394", false},
		{"末段非数字后缀被拒绝", "/backup/ds-x/bk-y/000001-abc", "ds-x/bk-y/000001-abc", false},
		{"末段多连字符被拒绝", "/backup/a/b/1-2-3", "a/b/1-2-3", false},
		{"末段含点被拒绝", "/backup/a/b/39.4", "a/b/39.4", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isSafeBackupSetRemovalPath(c.full, c.rel); got != c.expect {
				t.Fatalf("isSafeBackupSetRemovalPath(%q, %q) = %v, want %v", c.full, c.rel, got, c.expect)
			}
		})
	}
}

// 执行记录保留期清理只处理已收口状态，进行中的记录必须保留。
func TestIsBackupRunRetentionEligible(t *testing.T) {
	cases := map[string]bool{
		"SUCCESS":        true,
		"FAILED":         true,
		"STOPPED":        true,
		"CANCELLED":      true,
		"PENDING":        false,
		"RUNNING":        false,
		"STOP_REQUESTED": false,
		"PENDING_PREPARE": false,
		" running ":      false,
	}
	for status, expect := range cases {
		if got := isBackupRunRetentionEligible(status); got != expect {
			t.Fatalf("isBackupRunRetentionEligible(%q) = %v, want %v", status, got, expect)
		}
	}
}

func TestSanitizePathSegmentForBackupSetDir(t *testing.T) {
	if got := sanitizePathSegment("bk-45rw2lhqtetz", "task"); got != "bk-45rw2lhqtetz" {
		t.Fatalf("普通 taskID 应原样保留，got %q", got)
	}
	if got := sanitizePathSegment("a/b", "task"); got != "a_b" {
		t.Fatalf("斜杠应被替换为下划线，got %q", got)
	}
	if got := sanitizePathSegment("../etc", "task"); got == "../etc" {
		t.Fatalf("上跳路径应被清洗，got %q", got)
	}
}
