package services

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"
)

// fakeS3 是一个只实现 ListObjectsV2 与 GET object 的最小 S3 兼容端点。
// 目的是在不依赖真实云厂商的前提下验证对象存储基线读取链路（分页、空结果、缺失、损坏）。
type fakeS3 struct {
	t     *testing.T
	mu    sync.Mutex
	items map[string][]byte

	pageSize  int  // 服务端每页返回的对象数，用来逼出客户端翻页
	listFails bool // LIST 返回 500
	getFails  bool // GET 返回 500（模拟下载失败）

	listCalls int
	putKeys   []string

	server *httptest.Server
}

func newFakeS3(t *testing.T, pageSize int) *fakeS3 {
	t.Helper()
	f := &fakeS3{t: t, items: map[string][]byte{}, pageSize: pageSize}
	if f.pageSize <= 0 {
		f.pageSize = 2
	}
	f.server = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeS3) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	q := r.URL.Query()
	key := strings.TrimPrefix(r.URL.Path, "/demo-bucket/")

	switch {
	case r.Method == http.MethodGet && q.Has("list-type"):
		f.listCalls++
		if f.listFails {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`<?xml version="1.0"?><Error><Code>InternalError</Code></Error>`))
			return
		}
		f.writeListPage(w, q.Get("prefix"), q.Get("continuation-token"))
	case r.Method == http.MethodGet:
		if f.getFails {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`<?xml version="1.0"?><Error><Code>InternalError</Code></Error>`))
			return
		}
		obj, ok := f.items[key]
		// nil value = 幽灵对象：列举能看到，GET 返回 404（模拟列举与下载之间的并发删除）
		if !ok || obj == nil {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`<?xml version="1.0"?><Error><Code>NoSuchKey</Code></Error>`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(obj)
	case r.Method == http.MethodPut:
		body := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(body)
		f.putKeys = append(f.putKeys, key)
		w.WriteHeader(http.StatusOK)
	default:
		w.WriteHeader(http.StatusOK)
	}
}

