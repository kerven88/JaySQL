package services

import (
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"

	backupModels "github.com/simajiong/jaysql/internal/backup/models"
)

// ============================================================================
// MySQL XtraBackup 物理恢复链路解析
//
// 增量备份的 xbstream 只是"自基线起的数据页差异"，它无法单独还原出一套可用的
// 数据文件。要恢复一个增量备份集，必须把它依赖的全量基线，以及两者之间的每一级
// 增量都按先后顺序合并（xtrabackup --prepare --apply-log-only …）才行。
//
// 备份侧已经把依赖关系写进了 backup.meta.json：
//   - base_storage_artifact_path：本层依赖的上一级 xbstream
//   - from_lsn / to_lsn：本层覆盖的 LSN 区间
// 这里据此把链路完整回溯出来，并在合并前校验链路完整性，避免"缺基线的增量"
// 被当成全量直接 prepare（那样会得到一份残缺的数据目录）。
// ============================================================================

// mysqlPhysicalRestoreChainMaxDepth 限制链路回溯深度，防御元数据成环或异常数据。
const mysqlPhysicalRestoreChainMaxDepth = 64

// mysqlPhysicalRestoreChainNode 描述恢复链路中的一层备份。
// Order 从 1 开始递增，1 为最底层的全量基线，之后依次为各级增量。
type mysqlPhysicalRestoreChainNode struct {
	Order               int    `json:"order"`
	BackupType          string `json:"backup_type"`
	RunID               uint64 `json:"run_id,omitempty"`
	TaskID              string `json:"task_id,omitempty"`
	LocalPath           string `json:"local_path"`
	SizeBytes           int64  `json:"size_bytes"`
	Sha256              string `json:"sha256,omitempty"`
	FromLSN             string `json:"from_lsn,omitempty"`
	ToLSN               string `json:"to_lsn,omitempty"`
	ServerVersion       string `json:"server_version,omitempty"`
	StorageArtifactPath string `json:"storage_artifact_path,omitempty"`
	ManifestPath        string `json:"manifest_path,omitempty"`

	// RemotePath 仅执行期使用：该层在目标节点上的 xbstream 落地路径，不持久化。
	RemotePath string `json:"-"`
}

// mysqlPhysicalChainPathResolver 把备份文件引用（st:<id>:///… 或历史绝对路径）
// 解析为本机可读的绝对路径。抽成函数类型是为了让链路回溯逻辑可以脱离数据库独立测试。
type mysqlPhysicalChainPathResolver func(ref string) (string, error)

// resolveMySQLPhysicalRestoreChain 解析目标备份集的恢复链路。
// 返回顺序为 [FULL, INCR1, ..., 目标备份集]，并已完成完整性校验。
func resolveMySQLPhysicalRestoreChain(set *backupModels.InsightBackupSets) ([]mysqlPhysicalRestoreChainNode, error) {
	if set == nil {
		return nil, fmt.Errorf("备份集不存在")
	}
	targetID := set.StorageTargetID
	return resolveMySQLPhysicalRestoreChainWith(set.FilePath, func(ref string) (string, error) {
		return resolveMySQLPhysicalChainArtifactLocalPath(ref, targetID)
	})
}

