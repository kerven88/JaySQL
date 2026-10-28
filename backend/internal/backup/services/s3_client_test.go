package services

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

func TestAwsURIEncode(t *testing.T) {
	cases := []struct {
		in         string
		encodeSlsh bool
		want       string
	}{
		{"abc/def", false, "abc/def"},
		{"abc/def", true, "abc%2Fdef"},
		{"a b", true, "a%20b"},
		{"a+b", true, "a%2Bb"},
		{"~-_.", true, "~-_."},
		{"中文", true, "%E4%B8%AD%E6%96%87"},
	}
	for _, c := range cases {
		if got := awsURIEncode(c.in, c.encodeSlsh); got != c.want {
			t.Fatalf("awsURIEncode(%q, %v) = %q, want %q", c.in, c.encodeSlsh, got, c.want)
		}
	}
}

func TestEncodeCanonicalQuerySortsAndEscapes(t *testing.T) {
	q := url.Values{}
	q.Set("uploadId", "a b+c")
	q.Set("partNumber", "2")
	got := encodeCanonicalQuery(q)
	want := "partNumber=2&uploadId=a%20b%2Bc"
	if got != want {
		t.Fatalf("encodeCanonicalQuery = %q, want %q", got, want)
	}
}

func TestCanonicalObjectPathNoDoubleSlash(t *testing.T) {
	virtualHost, err := NewS3Client(S3ClientOptions{
		Endpoint: "oss-cn-hangzhou.aliyuncs.com", Region: "cn-hangzhou",
		Bucket: "demo-bucket", AccessKey: "ak", SecretKey: "sk",
	})
	if err != nil {
		t.Fatalf("NewS3Client: %v", err)
	}
	// 这是导致 OSS 403 SignatureDoesNotMatch 的历史 bug：base path 与 key 之间必须只有一个 "/"
	if got := virtualHost.canonicalObjectPath("dir/file.dump"); got != "/dir/file.dump" {
		t.Fatalf("virtual-host path = %q, want /dir/file.dump", got)
	}
	if got := virtualHost.canonicalObjectPath("/dir/file.dump"); got != "/dir/file.dump" {
		t.Fatalf("virtual-host path (leading slash) = %q, want /dir/file.dump", got)
	}
	if got := virtualHost.canonicalObjectPath(""); got != "/" {
		t.Fatalf("virtual-host path (empty key) = %q, want /", got)
	}
	if got := virtualHost.requestHost(); got != "demo-bucket.oss-cn-hangzhou.aliyuncs.com" {
		t.Fatalf("requestHost = %q", got)
	}

	pathStyle, err := NewS3Client(S3ClientOptions{
		Endpoint: "http://127.0.0.1:9000", Region: "us-east-1",
		Bucket: "demo-bucket", AccessKey: "ak", SecretKey: "sk", ForcePathStyle: true,
	})
	if err != nil {
		t.Fatalf("NewS3Client path-style: %v", err)
	}
	if got := pathStyle.canonicalObjectPath("dir/file.dump"); got != "/demo-bucket/dir/file.dump" {
		t.Fatalf("path-style path = %q", got)
	}
	if got := pathStyle.canonicalObjectPath(""); got != "/demo-bucket/" {
		t.Fatalf("path-style path (empty key) = %q", got)
	}
	if got := pathStyle.requestHost(); got != "127.0.0.1:9000" {
		t.Fatalf("path-style host = %q", got)
	}
}

func TestNewS3ClientValidation(t *testing.T) {
	if _, err := NewS3Client(S3ClientOptions{Endpoint: "", Bucket: "b", AccessKey: "a", SecretKey: "s"}); err == nil {
		t.Fatal("空 endpoint 应当报错")
	}
	if _, err := NewS3Client(S3ClientOptions{Endpoint: "h", Bucket: "", AccessKey: "a", SecretKey: "s"}); err == nil {
		t.Fatal("空 bucket 应当报错")
	}
	if _, err := NewS3Client(S3ClientOptions{Endpoint: "h", Bucket: "b", AccessKey: "", SecretKey: ""}); err == nil {
		t.Fatal("空凭据应当报错")
	}
	c, err := NewS3Client(S3ClientOptions{Endpoint: "h", Bucket: "b", AccessKey: "a", SecretKey: "s", PartSize: 1})
	if err != nil {
		t.Fatalf("NewS3Client: %v", err)
	}
	if c.PartSize() < s3MinPartSize {
		t.Fatalf("分片大小应被提升到下限，实际 %d", c.PartSize())
	}
}

