package services

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	backupModels "github.com/simajiong/jaysql/internal/backup/models"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
)

type memStreamSink struct {
	buf bytes.Buffer
}

func (m *memStreamSink) Write(p []byte) (int, error) { return m.buf.Write(p) }
func (m *memStreamSink) Close() error                { return nil }
func (m *memStreamSink) RemoteTarget() string        { return "mem://sink" }

func sha256Hex(t *testing.T, data []byte) string {
	t.Helper()
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// TestBackupArtifactStreamSha256MatchesWrittenBytes 保证零落盘直传模式下也能拿到产物摘要：
// 流累计的 sha256 必须等于最终写进 sink 的字节的摘要（含 zip/gzip 封装尾部）。
func TestBackupArtifactStreamSha256MatchesWrittenBytes(t *testing.T) {
	for _, mode := range []string{"", ArtifactCompressionGzip, ArtifactCompressionZip} {
		sink := &memStreamSink{}
		stream := newBackupArtifactStream(sink, mode, "")
		writer, err := stream.WriterForEntry("all_databases.sql")
		if err != nil {
			t.Fatalf("compression=%q 获取写入器失败: %v", mode, err)
		}
		if _, err := writer.Write([]byte("hello goinsight backup\n")); err != nil {
			t.Fatalf("compression=%q 写入失败: %v", mode, err)
		}
		if err := stream.Close(); err != nil {
			t.Fatalf("compression=%q 关闭失败: %v", mode, err)
		}
		want := sha256Hex(t, sink.buf.Bytes())
		if got := stream.Sha256(); got != want {
			t.Fatalf("compression=%q 流累计摘要与最终产物不一致: got=%s want=%s", mode, got, want)
		}
		if stream.SizeBytes() != int64(sink.buf.Len()) {
			t.Fatalf("compression=%q 字节计数不一致: got=%d want=%d", mode, stream.SizeBytes(), sink.buf.Len())
		}
	}
}

// TestWriteBackupSetSidecarFiles 校验附属文件的产出与格式。
func TestWriteBackupSetSidecarFiles(t *testing.T) {
	dir := t.TempDir()
	artifact := filepath.Join(dir, "all_databases.sql")
	if err := os.WriteFile(artifact, []byte("CREATE TABLE t1(id int);\n"), 0o644); err != nil {
		t.Fatalf("准备产物失败: %v", err)
	}
	sha, size, err := fileSha256(artifact)
	if err != nil {
		t.Fatalf("计算产物摘要失败: %v", err)
	}
	input := BackupExecutionInput{
		RunID:            7,
		TaskID:           "bk-abcdef",
		SourceInstanceID: 13,
		DBType:           "MySQL",
		DriverType:       "LOGICAL",
		SourceDBName:     "appdb",
	}
	meta := buildBackupSetMetadata(input, "all_databases.sql", size, sha, BackupTypeLogical, "mysqldump", "8.0.43")
	if err := writeBackupSetSidecarFiles(dir, meta, []backupChecksumEntry{{Name: "all_databases.sql", Sha256: sha}}); err != nil {
		t.Fatalf("写附属文件失败: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "metadata.json"))
	if err != nil {
		t.Fatalf("metadata.json 未产出: %v", err)
	}
	for _, want := range []string{`"backup_type": "logical"`, `"source_db_type": "MySQL"`, `"artifact_sha256": "` + sha} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("metadata.json 缺少 %s，实际内容=%s", want, string(raw))
		}
	}
	ck, err := os.ReadFile(filepath.Join(dir, "checksums.sha256"))
	if err != nil {
		t.Fatalf("checksums.sha256 未产出: %v", err)
	}
	if !strings.HasPrefix(string(ck), sha+"  all_databases.sql") {
		t.Fatalf("checksums.sha256 格式不符: %q", string(ck))
	}
}

// TestWriteBackupSetSidecarFilesSkipsEmptyChecksums 摘要为空时不产出空文件，
// 避免恢复侧出现「有校验和文件但匹配不到条目」的假象。
func TestWriteBackupSetSidecarFilesSkipsEmptyChecksums(t *testing.T) {
	dir := t.TempDir()
	meta := buildBackupSetMetadata(BackupExecutionInput{DBType: "MySQL"}, "x.sql", 0, "", BackupTypeLogical, "mysqldump", "")
	if err := writeBackupSetSidecarFiles(dir, meta, []backupChecksumEntry{{Name: "x.sql", Sha256: ""}}); err != nil {
		t.Fatalf("写附属文件失败: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "checksums.sha256")); !os.IsNotExist(err) {
		t.Fatalf("摘要为空时不应产出 checksums.sha256: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "metadata.json")); err != nil {
		t.Fatalf("metadata.json 仍应产出: %v", err)
	}
}

// TestRestorePrecheckPassesWithBackupSidecarFiles 是本次要补的闭环：
// 备份侧产出 → 恢复侧探测 → 两项校验均通过。
func TestRestorePrecheckPassesWithBackupSidecarFiles(t *testing.T) {
	dir := t.TempDir()
	artifact := filepath.Join(dir, "all_databases.sql")
	if err := os.WriteFile(artifact, []byte("CREATE TABLE t1(id int);\n"), 0o644); err != nil {
		t.Fatalf("准备产物失败: %v", err)
	}
	sha, size, err := fileSha256(artifact)
	if err != nil {
		t.Fatalf("计算产物摘要失败: %v", err)
	}
	input := BackupExecutionInput{RunID: 7, TaskID: "bk-abcdef", SourceInstanceID: 13, DBType: "MySQL", DriverType: "LOGICAL", SourceDBName: "appdb"}
	meta := buildBackupSetMetadata(input, "all_databases.sql", size, sha, BackupTypeLogical, "mysqldump", "8.0.43")
	if err := writeBackupSetSidecarFiles(dir, meta, []backupChecksumEntry{{Name: "all_databases.sql", Sha256: sha}}); err != nil {
		t.Fatalf("写附属文件失败: %v", err)
	}

	set := &backupModels.InsightBackupSets{
		Model:     &commonModels.Model{},
		DBType:    "MySQL",
		OutputDir: dir,
		FileName:  "all_databases.sql",
		FilePath:  artifact,
		Sha256:    sha,
		SizeBytes: size,
	}
	mdPath, ckPath := detectRestoreMetadataFiles(set, artifact)
	if mdPath == "" {
		t.Fatal("未探测到 metadata.json")
	}
	if ckPath == "" {
		t.Fatal("未探测到 checksums.sha256")
	}
	mdOK, _, mdErr := validateRestoreMetadata(set, mdPath)
	if mdErr != nil || !mdOK {
		t.Fatalf("元数据校验应通过: ok=%v err=%v", mdOK, mdErr)
	}
	ckOK, _, ckErr := validateRestoreChecksum(set, artifact, ckPath)
	if ckErr != nil || !ckOK {
		t.Fatalf("校验和校验应通过: ok=%v err=%v", ckOK, ckErr)
	}
}

// TestValidateRestoreMetadataRejectsPhysicalAndMismatch 物理备份集误走逻辑恢复必须被拦住。
func TestValidateRestoreMetadataRejectsPhysicalAndMismatch(t *testing.T) {
	dir := t.TempDir()
	set := &backupModels.InsightBackupSets{Model: &commonModels.Model{}, DBType: "MySQL"}

	physical := filepath.Join(dir, "metadata.json")
	if err := os.WriteFile(physical, []byte(`{"backup_type":"physical","source_db_type":"MySQL"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := validateRestoreMetadata(set, physical); err == nil {
		t.Fatal("物理备份集的 metadata 必须阻断逻辑恢复")
	}

	mismatch := filepath.Join(dir, "mismatch.json")
	if err := os.WriteFile(mismatch, []byte(`{"backup_type":"logical","source_db_type":"PostgreSQL"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := validateRestoreMetadata(set, mismatch); err == nil {
		t.Fatal("source_db_type 与备份集类型不一致必须阻断")
	}
}

// TestValidateRestoreChecksumDetectsTampering 产物被篡改时两条比对路径都要拦住。
func TestValidateRestoreChecksumDetectsTampering(t *testing.T) {
	dir := t.TempDir()
	artifact := filepath.Join(dir, "all_databases.sql")
	if err := os.WriteFile(artifact, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	sha, _, err := fileSha256(artifact)
	if err != nil {
		t.Fatal(err)
	}
	set := &backupModels.InsightBackupSets{Model: &commonModels.Model{}, DBType: "MySQL", Sha256: sha}
	if err := os.WriteFile(artifact, []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := validateRestoreChecksum(set, artifact, ""); err == nil {
		t.Fatal("产物被篡改后，与平台登记值比对必须阻断")
	}
	ck := filepath.Join(dir, "checksums.sha256")
	if err := os.WriteFile(ck, []byte(sha+"  all_databases.sql\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := validateRestoreChecksum(set, artifact, ck); err == nil {
		t.Fatal("产物被篡改后，与 checksums 文件比对必须阻断")
	}
}

// TestRestorePrecheckNoFalsePass 堵住「找到了文件但其实没校验」的假通过。
func TestRestorePrecheckNoFalsePass(t *testing.T) {
	dir := t.TempDir()
	artifact := filepath.Join(dir, "all_databases.sql")
	if err := os.WriteFile(artifact, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	sha, _, err := fileSha256(artifact)
	if err != nil {
		t.Fatal(err)
	}
	set := &backupModels.InsightBackupSets{Model: &commonModels.Model{}, DBType: "MySQL", Sha256: sha}

	txt := filepath.Join(dir, "metadata.txt")
	if err := os.WriteFile(txt, []byte("free text metadata"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ok, _, err := validateRestoreMetadata(set, txt); err != nil || ok {
		t.Fatalf("metadata.txt 无法做字段校验，不应计为通过: ok=%v err=%v", ok, err)
	}

	empty := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(empty, []byte(`{"run_id":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if ok, _, err := validateRestoreMetadata(set, empty); err != nil || ok {
		t.Fatalf("缺少 backup_type/source_db_type 的 metadata 不应计为通过: ok=%v err=%v", ok, err)
	}

	ck := filepath.Join(dir, "checksums.sha256")
	if err := os.WriteFile(ck, []byte(sha+"  other_file.sql\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ok, _, err := validateRestoreChecksum(set, artifact, ck); err != nil || ok {
		t.Fatalf("校验和文件缺少本产物条目时不应计为通过: ok=%v err=%v", ok, err)
	}
}

// TestRestoreArtifactSearchDirs 候选目录去重且忽略空值。
func TestRestoreArtifactSearchDirs(t *testing.T) {
	dir := t.TempDir()
	artifact := filepath.Join(dir, "all_databases.sql")
	set := &backupModels.InsightBackupSets{Model: &commonModels.Model{}, OutputDir: dir}
	got := restoreArtifactSearchDirs(set, artifact)
	if len(got) != 1 || got[0] != dir {
		t.Fatalf("同目录时应去重为一个: %v", got)
	}
	set.OutputDir = filepath.Join(dir, "other")
	got = restoreArtifactSearchDirs(set, artifact)
	if len(got) != 2 {
		t.Fatalf("不同目录时应返回两个: %v", got)
	}
}

// TestWriteAndVerifyBackupSetSidecar 验证「写 + 立即回读校验」的四种结果：
// 正常写入两项都通过；摘要缺失时校验和不通过；元数据关键字段为空时元数据不通过。
// 这条测试守住「落库字段必须来自真实校验，而不是写动作本身」这一契约。
func TestWriteAndVerifyBackupSetSidecar(t *testing.T) {
	dir := t.TempDir()
	sha := "a1b2c3d4e5f6"
	meta := buildBackupSetMetadata(
		BackupExecutionInput{DBType: "MySQL"}, "all_databases.sql", 1024, sha,
		BackupTypeLogical, "mysqldump", "8.0.43",
	)

	mdOK, ckOK := writeAndVerifyBackupSetSidecar(dir, meta, []backupChecksumEntry{{Name: "all_databases.sql", Sha256: sha}})
	if !mdOK || !ckOK {
		t.Fatalf("正常写入后两项都应校验通过: metadata=%v checksum=%v", mdOK, ckOK)
	}
	if _, err := os.Stat(filepath.Join(dir, "metadata.json")); err != nil {
		t.Fatalf("metadata.json 应已落盘: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "checksums.sha256")); err != nil {
		t.Fatalf("checksums.sha256 应已落盘: %v", err)
	}

	// 没有可用摘要时不产出 checksums 文件，校验和必须判定为未通过（不能算"写成功"）。
	dir2 := t.TempDir()
	if _, ckOK := writeAndVerifyBackupSetSidecar(dir2, meta, nil); ckOK {
		t.Fatal("无摘要条目时 checksum 不应计为通过")
	}
	if _, err := os.Stat(filepath.Join(dir2, "checksums.sha256")); err == nil {
		t.Fatal("无摘要条目时不应产出空的 checksums.sha256")
	}

	// 元数据缺少 backup_type / source_db_type 时，必须判定为未通过，
	// 否则备份侧落库一个 metadata_verified=1 的备份集，恢复侧照样会被一票否决。
	dir3 := t.TempDir()
	empty := buildBackupSetMetadata(BackupExecutionInput{}, "x.sql", 0, sha, BackupTypeLogical, "mysqldump", "")
	empty.BackupType = ""
	if mdOK, _ := writeAndVerifyBackupSetSidecar(dir3, empty, []backupChecksumEntry{{Name: "x.sql", Sha256: sha}}); mdOK {
		t.Fatal("metadata 缺少 backup_type/source_db_type 时不应计为通过")
	}
}