func (f *fakeS3) writeListPage(w http.ResponseWriter, prefix string, token string) {
	keys := make([]string, 0, len(f.items))
	for k := range f.items {
		if prefix == "" || strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	start := 0
	if token != "" {
		for i, k := range keys {
			if k == token {
				start = i + 1
				break
			}
		}
	}
	end := start + f.pageSize
	if end > len(keys) {
		end = len(keys)
	}
	page := keys[start:end]
	truncated := end < len(keys)
	next := ""
	if truncated && len(page) > 0 {
		next = page[len(page)-1]
	}

	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><ListBucketResult><Name>demo-bucket</Name>`)
	b.WriteString(fmt.Sprintf("<Prefix>%s</Prefix>", prefix))
	b.WriteString(fmt.Sprintf("<KeyCount>%d</KeyCount>", len(page)))
	b.WriteString(fmt.Sprintf("<IsTruncated>%t</IsTruncated>", truncated))
	if next != "" {
		b.WriteString(fmt.Sprintf("<NextContinuationToken>%s</NextContinuationToken>", next))
	}
	for _, k := range page {
		b.WriteString(fmt.Sprintf("<Contents><Key>%s</Key><Size>%d</Size><LastModified>2026-10-10T00:00:00.000Z</LastModified></Contents>", k, len(f.items[k])))
	}
	b.WriteString(`</ListBucketResult>`)
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(b.String()))
}

func (f *fakeS3) put(key string, content []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.items[key] = content
}

// putGhost 放一个"列举得到、下载不到"的幽灵对象。
func (f *fakeS3) putGhost(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.items[key] = nil
}

func (f *fakeS3) objectClient() *BackupExecutionStorageTarget {
	return &BackupExecutionStorageTarget{
		ID:               8,
		Type:             "OSS_ALIYUN",
		S3Endpoint:       f.server.URL,
		S3Region:         "cn-hangzhou",
		S3Bucket:         "demo-bucket",
		S3AccessKey:      "ak",
		S3SecretKey:      "sk",
		S3ForcePathStyle: true,
	}
}

func manifestJSON(toLSN string, createdAt string) []byte {
	raw, _ := json.Marshal(mysqlPhysicalBackupManifest{
		BackupType: "FULL",
		FromLSN:    "1000",
		ToLSN:      toLSN,
		CreatedAt:  createdAt,
	})
	return raw
}

func testRunDirSpec() BackupRunDirSpec {
	return BackupRunDirSpec{SourceInstanceID: 38, InstanceBizID: "ds-abc", TaskID: "bk-test", RunID: 900, RunSeq: 7}
}

func TestListObjectsWithPrefixPaginatesUntilExhausted(t *testing.T) {
	f := newFakeS3(t, 2)
	for i := 1; i <= 5; i++ {
		f.put(fmt.Sprintf("ds-abc/bk-test/00000%d-%d/backup.meta.json", i, 100+i), manifestJSON(fmt.Sprintf("%d", 1000+i), "2026-10-01T00:00:00Z"))
	}
	client, err := buildStorageTargetObjectClient(f.objectClient())
	if err != nil {
		t.Fatalf("buildStorageTargetObjectClient: %v", err)
	}
	got, err := client.ListObjectsWithPrefix(context.Background(), "ds-abc/bk-test/", 0, 0)
	if err != nil {
		t.Fatalf("ListObjectsWithPrefix: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("列举到 %d 个对象, want 5", len(got))
	}
	// 5 个对象 / 每页 2 个 = 3 次请求（2+2+1），证明翻页真的发生了
	if f.listCalls != 3 {
		t.Fatalf("LIST 调用次数 = %d, want 3", f.listCalls)
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].Key >= got[i].Key {
			t.Fatalf("返回未按 key 升序: %q >= %q", got[i-1].Key, got[i].Key)
		}
	}
}

func TestListObjectsWithPrefixReturnsEmptyForUnknownPrefix(t *testing.T) {
	f := newFakeS3(t, 2)
	f.put("ds-abc/bk-test/000001-101/backup.meta.json", manifestJSON("1001", "2026-10-01T00:00:00Z"))
	client, err := buildStorageTargetObjectClient(f.objectClient())
	if err != nil {
		t.Fatalf("buildStorageTargetObjectClient: %v", err)
	}
	got, err := client.ListObjectsWithPrefix(context.Background(), "ds-abc/bk-other/", 0, 0)
	if err != nil {
		t.Fatalf("空结果不应返回 error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("空结果返回了 %d 个对象, want 0", len(got))
	}
}

func TestFindLatestMySQLPhysicalBaseViaObjectStoragePicksNewest(t *testing.T) {
	f := newFakeS3(t, 2)
	spec := testRunDirSpec()
	f.put(spec.RelTaskDir()+"/000001-101/backup.meta.json", manifestJSON("1001", "2026-09-01T00:00:00Z"))
	f.put(spec.RelTaskDir()+"/000003-103/backup.meta.json", manifestJSON("1003", "2026-10-01T00:00:00Z"))
	f.put(spec.RelTaskDir()+"/000002-102/backup.meta.json", manifestJSON("1002", "2026-09-15T00:00:00Z"))
	// 干扰项：同前缀下的非 manifest 对象必须被过滤掉
	f.put(spec.RelTaskDir()+"/000003-103/full.xb", []byte("binary"))

	got, err := findLatestMySQLPhysicalBaseViaObjectStorage(f.objectClient(), spec)
	if err != nil {
		t.Fatalf("findLatestMySQLPhysicalBaseViaObjectStorage: %v", err)
	}
	if got == nil {
		t.Fatal("返回 nil, want 非 nil 结构")
	}
	if got.ToLSN != "1003" {
		t.Fatalf("ToLSN = %q, want 1003（应取最新批次）", got.ToLSN)
	}
	// 命中即返回（与 SSH 版一致），所以扫描计数只累计到命中的那一个
	if got.ScannedCount != 1 || got.InvalidCount != 0 {
		t.Fatalf("Scanned/Invalid = %d/%d, want 1/0", got.ScannedCount, got.InvalidCount)
	}
	if got.ManifestPath == "" {
		t.Fatal("ManifestPath 为空")
	}

	// 非 manifest 对象的过滤在列举层完成
	client, err := buildStorageTargetObjectClient(f.objectClient())
	if err != nil {
		t.Fatalf("buildStorageTargetObjectClient: %v", err)
	}
	keys, err := listMySQLPhysicalManifestKeys(context.Background(), client, spec)
	if err != nil {
		t.Fatalf("listMySQLPhysicalManifestKeys: %v", err)
	}
	if len(keys) != 3 {
		t.Fatalf("列举到 %d 个 manifest, want 3（full.xb 应被过滤）: %v", len(keys), keys)
	}
}

func TestFindLatestMySQLPhysicalBaseViaObjectStorageSkipsCorruptedAndMissing(t *testing.T) {
	f := newFakeS3(t, 2)
	spec := testRunDirSpec()
	// 最新批次损坏（JSON 不可解析），次新批次 to_lsn 为空，第三新才可用
	f.put(spec.RelTaskDir()+"/000003-103/backup.meta.json", []byte("{not-json"))
	f.put(spec.RelTaskDir()+"/000002-102/backup.meta.json", manifestJSON("", "2026-10-01T00:00:00Z"))
	f.put(spec.RelTaskDir()+"/000001-101/backup.meta.json", manifestJSON("1001", "2026-09-01T00:00:00Z"))

	got, err := findLatestMySQLPhysicalBaseViaObjectStorage(f.objectClient(), spec)
	if err != nil {
		t.Fatalf("个别对象损坏不应返回 error: %v", err)
	}
	if got.ToLSN != "1001" {
		t.Fatalf("ToLSN = %q, want 1001（应回退到第一个可用基线）", got.ToLSN)
	}
	if got.ScannedCount != 3 || got.InvalidCount != 2 {
		t.Fatalf("Scanned/Invalid = %d/%d, want 3/2", got.ScannedCount, got.InvalidCount)
	}
}

func TestFindLatestMySQLPhysicalBaseViaObjectStorageHandlesMissingObject(t *testing.T) {
	f := newFakeS3(t, 2)
	spec := testRunDirSpec()
	key := spec.RelTaskDir() + "/000001-101/backup.meta.json"
	// 列举能看到，但 GET 返回 404（列举与下载之间被并发删除）
	f.putGhost(key)
	target := f.objectClient()
	client, err := buildStorageTargetObjectClient(target)
	if err != nil {
		t.Fatalf("buildStorageTargetObjectClient: %v", err)
	}
	keys, err := listMySQLPhysicalManifestKeys(context.Background(), client, spec)
	if err != nil {
		t.Fatalf("listMySQLPhysicalManifestKeys: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("列举到 %d 个 manifest, want 1", len(keys))
	}

	got, err := findLatestMySQLPhysicalBaseViaObjectStorage(target, spec)
	if err != nil {
		t.Fatalf("对象不存在不应返回 error: %v", err)
	}
	if got.ToLSN != "" {
		t.Fatalf("ToLSN = %q, want 空（应按无基线处理）", got.ToLSN)
	}
	if got.ScannedCount != 1 || got.InvalidCount != 1 {
		t.Fatalf("Scanned/Invalid = %d/%d, want 1/1", got.ScannedCount, got.InvalidCount)
	}
}

func TestFindLatestMySQLPhysicalBaseViaObjectStorageReportsListFailure(t *testing.T) {
	f := newFakeS3(t, 2)
	f.listFails = true
	spec := testRunDirSpec()
	if _, err := findLatestMySQLPhysicalBaseViaObjectStorage(f.objectClient(), spec); err == nil {
		t.Fatal("列举失败应返回 error，让调用方按探测失败处理")
	}
}

func TestFindLatestMySQLPhysicalBaseDispatchesByStorageType(t *testing.T) {
	f := newFakeS3(t, 2)
	spec := testRunDirSpec()
	f.put(spec.RelTaskDir()+"/000001-101/backup.meta.json", manifestJSON("1001", "2026-10-01T00:00:00Z"))

	// 对象存储类型：走 LIST+GET 链路，不碰 SSH
	got, err := findLatestMySQLPhysicalBase(f.objectClient(), spec)
	if err != nil {
		t.Fatalf("对象存储类型不应走 SSH 链路: %v", err)
	}
	if got == nil || got.ToLSN != "1001" {
		t.Fatalf("对象存储链路取到的 ToLSN 异常: %+v", got)
	}

	// 未知类型：仍走 LOCAL_SERVER 校验并报原错误，不会误入对象存储链路
	unknown := &BackupExecutionStorageTarget{ID: 9, Type: "UNKNOWN_KIND"}
	if _, err := findLatestMySQLPhysicalBase(unknown, spec); err == nil {
		t.Fatal("未知存储类型应报错")
	} else if !strings.Contains(err.Error(), "LOCAL_SERVER") {
		t.Fatalf("未知类型应回落到 LOCAL_SERVER 校验，实际错误: %v", err)
	}

	// 对象存储类型但缺 AK/SK：显式报错，不静默退化
	noCred := &BackupExecutionStorageTarget{ID: 8, Type: "OSS_ALIYUN", S3Endpoint: f.server.URL, S3Bucket: "demo-bucket"}
	if _, err := findLatestMySQLPhysicalBase(noCred, spec); err == nil {
		t.Fatal("缺少 AK/SK 应报错")
	}
}

func TestUploadMySQLPhysicalManifestViaObjectStorage(t *testing.T) {
	f := newFakeS3(t, 2)
	spec := testRunDirSpec()
	manifest := &mysqlPhysicalBackupManifest{RunID: 900, TaskID: "bk-test", BackupType: "FULL", ToLSN: "2000"}
	if err := uploadMySQLPhysicalManifestViaObjectStorage(f.objectClient(), spec, manifest); err != nil {
		t.Fatalf("uploadMySQLPhysicalManifestViaObjectStorage: %v", err)
	}
	want := strings.TrimPrefix(spec.RelPath("backup.meta.json"), "/")
	if len(f.putKeys) != 1 || f.putKeys[0] != want {
		t.Fatalf("写入对象键 = %v, want [%s]", f.putKeys, want)
	}
}

func TestListMySQLPhysicalManifestKeysCoversLegacyPrefix(t *testing.T) {
	f := newFakeS3(t, 2)
	spec := testRunDirSpec()
	// 历史命名（数字实例 ID）下也应有一次备份
	f.put("38/bk-test/000001-101/backup.meta.json", manifestJSON("1001", "2026-09-01T00:00:00Z"))
	client, err := buildStorageTargetObjectClient(f.objectClient())
	if err != nil {
		t.Fatalf("buildStorageTargetObjectClient: %v", err)
	}
	keys, err := listMySQLPhysicalManifestKeys(context.Background(), client, spec)
	if err != nil {
		t.Fatalf("listMySQLPhysicalManifestKeys: %v", err)
	}
	if len(keys) != 1 || keys[0] != "38/bk-test/000001-101/backup.meta.json" {
		t.Fatalf("keys = %v, want 命中历史前缀下的 1 条", keys)
	}
}

// 保证 URL 构造正确：prefix 中的斜杠必须出现在 canonical query 里（S3 签名要求）。
func TestListObjectsWithPrefixKeepsSlashInCanonicalQuery(t *testing.T) {
	q := url.Values{}
	q.Set("list-type", "2")
	q.Set("prefix", "ds-abc/bk-test/")
	encoded := encodeCanonicalQuery(q)
	if !strings.Contains(encoded, "prefix=ds-abc%2Fbk-test%2F") {
		t.Fatalf("canonical query = %q, 斜杠未被正确编码", encoded)
	}
}
