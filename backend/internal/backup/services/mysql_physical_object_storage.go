package services

// MySQL 物理备份在**对象存储仓库**上的基线读写。
//
// 为什么需要这一份实现：
// 物理增量依赖上一次备份的 LSN 终点（to_lsn），记录在批次目录下的 backup.meta.json。
// SSH 实现靠 `find` 遍历文件系统找它，而对象存储没有 shell，只有 LIST/GET 两个接口 ——
// 于是改成「按任务目录前缀 LIST 枚举对象 → 逐个 GET 下载解析」，语义与 SSH 版对齐：
// 同样倒序扫描、同样区分「从未备份」与「基线全部损坏」、同样返回 mysqlPhysicalBackupBase。
//
// 两类仓库的寻址方式不同，这里不做任何静默退化：对象存储配置缺失时直接报错，
// 由调用方按「无基线 → 降级全量」处置，绝不回头去试 SSH。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
)

// mysqlPhysicalManifestFileName 批次目录下的基线元数据文件名，与 SSH 侧 find -name 的条件一致。
const mysqlPhysicalManifestFileName = "backup.meta.json"

// mysqlPhysicalObjectProbeTimeout 基线探测的整体超时，同时用作单个 HTTP 请求的超时。
//
// 定得克制是这个值的要点：基线探测跑在**同步路径**上（创建执行记录时判定全量/增量、
// 执行计划构建时解析基线），超时会直接拖慢调度循环与「立即备份」接口响应，
// 而调用方还有一次重试，最坏耗时是它的两倍。
// manifest 只是几 KB 的 JSON，列举 + 几次下载在这个预算内绰绰有余；
// 超时即按探测失败处理，调用方降级全量，不会让备份卡住。
const mysqlPhysicalObjectProbeTimeout = 30 * time.Second

// buildStorageTargetObjectClient 依据执行期存储目标快照构造 S3 兼容客户端。
// 配置缺项全部显式报错——对象存储的寻址靠 bucket/endpoint/AK/SK，
// 任何一项缺失都只会得到更晚、更难定位的失败。
func buildStorageTargetObjectClient(target *BackupExecutionStorageTarget) (*S3Client, error) {
	if target == nil {
		return nil, fmt.Errorf("存储目标配置为空")
	}
	if strings.TrimSpace(target.S3Bucket) == "" {
		return nil, fmt.Errorf("对象存储目标缺少 bucket 配置")
	}
	if strings.TrimSpace(target.S3Endpoint) == "" {
		return nil, fmt.Errorf("对象存储目标缺少 endpoint 配置")
	}
	if strings.TrimSpace(target.S3AccessKey) == "" || strings.TrimSpace(target.S3SecretKey) == "" {
		return nil, fmt.Errorf("对象存储目标缺少 AK/SK 凭据")
	}
	return NewS3Client(S3ClientOptions{
		Endpoint:       strings.TrimSpace(target.S3Endpoint),
		Region:         strings.TrimSpace(target.S3Region),
		Bucket:         strings.TrimSpace(target.S3Bucket),
		AccessKey:      strings.TrimSpace(target.S3AccessKey),
		SecretKey:      target.S3SecretKey,
		ForcePathStyle: target.S3ForcePathStyle,
		HTTPTimeout:    mysqlPhysicalObjectProbeTimeout,
	})
}

// findLatestMySQLPhysicalBaseViaObjectStorage 在对象存储仓库上查找最新的有效物理基线。
//
// 返回语义与 SSH 版 findLatestMySQLPhysicalBase 完全一致：
//   - 找到：返回 ToLSN 非空的基线；
//   - 没找到：返回非 nil 且 ToLSN 为空的结构，ScannedCount/InvalidCount 供调用方诊断；
//   - 仓库不可达 / 配置不完整 / 列举失败：返回 error（调用方按"探测失败"处理并降级全量）。
func findLatestMySQLPhysicalBaseViaObjectStorage(target *BackupExecutionStorageTarget, spec BackupRunDirSpec) (*mysqlPhysicalBackupBase, error) {
	client, err := buildStorageTargetObjectClient(target)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), mysqlPhysicalObjectProbeTimeout)
	defer cancel()

	// 列举失败是真错误（仓库不可达/凭据不足），不在这里吞掉：
	// 调用方拿到 error 会重试一次再降级，而"静默当成没基线"会让一次网络抖动白白多做一次全量。
	keys, err := listMySQLPhysicalManifestKeys(ctx, client, spec)
	if err != nil {
		return nil, fmt.Errorf("列举基线 manifest 失败: %w", err)
	}

	result := &mysqlPhysicalBackupBase{}
	// 倒序：与 SSH 版 sort -V 后从尾部取第一条等价，最新批次优先。
	for i := len(keys) - 1; i >= 0; i-- {
		key := keys[i]
		result.ScannedCount++
		manifest, readErr := downloadMySQLPhysicalManifest(ctx, client, key)
		if readErr != nil {
			// 单个对象读不了（并发删除的 404、内容损坏、网络抖动）只跳过这个候选，
			// 继续找更旧的批次——旧基线照样能支撑一次增量。
			result.InvalidCount++
			continue
		}
		if strings.TrimSpace(manifest.ToLSN) == "" {
			result.InvalidCount++
			continue
		}
		result.FromLSN = strings.TrimSpace(manifest.FromLSN)
		result.ToLSN = strings.TrimSpace(manifest.ToLSN)
		result.BackupType = strings.ToUpper(strings.TrimSpace(manifest.BackupType))
		result.CreatedAt = strings.TrimSpace(manifest.CreatedAt)
		result.StorageArtifactPath = strings.TrimSpace(manifest.StorageArtifactPath)
		// 路径优先取 manifest 自报值，为空时用实际对象键兜底：
		// 对象存储里 key 是唯一寻址依据，丢了它后续就无法定位基线产物。
		result.ManifestPath = strings.TrimSpace(manifest.ManifestPath)
		if result.ManifestPath == "" {
			result.ManifestPath = key
		}
		return result, nil
	}
	return result, nil
}

