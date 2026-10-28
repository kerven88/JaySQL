package services

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type backupRunLogWriter interface {
	io.Writer
	Infof(format string, args ...any)
	Warnf(format string, args ...any)
	Errorf(format string, args ...any)
	Path() string
	Close() error
}

type backupRunLoggerLike = backupRunLogWriter

type teeBackupRunLogger struct {
	local backupRunLogWriter
	extra io.Writer
	path  string
}

func (t *teeBackupRunLogger) Infof(format string, args ...any) { t.local.Infof(format, args...); t.writeExtra("INFO", format, args...) }
func (t *teeBackupRunLogger) Warnf(format string, args ...any) { t.local.Warnf(format, args...); t.writeExtra("WARN", format, args...) }
func (t *teeBackupRunLogger) Errorf(format string, args ...any) { t.local.Errorf(format, args...); t.writeExtra("ERROR", format, args...) }
func (t *teeBackupRunLogger) Path() string                      { return t.path }
func (t *teeBackupRunLogger) Close() error {
	if wc, ok := t.extra.(io.WriteCloser); ok {
		_ = wc.Close()
	}
	return t.local.Close()
}

func (t *teeBackupRunLogger) Write(p []byte) (int, error) {
	n, err := t.local.Write(p)
	if t.extra != nil {
		_, _ = t.extra.Write(p[:n])
	}
	return n, err
}

func (t *teeBackupRunLogger) writeExtra(level string, format string, args ...any) {
	if t == nil || t.extra == nil {
		return
	}
	line := fmt.Sprintf(format, args...)
	if !strings.HasSuffix(line, "\n") {
		line += "\n"
	}
	_, _ = t.extra.Write([]byte(fmt.Sprintf("[%s] [%s] %s",
		time.Now().Format("2006-01-02 15:04:05"),
		strings.ToUpper(strings.TrimSpace(level)),
		line,
	)))
}

type backupRunLogger struct {
	file *os.File
	path string
}

// normalizeRunLogWritePath 把「待写日志路径」收敛成真实的本地绝对路径。
//
// 背景：insight_backup_runs.log_path 存的是存储目标引用（形如 st:2:///ds-xxx/bk-xxx/000045-487/run.log），
// 而不是本地路径。服务端在追加/打开日志前必须先解析，否则 filepath 会把 "st:2:///xxx" 规范化成
// "st:2:/xxx"，再配合 ensureDir + OpenFile，就会在进程 CWD 下建出名为 "st:2:" 的垃圾目录
// （历史线上已累积 75 个这样的 run.log 碎片）。
//
// 解析失败时返回 ok=false：宁可不写这一行日志，也绝不落到错误位置。
func normalizeRunLogWritePath(path string) (string, bool) {
	p := strings.TrimSpace(path)
	if p == "" {
		return "", false
	}
	if !strings.HasPrefix(p, "st:") {
		return p, true
	}
	real, ok := resolveStorageRefToLocal(p, 0)
	if !ok || strings.TrimSpace(real) == "" {
		return "", false
	}
	return real, true
}

func openBackupRunLogger(path string) (*backupRunLogger, error) {
	realPath, ok := normalizeRunLogWritePath(path)
	if !ok {
		return nil, fmt.Errorf("run.log 路径无效或无法解析为本地路径: %s", strings.TrimSpace(path))
	}
	if err := ensureDir(filepath.Dir(realPath)); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(realPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, err
	}
	return &backupRunLogger{file: file, path: path}, nil
}

func (l *backupRunLogger) Write(p []byte) (int, error) {
	if l == nil || l.file == nil {
		return len(p), nil
	}
	n, err := l.file.Write(p)
	_ = l.file.Sync()
	return n, err
}

func (l *backupRunLogger) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	_ = l.file.Sync()
	return l.file.Close()
}

func (l *backupRunLogger) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