// resolveMySQLPhysicalRestoreChainWith 是链路回溯的实际实现。
// 从 entryRef 出发，沿每一层 backup.meta.json 的 base_storage_artifact_path 向上回溯，
// 直到抵达全量基线，再反转为合并顺序。
func resolveMySQLPhysicalRestoreChainWith(entryRef string, resolve mysqlPhysicalChainPathResolver) ([]mysqlPhysicalRestoreChainNode, error) {
	if resolve == nil {
		return nil, fmt.Errorf("备份文件路径解析器未初始化")
	}
	entry, err := resolve(entryRef)
	if err != nil {
		return nil, err
	}

	// 先从目标备份集沿依赖逐层向上回溯（顺序是反的），再反转成合并顺序。
	reversed := make([]mysqlPhysicalRestoreChainNode, 0, 4)
	seen := make(map[string]struct{}, 4)
	current := entry
	for depth := 0; ; depth++ {
		if depth >= mysqlPhysicalRestoreChainMaxDepth {
			return nil, fmt.Errorf("备份链路层级超过 %d 层（疑似元数据成环），已中止解析", mysqlPhysicalRestoreChainMaxDepth)
		}
		key := filepath.Clean(current)
		if _, dup := seen[key]; dup {
			return nil, fmt.Errorf("备份链路出现重复节点（疑似元数据成环）: %s", current)
		}
		seen[key] = struct{}{}

		manifest, manifestLocalPath, loadErr := loadMySQLPhysicalManifestBesideArtifact(current)
		if loadErr != nil {
			return nil, loadErr
		}
		node := mysqlPhysicalRestoreChainNode{
			BackupType:          normalizeMySQLPhysicalChainBackupType(manifest.BackupType),
			RunID:               manifest.RunID,
			TaskID:              strings.TrimSpace(manifest.TaskID),
			LocalPath:           current,
			SizeBytes:           statMySQLPhysicalChainFileSize(current),
			Sha256:              strings.TrimSpace(manifest.ArtifactSHA256),
			FromLSN:             strings.TrimSpace(manifest.FromLSN),
			ToLSN:               strings.TrimSpace(manifest.ToLSN),
			ServerVersion:       strings.TrimSpace(manifest.ServerVersion),
			StorageArtifactPath: strings.TrimSpace(manifest.StorageArtifactPath),
			ManifestPath:        manifestLocalPath,
		}
		if node.SizeBytes <= 0 {
			node.SizeBytes = manifest.ArtifactSizeBytes
		}
		reversed = append(reversed, node)

		if !strings.EqualFold(node.BackupType, "INCR") {
			// 只有 FULL 是链路终点；其他类型交给 validate 阶段给出明确错误。
			break
		}
		baseRef := strings.TrimSpace(manifest.BaseStorageArtifact)
		if baseRef == "" {
			return nil, fmt.Errorf(
				"增量备份（run=%d）的元数据缺少基线引用 base_storage_artifact_path，无法拼接恢复链路；"+
					"请确认该增量备份是由本平台生成，且基线备份未被清理", manifest.RunID)
		}
		next, nextErr := resolve(baseRef)
		if nextErr != nil {
			return nil, fmt.Errorf("回溯增量所依赖的上一级备份失败: %w", nextErr)
		}
		current = next
	}

	chain := make([]mysqlPhysicalRestoreChainNode, 0, len(reversed))
	for i := len(reversed) - 1; i >= 0; i-- {
		chain = append(chain, reversed[i])
	}
	for i := range chain {
		chain[i].Order = i + 1
	}
	if err := validateMySQLPhysicalRestoreChain(chain); err != nil {
		return nil, err
	}
	return chain, nil
}

// validateMySQLPhysicalRestoreChain 校验链路的合并可行性：
// 必须以全量打底，且相邻两级的 LSN 首尾衔接。
func validateMySQLPhysicalRestoreChain(chain []mysqlPhysicalRestoreChainNode) error {
	if len(chain) == 0 {
		return fmt.Errorf("恢复链路为空")
	}
	if !strings.EqualFold(strings.TrimSpace(chain[0].BackupType), "FULL") {
		return fmt.Errorf("恢复链路缺少全量基线：链路最底层是 %s（非 FULL），"+
			"请确认该备份依赖的全量备份仍然存在且未被保留期清理",
			displayOrDefault(strings.TrimSpace(chain[0].BackupType), "未知类型"))
	}
	for i := 1; i < len(chain); i++ {
		parent := chain[i-1]
		child := chain[i]
		if strings.TrimSpace(child.FromLSN) == "" || strings.TrimSpace(parent.ToLSN) == "" {
			// LSN 缺失时不做硬性拦截：xtrabackup 自身在合并阶段仍会校验，避免误伤历史数据。
			continue
		}
		if !mysqlPhysicalLSNEqual(child.FromLSN, parent.ToLSN) {
			return fmt.Errorf("备份链路 LSN 不连续：第 %d 层（%s）起始 LSN=%s 与第 %d 层（%s）结束 LSN=%s 不衔接，"+
				"强行合并会得到数据页不一致的实例", child.Order, child.BackupType, child.FromLSN,
				parent.Order, parent.BackupType, parent.ToLSN)
		}
	}
	return nil
}

// mysqlPhysicalLSNEqual 比较两个 LSN 是否相同。MySQL 的 LSN 是十进制数值，
// 位数可能超过 int64 的安全区间，因此用大整数比较；非数值型（如 PostgreSQL 的
// xlog 位置格式）退化为字符串比较。
func mysqlPhysicalLSNEqual(a string, b string) bool {
	textA := strings.TrimSpace(a)
	textB := strings.TrimSpace(b)
	if textA == "" || textB == "" {
		return true
	}
	numA, okA := new(big.Int).SetString(textA, 10)
	numB, okB := new(big.Int).SetString(textB, 10)
	if okA && okB {
		return numA.Cmp(numB) == 0
	}
	return strings.EqualFold(textA, textB)
}

func resolveMySQLPhysicalChainArtifactLocalPath(ref string, fallbackStorageTargetID uint64) (string, error) {
	text := strings.TrimSpace(ref)
	if text == "" {
		return "", fmt.Errorf("备份文件路径为空")
	}
	local, ok := resolveStorageRefToLocal(text, fallbackStorageTargetID)
	if strings.TrimSpace(local) == "" {
		return "", fmt.Errorf("备份文件 %s 无法解析为本地路径（当前物理恢复仅支持服务器本地存储目标）", text)
	}
	if !ok {
		return "", fmt.Errorf("备份文件不存在或已被清理: %s", local)
	}
	return local, nil
}