func TestIsObjectStorageType(t *testing.T) {
	for _, ok := range []string{"OSS_ALIYUN", "cos_tencent", "S3_MINIO", "OBS_HUAWEI"} {
		if !IsObjectStorageType(ok) {
			t.Fatalf("%s 应识别为对象存储", ok)
		}
	}
	if IsObjectStorageType("LOCAL_SERVER") {
		t.Fatal("LOCAL_SERVER 不应识别为对象存储")
	}
}

// ---- 分片上传端到端（对 mock S3 服务） ----

type mockS3 struct {
	mu        sync.Mutex
	parts     map[int][]byte
	etags     map[int]string
	uploadID  string
	initCalls int
	completed []s3CompletedPartXML
	aborted   bool
	objects   map[string][]byte
}

func newMockS3Server(t *testing.T) (*httptest.Server, *mockS3) {
	t.Helper()
	m := &mockS3{
		parts:    map[int][]byte{},
		etags:    map[int]string{},
		uploadID: "upload-123",
		objects:  map[string][]byte{},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			http.Error(w, "missing auth", http.StatusForbidden)
			return
		}
		// 校验 payload 摘要与 body 一致，确保签名用的 payload hash 正确
		body, _ := io.ReadAll(r.Body)
		wantHash := s3EmptyPayloadSHA256
		if len(body) > 0 {
			wantHash = s3sha256Hex(body)
		}
		if got := r.Header.Get("x-amz-content-sha256"); got != wantHash {
			http.Error(w, fmt.Sprintf("payload hash mismatch: got %s want %s", got, wantHash), http.StatusBadRequest)
			return
		}
		q := r.URL.Query()
		key := strings.TrimPrefix(r.URL.Path, "/demo-bucket/")
		m.mu.Lock()
		defer m.mu.Unlock()
		switch {
		case r.Method == http.MethodPost && q.Has("uploads"):
			m.initCalls++
			out, _ := xml.Marshal(s3InitiateMultipartUploadResult{Bucket: "demo-bucket", Key: key, UploadID: m.uploadID})
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(out)
		case r.Method == http.MethodPut && q.Has("partNumber"):
			n := 0
			_, _ = fmt.Sscanf(q.Get("partNumber"), "%d", &n)
			m.parts[n] = append([]byte(nil), body...)
			etag := fmt.Sprintf("\"etag-%d\"", n)
			m.etags[n] = etag
			w.Header().Set("ETag", etag)
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && q.Has("uploadId"):
			var payload s3CompleteMultipartUploadPayload
			_ = xml.Unmarshal(body, &payload)
			m.completed = payload.Parts
			// 按分片号拼装最终对象，便于回读校验
			var assembled []byte
			for i := 1; i <= len(m.parts); i++ {
				assembled = append(assembled, m.parts[i]...)
			}
			m.objects[key] = assembled
			out, _ := xml.Marshal(s3CompleteMultipartUploadResult{Bucket: "demo-bucket", Key: key, ETag: "\"final-etag\""})
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(out)
		case r.Method == http.MethodDelete && q.Has("uploadId"):
			m.aborted = true
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPut:
			m.objects[key] = append([]byte(nil), body...)
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodHead:
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && q.Has("list-type"):
			out, _ := xml.Marshal(s3ListBucketResult{})
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(out)
		case r.Method == http.MethodGet:
			obj, ok := m.objects[key]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(obj)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, m
}

func newMockClient(t *testing.T, srv *httptest.Server, concurrency int) *S3Client {
	t.Helper()
	c, err := NewS3Client(S3ClientOptions{
		Endpoint:       srv.URL,
		Region:         "us-east-1",
		Bucket:         "demo-bucket",
		AccessKey:      "ak",
		SecretKey:      "sk",
		ForcePathStyle: true,
		Concurrency:    concurrency,
		MaxRetries:     1,
	})
	if err != nil {
		t.Fatalf("NewS3Client: %v", err)
	}
	return c
}

func TestMultipartUploadSplitsAndCompletesInOrder(t *testing.T) {
	srv, m := newMockS3Server(t)
	client := newMockClient(t, srv, 3)

	partSize := int64(s3MinPartSize) // 5MiB
	payload := bytes.Repeat([]byte("a"), int(partSize)*2+1234)

	w, err := client.NewMultipartWriter(context.Background(), "inst/task/run/dump.sql.gz", partSize, int64(len(payload)))
	if err != nil {
		t.Fatalf("NewMultipartWriter: %v", err)
	}
	if _, err := w.Write(payload); err != nil {
		t.Fatalf("Write: %v", err)
	}
	res, err := w.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}

	if m.initCalls != 1 {
		t.Fatalf("InitiateMultipartUpload 调用次数 = %d, want 1", m.initCalls)
	}
	if len(m.parts) != 3 {
		t.Fatalf("分片数 = %d, want 3", len(m.parts))
	}
	if got := len(m.parts[1]); int64(got) != partSize {
		t.Fatalf("第 1 片大小 = %d, want %d", got, partSize)
	}
	if got := len(m.parts[2]); int64(got) != partSize {
		t.Fatalf("第 2 片大小 = %d, want %d", got, partSize)
	}
	if got := len(m.parts[3]); got != 1234 {
		t.Fatalf("尾片大小 = %d, want 1234", got)
	}
	// 合并请求里分片必须按序号升序
	if len(m.completed) != 3 {
		t.Fatalf("合并分片数 = %d, want 3", len(m.completed))
	}
	for i, p := range m.completed {
		if p.PartNumber != i+1 {
			t.Fatalf("合并请求分片顺序错误：第 %d 项 PartNumber=%d", i, p.PartNumber)
		}
		if p.ETag != fmt.Sprintf("etag-%d", i+1) {
			t.Fatalf("第 %d 片 ETag = %q", i+1, p.ETag)
		}
	}
	if res.PartCount != 3 || res.Size != int64(len(payload)) {
		t.Fatalf("上传结果 partCount=%d size=%d, want 3/%d", res.PartCount, res.Size, len(payload))
	}
}

