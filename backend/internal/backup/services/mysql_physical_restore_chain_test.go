package services

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ============================================================================
// MySQL XtraBackup 物理恢复链路：单元测试
// ============================================================================

// writeChainTestLevel 在测试目录下构造一层备份（xbstream + backup.meta.json）。
func writeChainTestLevel(t *testing.T, root string, dir string, manifest mysqlPhysicalBackupManifest, size int) string {
	t.Helper()
	levelDir := filepath.Join(root, dir)
	if err := os.MkdirAll(levelDir, 0o755); err != nil {
		t.Fatalf("创建测试目录失败: %v", err)
	}
	artifactPath := filepath.Join(levelDir, "backup.xbstream")
	if err := os.WriteFile(artifactPath, make([]byte, size), 0o644); err != nil {
		t.Fatalf("写入测试产物失败: %v", err)
	}
	if strings.TrimSpace(manifest.StorageArtifactPath) == "" {
		manifest.StorageArtifactPath = artifactPath
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("序列化测试元数据失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(levelDir, "backup.meta.json"), raw, 0o644); err != nil {
		t.Fatalf("写入测试元数据失败: %v", err)
	}
	return artifactPath
}

// localPathResolver 返回一个把 ref 直接当作本地路径的解析器，用于脱离数据库测试链路回溯。
func localPathResolver() mysqlPhysicalChainPathResolver {
	return func(ref string) (string, error) {
		text := strings.TrimSpace(ref)
		if text == "" {
			return "", fmt.Errorf("备份文件路径为空")
		}
		if _, err := os.Stat(text); err != nil {
			return "", fmt.Errorf("备份文件不存在或已被清理: %s", text)
		}
		return text, nil
	}
}

func TestResolveMySQLPhysicalRestoreChain_FullOnly(t *testing.T) {
	root := t.TempDir()
	full := writeChainTestLevel(t, root, "01-full", mysqlPhysicalBackupManifest{
		RunID: 101, BackupType: "FULL", FromLSN: "0", ToLSN: "1000", ArtifactSHA256: "sha-full",
	}, 128)

	chain, err := resolveMySQLPhysicalRestoreChainWith(full, localPathResolver())
	if err != nil {
		t.Fatalf("全量备份集不应报错: %v", err)
	}
	if len(chain) != 1 {
		t.Fatalf("全量备份集链路应为 1 层，实际 %d 层", len(chain))
	}
	if chain[0].Order != 1 || chain[0].BackupType != "FULL" || chain[0].RunID != 101 {
		t.Fatalf("链路节点信息不正确: %+v", chain[0])
	}
	if chain[0].SizeBytes != 128 {
		t.Fatalf("链路节点大小应为 128，实际 %d", chain[0].SizeBytes)
	}
}

func TestResolveMySQLPhysicalRestoreChain_IncrementalChainOrder(t *testing.T) {
	root := t.TempDir()
	fullPath := writeChainTestLevel(t, root, "01-full", mysqlPhysicalBackupManifest{
		RunID: 101, BackupType: "FULL", FromLSN: "0", ToLSN: "1000",
	}, 64)
	incr1Path := writeChainTestLevel(t, root, "02-incr", mysqlPhysicalBackupManifest{
		RunID: 102, BackupType: "INCR", FromLSN: "1000", ToLSN: "2000", BaseStorageArtifact: fullPath,
	}, 32)
	incr2Path := writeChainTestLevel(t, root, "03-incr", mysqlPhysicalBackupManifest{
		RunID: 103, BackupType: "INCR", FromLSN: "2000", ToLSN: "3000", BaseStorageArtifact: incr1Path,
	}, 16)

	chain, err := resolveMySQLPhysicalRestoreChainWith(incr2Path, localPathResolver())
	if err != nil {
		t.Fatalf("合法增量链路不应报错: %v", err)
	}
	if len(chain) != 3 {
		t.Fatalf("链路应为 3 层，实际 %d 层", len(chain))
	}
	wantTypes := []string{"FULL", "INCR", "INCR"}
	wantRuns := []uint64{101, 102, 103}
	for i := range chain {
		if chain[i].Order != i+1 {
			t.Fatalf("第 %d 个节点的 Order 应为 %d，实际 %d", i, i+1, chain[i].Order)
		}
		if chain[i].BackupType != wantTypes[i] {
			t.Fatalf("第 %d 层类型应为 %s，实际 %s", i, wantTypes[i], chain[i].BackupType)
		}
		if chain[i].RunID != wantRuns[i] {
			t.Fatalf("第 %d 层 run 应为 %d，实际 %d", i, wantRuns[i], chain[i].RunID)
		}
	}
	if total := mysqlPhysicalRestoreChainTotalBytes(chain); total != 112 {
		t.Fatalf("链路总大小应为 112，实际 %d", total)
	}
	if incrCount := mysqlPhysicalRestoreChainIncrCount(chain); incrCount != 2 {
		t.Fatalf("增量层数应为 2，实际 %d", incrCount)
	}
}

func TestResolveMySQLPhysicalRestoreChain_MissingBaseRef(t *testing.T) {
	root := t.TempDir()
	incrPath := writeChainTestLevel(t, root, "01-incr", mysqlPhysicalBackupManifest{
		RunID: 201, BackupType: "INCR", FromLSN: "100", ToLSN: "200", // 缺少 BaseStorageArtifact
	}, 8)

	_, err := resolveMySQLPhysicalRestoreChainWith(incrPath, localPathResolver())
	if err == nil {
		t.Fatal("缺少基线引用的增量备份必须报错")
	}
	if !strings.Contains(err.Error(), "缺少基线引用") {
		t.Fatalf("错误信息应说明缺少基线引用，实际: %v", err)
	}
}

func TestResolveMySQLPhysicalRestoreChain_BaseArtifactMissing(t *testing.T) {
	root := t.TempDir()
	incrPath := writeChainTestLevel(t, root, "01-incr", mysqlPhysicalBackupManifest{
		RunID: 202, BackupType: "INCR", FromLSN: "100", ToLSN: "200",
		BaseStorageArtifact: filepath.Join(root, "gone", "backup.xbstream"),
	}, 8)

	_, err := resolveMySQLPhysicalRestoreChainWith(incrPath, localPathResolver())
	if err == nil {
		t.Fatal("基线文件已被清理时必须报错")
	}
	if !strings.Contains(err.Error(), "回溯增量所依赖的上一级备份失败") {
		t.Fatalf("错误信息应指出基线回溯失败，实际: %v", err)
	}
}

func TestResolveMySQLPhysicalRestoreChain_BrokenLSN(t *testing.T) {
	root := t.TempDir()
	fullPath := writeChainTestLevel(t, root, "01-full", mysqlPhysicalBackupManifest{
		RunID: 301, BackupType: "FULL", FromLSN: "0", ToLSN: "1000",
	}, 8)
	// 增量起始 LSN 与基线结束 LSN 不衔接
	incrPath := writeChainTestLevel(t, root, "02-incr", mysqlPhysicalBackupManifest{
		RunID: 302, BackupType: "INCR", FromLSN: "1500", ToLSN: "2000", BaseStorageArtifact: fullPath,
	}, 8)

	_, err := resolveMySQLPhysicalRestoreChainWith(incrPath, localPathResolver())
	if err == nil {
		t.Fatal("LSN 不衔接的链路必须报错")
	}
	if !strings.Contains(err.Error(), "LSN 不连续") {
		t.Fatalf("错误信息应指出 LSN 不连续，实际: %v", err)
	}
}

func TestResolveMySQLPhysicalRestoreChain_Cycle(t *testing.T) {
	root := t.TempDir()
	pathA := filepath.Join(root, "a", "backup.xbstream")
	pathB := filepath.Join(root, "b", "backup.xbstream")
	writeChainTestLevel(t, root, "a", mysqlPhysicalBackupManifest{
		RunID: 401, BackupType: "INCR", FromLSN: "100", ToLSN: "200", BaseStorageArtifact: pathB,
	}, 8)
	writeChainTestLevel(t, root, "b", mysqlPhysicalBackupManifest{
		RunID: 402, BackupType: "INCR", FromLSN: "200", ToLSN: "300", BaseStorageArtifact: pathA,
	}, 8)

	_, err := resolveMySQLPhysicalRestoreChainWith(pathA, localPathResolver())
	if err == nil {
		t.Fatal("成环的链路必须报错")
	}
	if !strings.Contains(err.Error(), "成环") {
		t.Fatalf("错误信息应指出成环，实际: %v", err)
	}
}

func TestResolveMySQLPhysicalRestoreChain_NonFullBottom(t *testing.T) {
	root := t.TempDir()
	// 底层既不是 FULL 也不是 INCR（例如差异备份），应被校验拦截
	diffPath := writeChainTestLevel(t, root, "01-diff", mysqlPhysicalBackupManifest{
		RunID: 501, BackupType: "DIFF", FromLSN: "0", ToLSN: "500",
	}, 8)

	_, err := resolveMySQLPhysicalRestoreChainWith(diffPath, localPathResolver())
	if err == nil {
		t.Fatal("底层不是全量的链路必须报错")
	}
	if !strings.Contains(err.Error(), "缺少全量基线") {
		t.Fatalf("错误信息应说明缺少全量基线，实际: %v", err)
	}
}

func TestResolveMySQLPhysicalRestoreChain_EntryArtifactMissing(t *testing.T) {
	root := t.TempDir()
	_, err := resolveMySQLPhysicalRestoreChainWith(filepath.Join(root, "nope", "backup.xbstream"), localPathResolver())
	if err == nil {
		t.Fatal("目标备份文件不存在时必须报错")
	}
}

func TestResolveMySQLPhysicalRestoreChain_NilResolver(t *testing.T) {
	if _, err := resolveMySQLPhysicalRestoreChainWith("/tmp/x", nil); err == nil {
		t.Fatal("解析器为空时必须报错而不是 panic")
	}
}

func TestValidateMySQLPhysicalRestoreChain_Cases(t *testing.T) {
	cases := []struct {
		name    string
		chain   []mysqlPhysicalRestoreChainNode
		wantErr bool
	}{
		{
			name:    "空链路",
			chain:   nil,
			wantErr: true,
		},
		{
			name:    "单层全量",
			chain:   []mysqlPhysicalRestoreChainNode{{Order: 1, BackupType: "FULL", ToLSN: "100"}},
			wantErr: false,
		},
		{
			name: "LSN 缺失时不做拦截",
			chain: []mysqlPhysicalRestoreChainNode{
				{Order: 1, BackupType: "FULL"},
				{Order: 2, BackupType: "INCR"},
			},
			wantErr: false,
		},
		{
			name: "LSN 衔接正确",
			chain: []mysqlPhysicalRestoreChainNode{
				{Order: 1, BackupType: "FULL", ToLSN: "500"},
				{Order: 2, BackupType: "INCR", FromLSN: "500", ToLSN: "900"},
			},
			wantErr: false,
		},
		{
			name: "链路最底层是增量",
			chain: []mysqlPhysicalRestoreChainNode{
				{Order: 1, BackupType: "INCR", ToLSN: "500"},
			},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateMySQLPhysicalRestoreChain(tc.chain)
			if tc.wantErr && err == nil {
				t.Fatal("期望报错，实际通过")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("期望通过，实际报错: %v", err)
			}
		})
	}
}