// loadMySQLPhysicalManifestBesideArtifact 读取与 xbstream 同目录的 backup.meta.json。
// 备份侧写元数据时两者始终落在同一批次目录下，因此可以由产物路径直接推导。
func loadMySQLPhysicalManifestBesideArtifact(artifactLocalPath string) (*mysqlPhysicalBackupManifest, string, error) {
	manifestPath := filepath.Join(filepath.Dir(artifactLocalPath), "backup.meta.json")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, "", fmt.Errorf("读取备份元数据失败 %s: %w", manifestPath, err)
	}
	var manifest mysqlPhysicalBackupManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, "", fmt.Errorf("解析备份元数据失败 %s: %w", manifestPath, err)
	}
	return &manifest, manifestPath, nil
}

func statMySQLPhysicalChainFileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil || info == nil {
		return 0
	}
	return info.Size()
}

func normalizeMySQLPhysicalChainBackupType(value string) string {
	text := strings.ToUpper(strings.TrimSpace(value))
	if text == "" {
		return "FULL"
	}
	return text
}

// --- 链路信息展示 / 传输辅助 ---

func mysqlPhysicalRestoreChainTotalBytes(chain []mysqlPhysicalRestoreChainNode) int64 {
	var total int64
	for _, node := range chain {
		total += node.SizeBytes
	}
	return total
}

func mysqlPhysicalRestoreChainIncrCount(chain []mysqlPhysicalRestoreChainNode) int {
	count := 0
	for _, node := range chain {
		if strings.EqualFold(strings.TrimSpace(node.BackupType), "INCR") {
			count++
		}
	}
	return count
}

func summarizeMySQLPhysicalRestoreChain(chain []mysqlPhysicalRestoreChainNode) string {
	if len(chain) == 0 {
		return "恢复链路为空"
	}
	total := mysqlPhysicalRestoreChainTotalBytes(chain)
	incrCount := mysqlPhysicalRestoreChainIncrCount(chain)
	if incrCount == 0 {
		return fmt.Sprintf("恢复链路完整：直接恢复 1 个全量备份，共 %s。", formatPhysicalRestoreBytes(total))
	}
	return fmt.Sprintf("恢复链路完整：1 个全量 + %d 级增量（共 %d 层），需先合并再回填，合计 %s。",
		incrCount, len(chain), formatPhysicalRestoreBytes(total))
}

func mysqlPhysicalRestoreChainDetail(chain []mysqlPhysicalRestoreChainNode) map[string]any {
	nodes := make([]map[string]any, 0, len(chain))
	for _, node := range chain {
		nodes = append(nodes, map[string]any{
			"order":       node.Order,
			"backup_type": node.BackupType,
			"run_id":      node.RunID,
			"size_bytes":  node.SizeBytes,
			"from_lsn":    node.FromLSN,
			"to_lsn":      node.ToLSN,
			"file":        node.LocalPath,
		})
	}
	return map[string]any{
		"depth":       len(chain),
		"incr_count":  mysqlPhysicalRestoreChainIncrCount(chain),
		"total_bytes": mysqlPhysicalRestoreChainTotalBytes(chain),
		"nodes":       nodes,
	}
}

func mysqlPhysicalRestoreRemoteChainFile(workDir string, order int) string {
	return filepath.Join(workDir, "chain", fmt.Sprintf("%02d.xbstream", order))
}

func mysqlPhysicalRestoreRemoteUnpackDir(workDir string, order int) string {
	return filepath.Join(workDir, "unpacked", fmt.Sprintf("%02d", order))
}

// resolveMySQLPhysicalRestoreChainForExecution 返回执行期使用的链路，并给每一层
// 分配目标节点上的落地路径。历史 run 的 ResolvedContext 里没有链路信息，此时按
// 单文件全量恢复处理，保持与升级前一致的行为。
func resolveMySQLPhysicalRestoreChainForExecution(resolved mysqlPhysicalRestoreResolved, fallbackLocalPath string) []mysqlPhysicalRestoreChainNode {
	chain := make([]mysqlPhysicalRestoreChainNode, 0, 4)
	if len(resolved.Chain) > 0 {
		chain = append(chain, resolved.Chain...)
	} else {
		chain = append(chain, mysqlPhysicalRestoreChainNode{
			Order:      1,
			BackupType: "FULL",
			LocalPath:  strings.TrimSpace(fallbackLocalPath),
			SizeBytes:  resolved.BackupSizeBytes,
			Sha256:     strings.TrimSpace(resolved.BackupSha256),
		})
	}
	for i := range chain {
		if chain[i].Order <= 0 {
			chain[i].Order = i + 1
		}
		chain[i].RemotePath = mysqlPhysicalRestoreRemoteChainFile(resolved.WorkDir, chain[i].Order)
	}
	return chain
}