func TestMultipartUploadHandlesEmptyAndSmallPayloads(t *testing.T) {
	srv, m := newMockS3Server(t)
	client := newMockClient(t, srv, 2)

	// 空流：仍应产生一个空分片并成功合并（S3 要求至少一片）
	if _, err := client.MultipartUpload(context.Background(), "k/empty.dump", strings.NewReader(""), 0); err != nil {
		t.Fatalf("空对象上传失败: %v", err)
	}
	if len(m.parts) != 1 || len(m.parts[1]) != 0 {
		t.Fatalf("空对象应产生 1 个空分片，实际 %d 片", len(m.parts))
	}

	// 小对象：单片
	small := []byte("hello-backup")
	res, err := client.MultipartUpload(context.Background(), "k/small.dump", bytes.NewReader(small), int64(len(small)))
	if err != nil {
		t.Fatalf("小对象上传失败: %v", err)
	}
	if res.PartCount != 1 {
		t.Fatalf("小对象分片数 = %d, want 1", res.PartCount)
	}
}

func TestPutAndDeleteObject(t *testing.T) {
	srv, m := newMockS3Server(t)
	client := newMockClient(t, srv, 2)

	if err := client.PutObject(context.Background(), ".precheck/probe.txt", []byte("probe"), "text/plain"); err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	if got := string(m.objects[".precheck/probe.txt"]); got != "probe" {
		t.Fatalf("PutObject 内容 = %q", got)
	}
	if err := client.HeadBucket(context.Background()); err != nil {
		t.Fatalf("HeadBucket: %v", err)
	}
	if _, err := client.ListObjects(context.Background(), 10); err != nil {
		t.Fatalf("ListObjects: %v", err)
	}
	if err := client.DeleteObject(context.Background(), ".precheck/probe.txt"); err != nil {
		t.Fatalf("DeleteObject: %v", err)
	}
}