func TestMySQLPhysicalLSNEqual(t *testing.T) {
	cases := []struct {
		a    string
		b    string
		want bool
	}{
		{"1000", "1000", true},
		{"1000", "1001", false},
		{" 1000 ", "1000", true},
		{"", "1000", true},
		// 超过 int64 安全区间的大数仍需正确比较
		{"18446744073709551615", "18446744073709551615", true},
		{"18446744073709551615", "18446744073709551614", false},
		{"0/16B3748", "0/16B3748", true},
	}
	for _, tc := range cases {
		if got := mysqlPhysicalLSNEqual(tc.a, tc.b); got != tc.want {
			t.Fatalf("mysqlPhysicalLSNEqual(%q, %q) = %v，期望 %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestResolveMySQLPhysicalRestoreChainForExecution_Fallback(t *testing.T) {
	resolved := mysqlPhysicalRestoreResolved{
		WorkDir:         "/work/dir",
		BackupSizeBytes: 2048,
		BackupSha256:    "sha-x",
	}
	chain := resolveMySQLPhysicalRestoreChainForExecution(resolved, "/storage/backup.xbstream")
	if len(chain) != 1 {
		t.Fatalf("无链路信息时应退化为单层，实际 %d 层", len(chain))
	}
	if chain[0].BackupType != "FULL" || chain[0].LocalPath != "/storage/backup.xbstream" {
		t.Fatalf("退化节点信息不正确: %+v", chain[0])
	}
	if chain[0].RemotePath != "/work/dir/chain/01.xbstream" {
		t.Fatalf("远端落地路径不正确: %s", chain[0].RemotePath)
	}
	if chain[0].SizeBytes != 2048 || chain[0].Sha256 != "sha-x" {
		t.Fatalf("退化节点应继承 resolved 中的大小与校验和: %+v", chain[0])
	}
}

func TestResolveMySQLPhysicalRestoreChainForExecution_AssignsRemotePaths(t *testing.T) {
	resolved := mysqlPhysicalRestoreResolved{
		WorkDir: "/work/dir",
		Chain: []mysqlPhysicalRestoreChainNode{
			{Order: 1, BackupType: "FULL", LocalPath: "/s/full.xbstream", SizeBytes: 10},
			{Order: 2, BackupType: "INCR", LocalPath: "/s/incr.xbstream", SizeBytes: 5},
		},
	}
	chain := resolveMySQLPhysicalRestoreChainForExecution(resolved, "")
	if len(chain) != 2 {
		t.Fatalf("应保留 2 层，实际 %d 层", len(chain))
	}
	want := []string{"/work/dir/chain/01.xbstream", "/work/dir/chain/02.xbstream"}
	for i := range chain {
		if chain[i].RemotePath != want[i] {
			t.Fatalf("第 %d 层远端路径应为 %s，实际 %s", i+1, want[i], chain[i].RemotePath)
		}
	}
}

func TestBuildMySQLPhysicalRestoreExecuteScript_FullOnly(t *testing.T) {
	resolved := mysqlPhysicalRestoreResolved{
		WorkDir:        "/work/dir",
		XtraBackupPath: "/opt/xtrabackup",
		TargetDataDir:  "/data/mysql",
		TargetPort:     3306,
		MysqlOSUser:    "mysql",
	}
	chain := []mysqlPhysicalRestoreChainNode{{
		Order: 1, BackupType: "FULL", RemotePath: "/work/dir/chain/01.xbstream",
	}}
	script := buildMySQLPhysicalRestoreExecuteScript(resolved, chain, true)

	if strings.Contains(script, "--apply-log-only") {
		t.Fatal("纯全量恢复不应使用 --apply-log-only")
	}
	if !strings.Contains(script, "--prepare --target-dir='/work/dir/unpacked/01'") {
		t.Fatalf("全量恢复应直接对 01 目录做 prepare，实际脚本:\n%s", script)
	}
	if !strings.Contains(script, "--copy-back --target-dir='/work/dir/unpacked/01'") {
		t.Fatalf("应从基线目录 copy-back，实际脚本:\n%s", script)
	}
	if !strings.Contains(script, "STATUS=SUCCESS") {
		t.Fatal("脚本应包含成功标记")
	}
}

func TestBuildMySQLPhysicalRestoreExecuteScript_IncrementalChain(t *testing.T) {
	resolved := mysqlPhysicalRestoreResolved{
		WorkDir:        "/work/dir",
		XtraBackupPath: "/opt/xtrabackup",
		TargetDataDir:  "/data/mysql",
		TargetPort:     3306,
		MysqlOSUser:    "mysql",
	}
	chain := []mysqlPhysicalRestoreChainNode{
		{Order: 1, BackupType: "FULL", RemotePath: "/work/dir/chain/01.xbstream"},
		{Order: 2, BackupType: "INCR", RemotePath: "/work/dir/chain/02.xbstream"},
		{Order: 3, BackupType: "INCR", RemotePath: "/work/dir/chain/03.xbstream"},
	}
	script := buildMySQLPhysicalRestoreExecuteScript(resolved, chain, true)

	// 三层各自解流到独立目录
	for _, order := range []string{"01", "02", "03"} {
		want := fmt.Sprintf("xbstream -x -C '/work/dir/unpacked/%s' < '/work/dir/chain/%s.xbstream'", order, order)
		if !strings.Contains(script, want) {
			t.Fatalf("脚本缺少第 %s 层的解流命令，期望包含: %s\n实际脚本:\n%s", order, want, script)
		}
	}

	// 全量基线必须先做 apply-log-only
	if !strings.Contains(script, "--prepare --apply-log-only --target-dir='/work/dir/unpacked/01'") {
		t.Fatalf("全量基线应先执行 apply-log-only prepare，实际脚本:\n%s", script)
	}

	// 收集所有带 --incremental-dir 的行
	incrementalLines := make([]string, 0, 4)
	for _, line := range strings.Split(script, "\n") {
		if strings.Contains(line, "--incremental-dir=") {
			incrementalLines = append(incrementalLines, line)
		}
	}
	if len(incrementalLines) != 2 {
		t.Fatalf("三层链路应有 2 条增量合并命令，实际 %d 条", len(incrementalLines))
	}
	if !strings.Contains(incrementalLines[0], "--apply-log-only") ||
		!strings.Contains(incrementalLines[0], "--incremental-dir='/work/dir/unpacked/02'") {
		t.Fatalf("中间层应使用 apply-log-only 合并 02，实际: %s", incrementalLines[0])
	}
	if strings.Contains(incrementalLines[1], "--apply-log-only") {
		t.Fatalf("末层不应再带 apply-log-only（需做 undo 回滚），实际: %s", incrementalLines[1])
	}
	if !strings.Contains(incrementalLines[1], "--incremental-dir='/work/dir/unpacked/03'") {
		t.Fatalf("末层应合并 03，实际: %s", incrementalLines[1])
	}

	// copy-back 必须从基线目录
	if !strings.Contains(script, "--copy-back --target-dir='/work/dir/unpacked/01'") {
		t.Fatalf("应从基线目录 copy-back，实际脚本:\n%s", script)
	}
}

func TestBuildMySQLPhysicalRestoreExecuteScript_EmptyChainFallback(t *testing.T) {
	resolved := mysqlPhysicalRestoreResolved{
		WorkDir:          "/work/dir",
		XtraBackupPath:   "/opt/xtrabackup",
		RemoteBackupFile: "/work/dir/backup.xbstream",
		TargetDataDir:    "/data/mysql",
		MysqlOSUser:      "mysql",
	}
	script := buildMySQLPhysicalRestoreExecuteScript(resolved, nil, false)
	if !strings.Contains(script, "--prepare --target-dir='/work/dir/unpacked/01'") {
		t.Fatalf("空链路应退化为单层恢复，实际脚本:\n%s", script)
	}
	if !strings.Contains(script, "SKIP_AUTO_START") {
		t.Fatal("autoStart=false 时应提示跳过自动启动")
	}
}

func TestSummarizeMySQLPhysicalRestoreChain(t *testing.T) {
	full := []mysqlPhysicalRestoreChainNode{{Order: 1, BackupType: "FULL", SizeBytes: 1024}}
	if got := summarizeMySQLPhysicalRestoreChain(full); !strings.Contains(got, "直接恢复 1 个全量备份") {
		t.Fatalf("全量链路摘要不正确: %s", got)
	}
	mixed := []mysqlPhysicalRestoreChainNode{
		{Order: 1, BackupType: "FULL", SizeBytes: 1024},
		{Order: 2, BackupType: "INCR", SizeBytes: 1024},
	}
	if got := summarizeMySQLPhysicalRestoreChain(mixed); !strings.Contains(got, "1 个全量 + 1 级增量") {
		t.Fatalf("增量链路摘要不正确: %s", got)
	}
}

// TestBuildMySQLPhysicalRestoreExecuteScript_ShellSyntax 用 sh -n 校验生成的脚本语法，
// 避免路径中的特殊字符或拼接错误在远端才暴露。
func TestBuildMySQLPhysicalRestoreExecuteScript_ShellSyntax(t *testing.T) {
	cases := []struct {
		name  string
		chain []mysqlPhysicalRestoreChainNode
	}{
		{
			name:  "单层全量",
			chain: []mysqlPhysicalRestoreChainNode{{Order: 1, BackupType: "FULL", RemotePath: "/work/dir/chain/01.xbstream"}},
		},
		{
			name: "三层增量链",
			chain: []mysqlPhysicalRestoreChainNode{
				{Order: 1, BackupType: "FULL", RemotePath: "/work/dir/chain/01.xbstream"},
				{Order: 2, BackupType: "INCR", RemotePath: "/work/dir/chain/02.xbstream"},
				{Order: 3, BackupType: "INCR", RemotePath: "/work/dir/chain/03.xbstream"},
			},
		},
		{
			// 路径含空格与单引号，验证 quirk 转义不会破坏语法
			name: "特殊字符路径",
			chain: []mysqlPhysicalRestoreChainNode{
				{Order: 1, BackupType: "FULL", RemotePath: "/work/dir with space/chain/01.xbstream"},
				{Order: 2, BackupType: "INCR", RemotePath: "/work/it's/chain/02.xbstream"},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resolved := mysqlPhysicalRestoreResolved{
				WorkDir:        "/work/dir",
				XtraBackupPath: "/opt/xtrabackup",
				TargetDataDir:  "/data/mysql",
				TargetPort:     3306,
				MysqlOSUser:    "mysql",
			}
			script := buildMySQLPhysicalRestoreExecuteScript(resolved, tc.chain, true)
			scriptPath := filepath.Join(t.TempDir(), "restore.sh")
			if err := os.WriteFile(scriptPath, []byte(script), 0o644); err != nil {
				t.Fatalf("写入脚本失败: %v", err)
			}
			cmd := exec.Command("/bin/sh", "-n", scriptPath)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("脚本语法校验失败: %v\n输出: %s\n脚本:\n%s", err, string(out), script)
			}
		})
	}
}