func appendBackupRunLog(path string, level string, format string, args ...any) {
	realPath, ok := normalizeRunLogWritePath(path)
	if !ok {
		// 路径为空或为无法解析的存储目标引用：静默跳过，避免写出 st:<id>: 之类的错误目录。
		return
	}
	if err := ensureDir(filepath.Dir(realPath)); err != nil {
		return
	}
	file, err := os.OpenFile(realPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer file.Close()
	line := fmt.Sprintf(format, args...)
	if !strings.HasSuffix(line, "\n") {
		line += "\n"
	}
	_, _ = file.WriteString(fmt.Sprintf("[%s] [%s] %s",
		time.Now().Format("2006-01-02 15:04:05"),
		strings.ToUpper(strings.TrimSpace(level)),
		line,
	))
	_ = file.Sync()
}

func (l *backupRunLogger) logf(level string, format string, args ...any) {
	if l == nil {
		return
	}
	line := fmt.Sprintf(format, args...)
	if !strings.HasSuffix(line, "\n") {
		line += "\n"
	}
	_, _ = l.Write([]byte(fmt.Sprintf("[%s] [%s] %s",
		time.Now().Format("2006-01-02 15:04:05"),
		strings.ToUpper(strings.TrimSpace(level)),
		line,
	)))
}

func (l *backupRunLogger) Infof(format string, args ...any) {
	l.logf("INFO", format, args...)
}

func (l *backupRunLogger) Warnf(format string, args ...any) {
	l.logf("WARN", format, args...)
}

func (l *backupRunLogger) Errorf(format string, args ...any) {
	l.logf("ERROR", format, args...)
}

// BackupRunDirSpec 描述一次备份在存储目标上的批次目录所需的最小信息。
//
// 目录规则：<base>/<instance>/<task>/<run>
//   - 新规则（RunSeq > 0）：instance 段用数据源业务 ID（ds-xxxxxxxxxxxx），run 段用「序号-runID」
//   - 历史数据（RunSeq == 0）：沿用旧规则（数字实例 ID + 纯 runID），保证老目录仍可定位、可删除
//
// RunID 必须始终保留在目录名里：它是全局唯一且永不变化的锚点，序号只作可读性前缀。
type BackupRunDirSpec struct {
	SourceInstanceID uint64
	InstanceBizID    string
	TaskID           string
	RunID            uint64
	RunSeq           uint64
}

// instanceSegment 决定实例层目录名。
// 有数据源业务 ID 就用它（新规则）；拿不到则回落数字主键（历史数据与降级场景），
// 保证路径构造不会因为缺一条记录而失败。
func (s BackupRunDirSpec) instanceSegment() string {
	if biz := sanitizePathSegment(s.InstanceBizID, ""); biz != "" {
		return biz
	}
	return strconv.FormatUint(s.SourceInstanceID, 10)
}

func (s BackupRunDirSpec) runSegment() string {
	if s.RunSeq == 0 {
		return strconv.FormatUint(s.RunID, 10)
	}
	return fmt.Sprintf("%06d-%d", s.RunSeq, s.RunID)
}

// RelDir 返回批次目录相对存储目标根目录的斜杠路径
func (s BackupRunDirSpec) RelDir() string {
	return path.Join(s.instanceSegment(), sanitizePathSegment(s.TaskID, "task"), s.runSegment())
}

// RelTaskDir 返回「实例/任务」两级目录（不含批次），用于扫描该任务的全部批次
func (s BackupRunDirSpec) RelTaskDir() string {
	return path.Join(s.instanceSegment(), sanitizePathSegment(s.TaskID, "task"))
}

// LegacyTaskRelDir 返回旧命名规则下的「实例/任务」目录，用于兼容升级前落盘的历史批次
func (s BackupRunDirSpec) LegacyTaskRelDir() string {
	return path.Join(strconv.FormatUint(s.SourceInstanceID, 10), sanitizePathSegment(s.TaskID, "task"))
}

// TaskRootCandidates 返回该任务批次可能存在的全部根目录（新规则在前，历史规则在后，自动去重）。
// 升级后历史批次仍在旧目录，扫描类逻辑必须同时覆盖两者，否则增量备份会丢失基线。
func (s BackupRunDirSpec) TaskRootCandidates(storageBaseDir string) []string {
	roots := make([]string, 0, 2)
	seen := map[string]struct{}{}
	for _, rel := range []string{s.RelTaskDir(), s.LegacyTaskRelDir()} {
		abs := filepath.Join(storageBaseDir, filepath.FromSlash(rel))
		if _, ok := seen[abs]; ok {
			continue
		}
		seen[abs] = struct{}{}
		roots = append(roots, abs)
	}
	return roots
}

// RelPath 返回批次目录内某个文件的相对斜杠路径
func (s BackupRunDirSpec) RelPath(fileName string) string {
	return path.Join(s.RelDir(), path.Base(fileName))
}

// AbsDir 返回批次目录在存储目标上的本地绝对路径
func (s BackupRunDirSpec) AbsDir(storageBaseDir string) string {
	return filepath.Join(storageBaseDir, filepath.FromSlash(s.RelDir()))
}

func resolveBackupRunDirByDriver(baseDir string, driverType string, dbType string, spec BackupRunDirSpec) string {
	return filepath.Join(resolveBackupStorageBaseDir(baseDir, driverType, dbType), filepath.FromSlash(spec.RelDir()))
}

func resolveBackupRunLogPathByDriver(baseDir string, driverType string, dbType string, spec BackupRunDirSpec) string {
	return filepath.Join(resolveBackupRunDirByDriver(baseDir, driverType, dbType, spec), "run.log")
}