// listMySQLPhysicalManifestKeys 列举任务目录下全部基线 manifest 的对象键，按 key 升序返回。
//
// 对象存储没有目录，前缀是唯一的"目录"表达方式。runSegment 采用 %06d 补零命名，
// 因此字典序等价于 SSH 侧的 sort -V 版本序；两个候选前缀（新 biz ID 规则 / 历史数字 ID 规则）
// 分别列举后统一排序，与 SSH 版同时扫描两个 root 的行为对齐。
func listMySQLPhysicalManifestKeys(ctx context.Context, client *S3Client, spec BackupRunDirSpec) ([]string, error) {
	prefixes := make([]string, 0, 2)
	seen := map[string]struct{}{}
	for _, rel := range []string{spec.RelTaskDir(), spec.LegacyTaskRelDir()} {
		normalized := strings.Trim(strings.TrimSpace(rel), "/")
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		prefixes = append(prefixes, normalized+"/")
	}
	if len(prefixes) == 0 {
		return nil, fmt.Errorf("任务目录前缀为空，无法列举基线对象")
	}

	keys := make([]string, 0, 8)
	for _, prefix := range prefixes {
		items, err := client.ListObjectsWithPrefix(ctx, prefix, 0, 0)
		if err != nil {
			return nil, fmt.Errorf("前缀 %s: %w", prefix, err)
		}
		for _, item := range items {
			if path.Base(item.Key) != mysqlPhysicalManifestFileName {
				continue
			}
			keys = append(keys, item.Key)
		}
	}
	sort.Strings(keys)
	return keys, nil
}

// downloadMySQLPhysicalManifest 下载并解析单个基线 manifest。
// 空对象、非法 JSON 都返回 error，由调用方计入 InvalidCount。
func downloadMySQLPhysicalManifest(ctx context.Context, client *S3Client, key string) (*mysqlPhysicalBackupManifest, error) {
	var buf bytes.Buffer
	if _, err := client.GetObject(ctx, key, &buf); err != nil {
		return nil, err
	}
	if buf.Len() == 0 {
		return nil, fmt.Errorf("对象 %s 内容为空", key)
	}
	var manifest mysqlPhysicalBackupManifest
	if err := json.Unmarshal(buf.Bytes(), &manifest); err != nil {
		return nil, fmt.Errorf("解析对象 %s 失败: %w", key, err)
	}
	return &manifest, nil
}

// uploadMySQLPhysicalManifestViaObjectStorage 把本次备份的元数据写回对象存储仓库。
//
// 与 SSH 版 uploadMySQLPhysicalManifest 的落点规则一致（批次目录下的 backup.meta.json），
// 只是载体从"写文件"换成 PutObject。这次写入就是下一次增量的基线。
func uploadMySQLPhysicalManifestViaObjectStorage(target *BackupExecutionStorageTarget, spec BackupRunDirSpec, manifest *mysqlPhysicalBackupManifest) error {
	client, err := buildStorageTargetObjectClient(target)
	if err != nil {
		return err
	}
	buf, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化基线 manifest 失败: %w", err)
	}
	key := strings.TrimPrefix(strings.TrimSpace(spec.RelPath(mysqlPhysicalManifestFileName)), "/")
	if key == "" {
		return fmt.Errorf("基线 manifest 对象键为空")
	}
	ctx, cancel := context.WithTimeout(context.Background(), mysqlPhysicalObjectProbeTimeout)
	defer cancel()
	if err := client.PutObject(ctx, key, buf, "application/json"); err != nil {
		return fmt.Errorf("写入基线 manifest 对象 %s 失败: %w", key, err)
	}
	return nil
}