func TestS3ErrorClassification(t *testing.T) {
	sigErr := &S3HTTPError{Status: 403, Code: "SignatureDoesNotMatch", Message: "sig mismatch"}
	if !IsS3SignatureError(sigErr) {
		t.Fatal("SignatureDoesNotMatch 应判为签名类错误")
	}
	deniedErr := &S3HTTPError{Status: 403, Code: "AccessDenied", Message: "denied"}
	if IsS3SignatureError(deniedErr) {
		t.Fatal("AccessDenied 不应判为签名类错误（属权限问题）")
	}
}

func TestS3UploadTuningFromEnv(t *testing.T) {
	partSize, concurrency, maxRetries, storageClass := S3UploadTuningFromEnv(map[string]string{
		"s3_part_size_mb":  "32",
		"s3_concurrency":   "8",
		"s3_max_retries":   "5",
		"s3_storage_class": "IA",
	})
	if partSize != 32<<20 || concurrency != 8 || maxRetries != 5 || storageClass != "IA" {
		t.Fatalf("解析结果 partSize=%d conc=%d retries=%d class=%s", partSize, concurrency, maxRetries, storageClass)
	}
	// 未配置时给出安全默认值
	partSize, concurrency, maxRetries, storageClass = S3UploadTuningFromEnv(nil)
	if partSize != 0 || concurrency != 0 || maxRetries != s3DefaultMaxRetries || storageClass != "" {
		t.Fatalf("默认值不符：partSize=%d conc=%d retries=%d class=%q", partSize, concurrency, maxRetries, storageClass)
	}
}

// TestS3StreamSinkAsBackupStreamSink 验证 S3StreamSink 满足 BackupStreamSink 契约，
// 且"压缩流 → 对象存储 → 回读解压"链路内容无损（等价于零落盘直传的产物完整性）。
func TestS3StreamSinkAsBackupStreamSink(t *testing.T) {
	var _ BackupStreamSink = (*S3StreamSink)(nil)

	srv, _ := newMockS3Server(t)
	client := newMockClient(t, srv, 3)

	partSize := int64(s3MinPartSize)
	w, err := client.NewMultipartWriter(context.Background(), "inst/task/run/backup.sql.gz", partSize, 0)
	if err != nil {
		t.Fatalf("NewMultipartWriter: %v", err)
	}
	sink := NewS3StreamSink(w)

	// 模拟执行器：通过 BackupStreamSink 写 gzip 压缩流
	src := bytes.Repeat([]byte("INSERT INTO t VALUES (1,'goinsight');\n"), 400000) // ~13MB
	var gzBuf bytes.Buffer
	gw := gzip.NewWriter(&gzBuf)
	if _, err := gw.Write(src); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	compressedLen := gzBuf.Len()
	if _, err := io.Copy(sink, &gzBuf); err != nil {
		t.Fatalf("copy to sink: %v", err)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("sink close: %v", err)
	}
	res := sink.Result()
	if res == nil || res.Size != int64(compressedLen) {
		t.Fatalf("上传结果异常: %+v want size=%d", res, compressedLen)
	}

	// 回读并解压，验证与源一致
	var back bytes.Buffer
	if _, err := client.GetObject(context.Background(), "inst/task/run/backup.sql.gz", &back); err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	gr, err := gzip.NewReader(bytes.NewReader(back.Bytes()))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	plain, err := io.ReadAll(gr)
	if err != nil {
		t.Fatalf("gunzip: %v", err)
	}
	if !bytes.Equal(plain, src) {
		t.Fatalf("回读解压内容与源不一致: got %d bytes want %d bytes", len(plain), len(src))
	}
}
