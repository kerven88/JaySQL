package services

// S3 兼容对象存储客户端（纯标准库实现，AWS Signature V4）。
//
// 设计目标：
//  1. 通用：一份实现覆盖 AWS S3 / 阿里云 OSS / 腾讯云 COS / 华为云 OBS / MinIO
//     以及任意 S3 兼容端点，差异只体现在 endpoint / region / path-style 三个配置上；
//  2. 高性能：大文件走分片上传（Multipart Upload），分片可并发、可重试，
//     并支持把"边产生边上传"的流式写入（零落盘），避免 15GB+ 备份先在本地落盘再搬运；
//  3. 可诊断：所有失败都保留 HTTP 状态码与响应体 <Code>，便于区分
//     「签名/凭据错」与「权限不足」——这两类问题的处置方向完全不同。
//
// 该客户端同时被 storage_target_precheck.go（连通性预检）与 backup-agent（备份上传）复用，
// 保证"测试通过"与"真实上传"走的是同一套签名实现。

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	s3V4SignerAlgorithm = "AWS4-HMAC-SHA256"
	s3ServiceName       = "s3"

	// S3 规定：除最后一片外，每片最小 5MiB；单个对象最多 10000 片。
	s3MinPartSize     = 5 << 20
	s3DefaultPartSize = 16 << 20
	s3MaxParts        = 10000

	s3DefaultConcurrency = 4
	s3DefaultMaxRetries  = 3

	// s3DefaultListPageSize 单次 LIST 请求的对象数上限（S3 协议硬上限 1000）。
	s3DefaultListPageSize = 1000
	// s3DefaultListMaxTotal 列举的累计保护上限。
	// 基线扫描只需要一个任务目录下的 manifest，达到上限即停止翻页，
	// 避免桶内对象极多时把一次备份决策拖成无界循环。
	s3DefaultListMaxTotal = 10000

	// sha256("")，S3 对无 body 请求要求的 x-amz-content-sha256 值。
	s3EmptyPayloadSHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
)

// S3ClientOptions 描述一个 S3 兼容存储端点及其性能参数。
type S3ClientOptions struct {
	Endpoint       string
	Region         string
	Bucket         string
	AccessKey      string
	SecretKey      string
	ForcePathStyle bool

	// PartSize 分片大小（字节）。<=0 时使用 16MiB；小于 5MiB 会被提升到 5MiB。
	PartSize int64
	// Concurrency 并发上传的分片数。<=0 时使用 4，上限 32。
	Concurrency int
	// MaxRetries 单次操作的额外重试次数（网络错误 / 5xx / 429）。<0 视为 0。
	MaxRetries int
	// StorageClass 可选，如 STANDARD / IA / ARCHIVE（不设则用服务端默认）。
	StorageClass string
	// HTTPTimeout 单个 HTTP 请求的总超时（0 表示不限制，靠传输层超时兜底）。
	HTTPTimeout time.Duration
	// UserAgent 自定义 UA。
	UserAgent string

	// OnProgress 上传进度回调（已完成字节数, 总字节数）。仅分片上传路径调用。
	OnProgress func(done, total int64)
}

// S3Client 是线程安全的 S3 兼容客户端。
type S3Client struct {
	scheme         string
	host           string
	bucket         string
	region         string
	accessKey      string
	secretKey      string
	forcePathStyle bool

	partSize     int64
	concurrency  int
	maxRetries   int
	storageClass string
	userAgent    string
	onProgress   func(done, total int64)

	httpClient *http.Client
}

// S3UploadResult 描述一次上传的结果。
type S3UploadResult struct {
	Bucket    string
	Key       string
	ETag      string
	Size      int64
	UploadID  string
	PartCount int
}

// S3HTTPError 保留对象存储返回的原始错误语义。
// Code 是响应体 <Error><Code>，例如 SignatureDoesNotMatch / AccessDenied / NoSuchBucket。
type S3HTTPError struct {
	Status  int
	Code    string
	Message string
	Body    string
}

func (e *S3HTTPError) Error() string {
	if e == nil {
		return ""
	}
	if e.Code != "" {
		return fmt.Sprintf("S3 返回状态码 %d（Code=%s）：%s", e.Status, e.Code, e.Message)
	}
	return fmt.Sprintf("S3 返回状态码 %d：%s", e.Status, strings.TrimSpace(e.Body))
}

// NewS3Client 构造客户端并做启动期校验（快速失败）。
func NewS3Client(opts S3ClientOptions) (*S3Client, error) {
	scheme, host, ok := normalizeEndpoint(opts.Endpoint)
	if !ok {
		return nil, fmt.Errorf("Endpoint 非法：%q（示例：oss-cn-hangzhou.aliyuncs.com 或 http://minio-host:9000）", opts.Endpoint)
	}
	bucket := strings.TrimSpace(opts.Bucket)
	if bucket == "" {
		return nil, fmt.Errorf("Bucket 不能为空")
	}
	if strings.TrimSpace(opts.AccessKey) == "" || strings.TrimSpace(opts.SecretKey) == "" {
		return nil, fmt.Errorf("AccessKey / SecretKey 不能为空")
	}
	region := strings.TrimSpace(opts.Region)
	if region == "" {
		// 少数端点（如部分 MinIO）不校验 region，但签名串必须带一个非空值。
		region = "us-east-1"
	}

	partSize := opts.PartSize
	if partSize <= 0 {
		partSize = s3DefaultPartSize
	}
	if partSize < s3MinPartSize {
		partSize = s3MinPartSize
	}
	concurrency := opts.Concurrency
	if concurrency <= 0 {
		concurrency = s3DefaultConcurrency
	}
	if concurrency > 32 {
		concurrency = 32
	}
	maxRetries := opts.MaxRetries
	if maxRetries < 0 {
		maxRetries = 0
	}
	userAgent := strings.TrimSpace(opts.UserAgent)
	if userAgent == "" {
		userAgent = "goinsight-backup/1.0"
	}

	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   15 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          128,
		MaxIdleConnsPerHost:   concurrency*2 + 4,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ExpectContinueTimeout: 5 * time.Second,
		// 首字节超时：分片上传时能及时发现"连得上但对端不干活"
		ResponseHeaderTimeout: 60 * time.Second,
	}

	return &S3Client{
		scheme:         scheme,
		host:           strings.ToLower(host),
		bucket:         bucket,
		region:         region,
		accessKey:      strings.TrimSpace(opts.AccessKey),
		secretKey:      opts.SecretKey,
		forcePathStyle: opts.ForcePathStyle,
		partSize:       partSize,
		concurrency:    concurrency,
		maxRetries:     maxRetries,
		storageClass:   strings.TrimSpace(opts.StorageClass),
		userAgent:      userAgent,
		onProgress:     opts.OnProgress,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   opts.HTTPTimeout,
		},
	}, nil
}

// Bucket 返回当前桶名。
func (c *S3Client) Bucket() string { return c.bucket }

// Region 返回当前 region。
func (c *S3Client) Region() string { return c.region }

// Endpoint 返回规范化的 host。
func (c *S3Client) Endpoint() string { return c.host }

// ForcePathStyle 返回是否使用 path-style 寻址。
func (c *S3Client) ForcePathStyle() bool { return c.forcePathStyle }

// PartSize 返回分片大小。
func (c *S3Client) PartSize() int64 { return c.partSize }

// Concurrency 返回并发度。
func (c *S3Client) Concurrency() int { return c.concurrency }

// ---- 签名 ----

func s3hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

func s3sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func s3GetSignatureKey(key, dateStamp, regionName, serviceName string) []byte {
	kDate := s3hmacSHA256([]byte("AWS4"+key), dateStamp)
	kRegion := s3hmacSHA256(kDate, regionName)
	kService := s3hmacSHA256(kRegion, serviceName)
	return s3hmacSHA256(kService, "aws4_request")
}

// awsURIEncode 按 AWS SigV4 规范做 RFC3986 编码。
// 关键点：空格必须编码成 %20（不能是 +），未保留字符之外一律大写十六进制，
// 且路径中的 "/" 不参与编码。历史实现用 url.QueryEscape 拼查询串，
// 遇到含空格/加号的 uploadId 会算错签名，这里统一修正。
func awsURIEncode(s string, encodeSlash bool) string {
	var b strings.Builder
	b.Grow(len(s) + 8)
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9'),
			ch == '-', ch == '_', ch == '.', ch == '~':
			b.WriteByte(ch)
		case ch == '/' && !encodeSlash:
			b.WriteByte('/')
		default:
			b.WriteString("%")
			b.WriteString(strings.ToUpper(hex.EncodeToString([]byte{ch})))
		}
	}
	return b.String()
}

// normalizeEndpoint 把用户填写的 endpoint 规范化为 (scheme, host)。
// 允许不带协议（默认 https），允许误带路径（丢弃），host 统一小写。
func normalizeEndpoint(endpoint string) (scheme, host string, ok bool) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return "", "", false
	}
	if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
		endpoint = "https://" + endpoint
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		return "", "", false
	}
	return u.Scheme, strings.ToLower(u.Host), true
}

// canonicalObjectPath 构造规范化对象路径。
//   - path-style：   /<bucket>[/<key>]
//   - 虚拟主机风格： /<key>
//
// 只保留一个 "/" 分隔符——这正是早期预检把 base path 与 key 拼成 "//key"
// 导致 OSS 报 403 SignatureDoesNotMatch 的根源。
func (c *S3Client) canonicalObjectPath(objectKey string) string {
	base := "/"
	if c.forcePathStyle {
		base = "/" + c.bucket
	}
	key := strings.TrimLeft(strings.TrimSpace(objectKey), "/")
	if key == "" {
		return strings.TrimRight(base, "/") + "/"
	}
	return strings.TrimRight(base, "/") + "/" + key
}

// requestHost 返回实际请求的 Host（虚拟主机风格需要把 bucket 前缀拼上）。
func (c *S3Client) requestHost() string {
	if c.forcePathStyle {
		return c.host
	}
	return strings.ToLower(c.bucket) + "." + c.host
}

type s3SignedRequest struct {
	req         *http.Request
	signTime    time.Time
	dateStamp   string
	amzDate     string
	payloadHash string
	host        string
	path        string
}

// buildSignedRequest 构造并完成 SigV4 签名的请求。
// body 为空时 payloadHash 取空串摘要；非空时对 body 计算 sha256。
// extraHeaders 中的头会被纳入签名，调用方后续不得修改。
func (c *S3Client) buildSignedRequest(method, objectKey string, query url.Values, body []byte, extraHeaders map[string]string) (*s3SignedRequest, error) {
	host := c.requestHost()
	path := c.canonicalObjectPath(objectKey)

	target := &url.URL{Scheme: c.scheme, Host: host, Path: path}
	if len(query) > 0 {
		target.RawQuery = encodeCanonicalQuery(query)
	}

	var reader io.Reader
	if len(body) > 0 {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, target.String(), reader)
	if err != nil {
		return nil, err
	}

	payloadHash := s3EmptyPayloadSHA256
	if len(body) > 0 {
		payloadHash = s3sha256Hex(body)
	}
	sr := &s3SignedRequest{
		req:         req,
		signTime:    time.Now().UTC(),
		payloadHash: payloadHash,
		host:        host,
		path:        path,
	}
	sr.dateStamp = sr.signTime.Format("20060102")
	sr.amzDate = sr.signTime.Format("20060102T150405Z")

	req.Header.Set("Host", host)
	req.Header.Set("x-amz-content-sha256", payloadHash)
	req.Header.Set("x-amz-date", sr.amzDate)
	req.Header.Set("User-Agent", c.userAgent)
	if len(body) > 0 {
		req.ContentLength = int64(len(body))
	}
	if c.storageClass != "" && (method == http.MethodPut || method == http.MethodPost) {
		req.Header.Set("x-amz-storage-class", c.storageClass)
	}
	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}

	c.signRequest(sr)
	return sr, nil
}

// encodeCanonicalQuery 生成规范化查询串（按 key 升序、RFC3986 编码）。
func encodeCanonicalQuery(query url.Values) string {
	keys := make([]string, 0, len(query))
	for k := range query {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		vals := query[k]
		if len(vals) == 0 {
			parts = append(parts, awsURIEncode(k, true)+"=")
			continue
		}
		// 同一 key 多个值按字典序排列
		sortedVals := append([]string(nil), vals...)
		sort.Strings(sortedVals)
		for _, v := range sortedVals {
			parts = append(parts, awsURIEncode(k, true)+"="+awsURIEncode(v, true))
		}
	}
	return strings.Join(parts, "&")
}

func (c *S3Client) signRequest(sr *s3SignedRequest) {
	req := sr.req

	headerKeys := make([]string, 0, len(req.Header))
	headerVals := make(map[string]string, len(req.Header))
	for k, vals := range req.Header {
		if len(vals) == 0 {
			continue
		}
		lk := strings.ToLower(k)
		headerVals[lk] = strings.Join(vals, ",")
		headerKeys = append(headerKeys, lk)
	}
	sort.Strings(headerKeys)

	var canonHdr strings.Builder
	for _, k := range headerKeys {
		canonHdr.WriteString(k)
		canonHdr.WriteString(":")
		canonHdr.WriteString(strings.TrimSpace(headerVals[k]))
		canonHdr.WriteString("\n")
	}
	signedHeaders := strings.Join(headerKeys, ";")

	canonicalRequest := strings.Join([]string{
		req.Method,
		awsURIEncode(sr.path, false),
		encodeCanonicalQuery(req.URL.Query()),
		canonHdr.String(),
		signedHeaders,
		sr.payloadHash,
	}, "\n")

	credentialScope := sr.dateStamp + "/" + c.region + "/" + s3ServiceName + "/aws4_request"
	stringToSign := strings.Join([]string{
		s3V4SignerAlgorithm,
		sr.amzDate,
		credentialScope,
		s3sha256Hex([]byte(canonicalRequest)),
	}, "\n")

	signKey := s3GetSignatureKey(c.secretKey, sr.dateStamp, c.region, s3ServiceName)
	signature := hex.EncodeToString(s3hmacSHA256(signKey, stringToSign))

	req.Header.Set("Authorization", fmt.Sprintf(
		"%s Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		s3V4SignerAlgorithm, c.accessKey, credentialScope, signedHeaders, signature,
	))
}

// ---- 底层请求 ----

const s3ResponseBodyLimit = 256 << 10

// doWithHeaders 执行一次签名请求，返回状态码、响应头与响应体（截断到 256KB）。
// 失败按策略重试（网络错误 / 5xx / 429）。状态码 >= 400 不视为 Go error，
// 以便调用方按 <Code> 精确分类；只有传输层失败或重试耗尽才返回 error。
func (c *S3Client) doWithHeaders(ctx context.Context, method, objectKey string, query url.Values, body []byte, extraHeaders map[string]string) (int, http.Header, []byte, error) {
	var (
		lastErr  error
		status   int
		headers  http.Header
		respBody []byte
	)
	attempts := c.maxRetries + 1
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return 0, nil, nil, ctx.Err()
			case <-time.After(s3RetryBackoff(attempt)):
			}
		}
		sr, err := c.buildSignedRequest(method, objectKey, query, body, extraHeaders)
		if err != nil {
			return 0, nil, nil, err
		}
		if ctx != nil {
			sr.req = sr.req.WithContext(ctx)
		}
		resp, err := c.httpClient.Do(sr.req)
		if err != nil {
			lastErr = err
			if ctx != nil && ctx.Err() != nil {
				return 0, nil, nil, ctx.Err()
			}
			continue
		}
		respBody, _ = io.ReadAll(io.LimitReader(resp.Body, s3ResponseBodyLimit))
		_ = resp.Body.Close()
		status = resp.StatusCode
		headers = resp.Header
		if status >= 500 || status == http.StatusTooManyRequests {
			lastErr = s3HTTPErrorFromResponse(status, respBody)
			continue
		}
		return status, headers, respBody, nil
	}
	if lastErr == nil {
		lastErr = errors.New("S3 请求失败")
	}
	return status, headers, respBody, lastErr
}

// do 是 doWithHeaders 的简化包装，忽略响应头。
func (c *S3Client) do(ctx context.Context, method, objectKey string, query url.Values, body []byte, extraHeaders map[string]string) (int, []byte, error) {
	status, _, respBody, err := c.doWithHeaders(ctx, method, objectKey, query, body, extraHeaders)
	return status, respBody, err
}

func s3RetryBackoff(attempt int) time.Duration {
	// 200ms, 400ms, 800ms, 1.6s … 上限 8s
	d := time.Duration(200*(1<<uint(attempt-1))) * time.Millisecond
	if d > 8*time.Second {
		d = 8 * time.Second
	}
	return d
}

func s3HTTPErrorFromResponse(status int, body []byte) *S3HTTPError {
	trimmed := strings.TrimSpace(string(body))
	code := ""
	message := ""
	var parsed s3ErrorEnvelope
	if xmlErr := xml.Unmarshal(body, &parsed); xmlErr == nil {
		code = strings.TrimSpace(parsed.Code)
		message = strings.TrimSpace(parsed.Message)
	}
	if len(trimmed) > 600 {
		trimmed = trimmed[:600]
	}
	if message == "" {
		message = trimmed
	}
	return &S3HTTPError{Status: status, Code: code, Message: message, Body: trimmed}
}

type s3ErrorEnvelope struct {
	XMLName   xml.Name `xml:"Error"`
	Code      string   `xml:"Code"`
	Message   string   `xml:"Message"`
	RequestID string   `xml:"RequestId"`
}

// isS3SignatureErrorCode 判断错误码是否属于"签名/凭据/时钟"这一类，
// 而不是"权限不足"。两者处置方向完全不同，不能混为一谈。
func isS3SignatureErrorCode(code string) bool {
	switch strings.TrimSpace(code) {
	case "SignatureDoesNotMatch",
		"InvalidAccessKeyId",
		"IncompleteSignature",
		"RequestTimeTooSkewed",
		"InvalidSecurity",
		"MissingSecurityHeader",
		"AuthorizationHeaderMalformed",
		"InvalidRequest":
		return true
	}
	return false
}

// IsS3SignatureError 供上层判断 error 是否为签名类错误。
func IsS3SignatureError(err error) bool {
	var httpErr *S3HTTPError
	if errors.As(err, &httpErr) {
		return isS3SignatureErrorCode(httpErr.Code)
	}
	return false
}

// ---- 基础对象操作 ----

// DownloadObject 把对象存储中的指定对象下载到本地文件。
// 采用流式写入，避免大备份文件（数 GB）占用内存。返回下载字节数。
func (c *S3Client) DownloadObject(ctx context.Context, objectKey, localPath string) (int64, error) {
	attempts := c.maxRetries + 1
	if attempts <= 0 {
		attempts = 1
	}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return 0, ctx.Err()
			case <-time.After(s3RetryBackoff(attempt)):
			}
		}
		sr, err := c.buildSignedRequest(http.MethodGet, objectKey, nil, nil, nil)
		if err != nil {
			return 0, err
		}
		req := sr.req
		if ctx != nil {
			req = req.WithContext(ctx)
		}
		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = err
			if ctx != nil && ctx.Err() != nil {
				return 0, ctx.Err()
			}
			continue
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, s3ResponseBodyLimit))
			return 0, s3HTTPErrorFromResponse(resp.StatusCode, body)
		}
		if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
			return 0, fmt.Errorf("创建下载目录失败: %w", err)
		}
		f, err := os.Create(localPath)
		if err != nil {
			return 0, fmt.Errorf("创建本地文件失败: %w", err)
		}
		n, copyErr := io.Copy(f, resp.Body)
		if closeErr := f.Close(); closeErr != nil && copyErr == nil {
			copyErr = closeErr
		}
		if copyErr != nil {
			_ = os.Remove(localPath)
			lastErr = copyErr
			if ctx != nil && ctx.Err() != nil {
				return 0, ctx.Err()
			}
			continue
		}
		return n, nil
	}
	if lastErr == nil {
		lastErr = errors.New("S3 下载失败")
	}
	return 0, lastErr
}

// HeadBucket 探测桶是否可达（200 = 有权限访问该桶）。
func (c *S3Client) HeadBucket(ctx context.Context) error {
	status, body, err := c.do(ctx, http.MethodHead, "", nil, nil, nil)
	if err != nil {
		return err
	}
	if status == http.StatusOK {
		return nil
	}
	return s3HTTPErrorFromResponse(status, body)
}

// ListObjects 列出桶内对象（最多 maxKeys 个），返回对象键与存储类型样本。
func (c *S3Client) ListObjects(ctx context.Context, maxKeys int) ([]S3ObjectInfo, error) {
	if maxKeys <= 0 {
		maxKeys = 10
	}
	q := url.Values{}
	q.Set("list-type", "2")
	q.Set("max-keys", strconv.Itoa(maxKeys))
	status, body, err := c.do(ctx, http.MethodGet, "", q, nil, nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, s3HTTPErrorFromResponse(status, body)
	}
	var parsed s3ListBucketResult
	if xmlErr := xml.Unmarshal(body, &parsed); xmlErr != nil {
		return nil, fmt.Errorf("解析 ListObjects 响应失败: %w", xmlErr)
	}
	out := make([]S3ObjectInfo, 0, len(parsed.Contents))
	for _, item := range parsed.Contents {
		out = append(out, S3ObjectInfo{
			Key:          item.Key,
			Size:         item.Size,
			ETag:         strings.Trim(item.ETag, "\""),
			StorageClass: item.StorageClass,
			LastModified: item.LastModified,
		})
	}
	return out, nil
}

// S3ObjectInfo 是列表结果中的单条对象信息。
type S3ObjectInfo struct {
	Key          string
	Size         int64
	ETag         string
	StorageClass string
	LastModified string
}

// ListObjectsWithPrefix 按前缀列举对象，自动翻页直到取完或触及 maxTotal 上限。
//
// 与 ListObjects 的区别：
//   - 支持 prefix（对象存储没有目录，前缀是唯一的"目录"表达方式）；
//   - 支持 continuation-token 翻页，不会只拿到第一页就返回；
//   - 空结果是正常语义，返回空切片而非 error —— 调用方据此区分「从未备份」与「列举失败」。
//
// prefix 允许带或不带前导斜杠（统一去掉，S3 key 本身不以斜杠开头）。
// pageSize<=0 取 1000，>1000 截断到协议上限；maxTotal<=0 取 10000 作为保护性上限。
func (c *S3Client) ListObjectsWithPrefix(ctx context.Context, prefix string, pageSize int, maxTotal int) ([]S3ObjectInfo, error) {
	if pageSize <= 0 {
		pageSize = s3DefaultListPageSize
	}
	if pageSize > s3DefaultListPageSize {
		pageSize = s3DefaultListPageSize
	}
	if maxTotal <= 0 {
		maxTotal = s3DefaultListMaxTotal
	}
	normalizedPrefix := strings.TrimPrefix(strings.TrimSpace(prefix), "/")

	out := make([]S3ObjectInfo, 0, pageSize)
	token := ""
	for {
		q := url.Values{}
		q.Set("list-type", "2")
		q.Set("max-keys", strconv.Itoa(pageSize))
		if normalizedPrefix != "" {
			q.Set("prefix", normalizedPrefix)
		}
		if token != "" {
			q.Set("continuation-token", token)
		}
		status, body, err := c.do(ctx, http.MethodGet, "", q, nil, nil)
		if err != nil {
			return nil, err
		}
		if status != http.StatusOK {
			return nil, s3HTTPErrorFromResponse(status, body)
		}
		var parsed s3ListBucketResult
		if xmlErr := xml.Unmarshal(body, &parsed); xmlErr != nil {
			return nil, fmt.Errorf("解析 ListObjects 响应失败: %w", xmlErr)
		}
		for _, item := range parsed.Contents {
			out = append(out, S3ObjectInfo{
				Key:          item.Key,
				Size:         item.Size,
				ETag:         strings.Trim(item.ETag, "\""),
				StorageClass: item.StorageClass,
				LastModified: item.LastModified,
			})
		}
		if !parsed.IsTruncated || strings.TrimSpace(parsed.NextContinuationToken) == "" {
			return out, nil
		}
		if len(out) >= maxTotal {
			return out, nil
		}
		token = strings.TrimSpace(parsed.NextContinuationToken)
	}
}

// PutObject 上传一个完整对象（小文件 / 预检探针用）。大文件请用 MultipartUpload。
func (c *S3Client) PutObject(ctx context.Context, objectKey string, body []byte, contentType string) error {
	headers := map[string]string{}
	if strings.TrimSpace(contentType) != "" {
		headers["Content-Type"] = contentType
	}
	status, respBody, err := c.do(ctx, http.MethodPut, objectKey, nil, body, headers)
	if err != nil {
		return err
	}
	if status == http.StatusOK || status == http.StatusCreated {
		return nil
	}
	return s3HTTPErrorFromResponse(status, respBody)
}

// GetObject 下载对象到 w，返回写入字节数。
func (c *S3Client) GetObject(ctx context.Context, objectKey string, w io.Writer) (int64, error) {
	sr, err := c.buildSignedRequest(http.MethodGet, objectKey, nil, nil, nil)
	if err != nil {
		return 0, err
	}
	req := sr.req
	if ctx != nil {
		req = req.WithContext(ctx)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, s3ResponseBodyLimit))
		return 0, s3HTTPErrorFromResponse(resp.StatusCode, body)
	}
	return io.Copy(w, resp.Body)
}

// DeleteObject 删除对象。404 视为已删除（幂等）。
func (c *S3Client) DeleteObject(ctx context.Context, objectKey string) error {
	status, body, err := c.do(ctx, http.MethodDelete, objectKey, nil, nil, nil)
	if err != nil {
		return err
	}
	if status == http.StatusOK || status == http.StatusNoContent || status == http.StatusNotFound {
		return nil
	}
	return s3HTTPErrorFromResponse(status, body)
}

// ---- 分片上传 ----

type s3ListBucketResult struct {
	XMLName  xml.Name `xml:"ListBucketResult"`
	KeyCount int      `xml:"KeyCount"`
	// IsTruncated / NextContinuationToken 是 ListObjectsV2 的翻页依据。
	// 缺任一个都视为已取完，避免服务端不返回该字段时陷入死循环。
	IsTruncated           bool   `xml:"IsTruncated"`
	NextContinuationToken string `xml:"NextContinuationToken"`
	Contents              []struct {
		Key          string `xml:"Key"`
		LastModified string `xml:"LastModified"`
		ETag         string `xml:"ETag"`
		Size         int64  `xml:"Size"`
		StorageClass string `xml:"StorageClass"`
	} `xml:"Contents"`
}

type s3InitiateMultipartUploadResult struct {
	XMLName  xml.Name `xml:"InitiateMultipartUploadResult"`
	Bucket   string   `xml:"Bucket"`
	Key      string   `xml:"Key"`
	UploadID string   `xml:"UploadId"`
}

type s3CompleteMultipartUploadPayload struct {
	XMLName xml.Name             `xml:"CompleteMultipartUpload"`
	Parts   []s3CompletedPartXML `xml:"Part"`
}

type s3CompletedPartXML struct {
	PartNumber int    `xml:"PartNumber"`
	ETag       string `xml:"ETag"`
}

type s3CompleteMultipartUploadResult struct {
	XMLName  xml.Name `xml:"CompleteMultipartUploadResult"`
	Location string   `xml:"Location"`
	Bucket   string   `xml:"Bucket"`
	Key      string   `xml:"Key"`
	ETag     string   `xml:"ETag"`
}

type s3UploadedPart struct {
	number int
	etag   string
}

// calcPartSize 依据对象总大小选分片大小：
// 保证片数不超过 1 万片，且不小于用户配置值与 5MiB 下限。
func (c *S3Client) calcPartSize(totalSize int64) int64 {
	partSize := c.partSize
	if totalSize > 0 {
		// 向上取整到 MiB，避免除数精度问题
		needed := (totalSize + s3MaxParts - 1) / s3MaxParts
		needed = ((needed + (1 << 20) - 1) / (1 << 20)) << 20
		if needed > partSize {
			partSize = needed
		}
	}
	if partSize < s3MinPartSize {
		partSize = s3MinPartSize
	}
	return partSize
}

// MultipartUpload 把 reader 的内容以分片方式并发上传到 objectKey。
// totalSize <= 0 时按未知长度处理（按配置分片大小切分）。
func (c *S3Client) MultipartUpload(ctx context.Context, objectKey string, reader io.Reader, totalSize int64) (*S3UploadResult, error) {
	w, err := c.NewMultipartWriter(ctx, objectKey, c.calcPartSize(totalSize), totalSize)
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(w, reader); err != nil {
		_ = w.Abort()
		return nil, err
	}
	return w.Close()
}

// S3MultipartWriter 实现了 io.WriteCloser：写入的字节会被切成若干分片并发上传，
// Close 时补齐最后一片并完成合并。它同时被用作"零落盘直传"的 StreamSink，
// 因此大备份可以一边由导出工具产生、一边并发传至对象存储，本地无需完整落盘。
type S3MultipartWriter struct {
	client    *S3Client
	ctx       context.Context
	objectKey string

	partSize  int64
	totalSize int64

	uploadID string

	mu        sync.Mutex
	buf       []byte
	partNum   int
	parts     []s3UploadedPart
	written   int64
	failedErr error
	closed    bool
	started   bool

	sem chan struct{}
	wg  sync.WaitGroup
}

// NewMultipartWriter 创建流式分片写入器（惰性创建 multipart upload，首次写入时才发起）。
// partSize <= 0 时使用客户端配置的分片大小。
func (c *S3Client) NewMultipartWriter(ctx context.Context, objectKey string, partSize int64, totalSize int64) (*S3MultipartWriter, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	key := strings.TrimLeft(strings.TrimSpace(objectKey), "/")
	if key == "" {
		return nil, fmt.Errorf("对象 Key 不能为空")
	}
	if partSize <= 0 {
		partSize = c.partSize
	}
	if partSize < s3MinPartSize {
		partSize = s3MinPartSize
	}
	return &S3MultipartWriter{
		client:    c,
		ctx:       ctx,
		objectKey: key,
		partSize:  partSize,
		totalSize: totalSize,
		sem:       make(chan struct{}, c.concurrency),
		buf:       make([]byte, 0, partSize),
	}, nil
}

// RemoteTarget 返回人类可读的目标标识（满足 BackupStreamSink 接口）。
func (w *S3MultipartWriter) RemoteTarget() string {
	return fmt.Sprintf("s3://%s/%s", w.client.bucket, w.objectKey)
}

func (w *S3MultipartWriter) Write(p []byte) (int, error) {
	if w == nil {
		return 0, fmt.Errorf("writer 为空")
	}
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return 0, fmt.Errorf("分片写入器已关闭")
	}
	if w.failedErr != nil {
		err := w.failedErr
		w.mu.Unlock()
		return 0, err
	}
	w.mu.Unlock()

	accepted := 0
	for len(p) > 0 {
		w.mu.Lock()
		remaining := int(w.partSize) - len(w.buf)
		w.mu.Unlock()
		if remaining <= 0 {
			if err := w.flushFullPartWhenReady(); err != nil {
				return accepted, err
			}
			continue
		}
		chunk := p
		if len(chunk) > remaining {
			chunk = chunk[:remaining]
		}
		w.mu.Lock()
		w.buf = append(w.buf, chunk...)
		w.written += int64(len(chunk))
		full := int64(len(w.buf)) >= w.partSize
		w.mu.Unlock()
		p = p[len(chunk):]
		accepted += len(chunk)
		if full {
			if err := w.flushFullPartWhenReady(); err != nil {
				return accepted, err
			}
		}
	}
	w.reportProgress()
	return accepted, nil
}

// flushFullPartWhenReady 在还有空闲并发额度时把当前满片交给后台上传；
// 额度耗尽则阻塞等待，形成对上游的自然背压（避免内存无限增长）。
func (w *S3MultipartWriter) flushFullPartWhenReady() error {
	// 必须在派发分片前确保 multipart upload 会话已创建，
	// 否则后台上传会拿到空 uploadId。
	if err := w.ensureStarted(); err != nil {
		return err
	}
	w.mu.Lock()
	if len(w.buf) < int(w.partSize) {
		w.mu.Unlock()
		return nil
	}
	if w.failedErr != nil {
		err := w.failedErr
		w.mu.Unlock()
		return err
	}
	data := make([]byte, len(w.buf))
	copy(data, w.buf)
	w.buf = w.buf[:0]
	w.partNum++
	number := w.partNum
	w.mu.Unlock()

	select {
	case w.sem <- struct{}{}:
	case <-w.ctx.Done():
		return w.ctx.Err()
	}
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		defer func() { <-w.sem }()
		w.uploadPart(number, data)
	}()
	return w.currentErr()
}

func (w *S3MultipartWriter) uploadPart(number int, data []byte) {
	etag, err := w.client.uploadPart(w.ctx, w.objectKey, w.uploadID, number, data)
	if err != nil {
		w.mu.Lock()
		if w.failedErr == nil {
			w.failedErr = fmt.Errorf("上传第 %d 片失败: %w", number, err)
		}
		w.mu.Unlock()
		return
	}
	w.mu.Lock()
	w.parts = append(w.parts, s3UploadedPart{number: number, etag: etag})
	w.mu.Unlock()
	w.reportProgress()
}

func (w *S3MultipartWriter) reportProgress() {
	if w == nil || w.client == nil || w.client.onProgress == nil {
		return
	}
	w.mu.Lock()
	done := w.written
	total := w.totalSize
	w.mu.Unlock()
	w.client.onProgress(done, total)
}

func (w *S3MultipartWriter) currentErr() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.failedErr
}

// ensureStarted 在真正需要上传时创建 multipart upload 会话。
// 调用方需持有 w.mu 之外的语境（内部自行加锁）。
func (w *S3MultipartWriter) ensureStarted() error {
	w.mu.Lock()
	if w.started {
		w.mu.Unlock()
		return nil
	}
	w.mu.Unlock()

	uploadID, err := w.client.createMultipartUpload(w.ctx, w.objectKey)
	if err != nil {
		w.mu.Lock()
		if w.failedErr == nil {
			w.failedErr = err
		}
		w.mu.Unlock()
		return err
	}
	w.mu.Lock()
	w.uploadID = uploadID
	w.started = true
	w.mu.Unlock()
	return nil
}

// Close 结束写入：补齐最后一片、等待全部并发分片完成、合并对象。
func (w *S3MultipartWriter) Close() (*S3UploadResult, error) {
	if w == nil {
		return nil, nil
	}
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil, nil
	}
	w.closed = true
	w.mu.Unlock()

	if err := w.ensureStarted(); err != nil {
		return nil, err
	}

	// 上传尾片（可小于 5MiB）。若整体为空，则上传一个空分片，保证至少一片。
	w.mu.Lock()
	tail := make([]byte, len(w.buf))
	copy(tail, w.buf)
	w.buf = w.buf[:0]
	needTail := len(tail) > 0 || w.partNum == 0
	if needTail {
		w.partNum++
	}
	tailNumber := w.partNum
	w.mu.Unlock()

	if needTail {
		select {
		case w.sem <- struct{}{}:
		case <-w.ctx.Done():
			_ = w.Abort()
			return nil, w.ctx.Err()
		}
		w.wg.Add(1)
		go func() {
			defer w.wg.Done()
			defer func() { <-w.sem }()
			w.uploadPart(tailNumber, tail)
		}()
	}

	w.wg.Wait()

	if err := w.currentErr(); err != nil {
		_ = w.Abort()
		return nil, err
	}

	w.mu.Lock()
	parts := append([]s3UploadedPart(nil), w.parts...)
	total := w.written
	w.mu.Unlock()
	sort.Slice(parts, func(i, j int) bool { return parts[i].number < parts[j].number })

	etag, err := w.client.completeMultipartUpload(w.ctx, w.objectKey, w.uploadID, parts)
	if err != nil {
		_ = w.Abort()
		return nil, err
	}
	return &S3UploadResult{
		Bucket:    w.client.bucket,
		Key:       w.objectKey,
		ETag:      strings.Trim(etag, "\""),
		Size:      total,
		UploadID:  w.uploadID,
		PartCount: len(parts),
	}, nil
}

// Abort 放弃上传并清理已上传分片；用于失败回滚，避免留下计费的分片残留。
func (w *S3MultipartWriter) Abort() error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	uploadID := w.uploadID
	started := w.started
	w.mu.Unlock()
	if !started || uploadID == "" {
		return nil
	}
	return w.client.abortMultipartUpload(w.ctx, w.objectKey, uploadID)
}

func (c *S3Client) createMultipartUpload(ctx context.Context, objectKey string) (string, error) {
	q := url.Values{}
	q.Set("uploads", "")
	headers := map[string]string{"Content-Type": "application/octet-stream"}
	status, body, err := c.do(ctx, http.MethodPost, objectKey, q, nil, headers)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", s3HTTPErrorFromResponse(status, body)
	}
	var parsed s3InitiateMultipartUploadResult
	if xmlErr := xml.Unmarshal(body, &parsed); xmlErr != nil {
		return "", fmt.Errorf("解析 InitiateMultipartUpload 响应失败: %w", xmlErr)
	}
	if strings.TrimSpace(parsed.UploadID) == "" {
		return "", fmt.Errorf("InitiateMultipartUpload 未返回 UploadId")
	}
	return parsed.UploadID, nil
}

// uploadPart 上传单个分片并返回其 ETag（ETag 在响应头中，必须单独读取）。
func (c *S3Client) uploadPart(ctx context.Context, objectKey, uploadID string, partNumber int, data []byte) (string, error) {
	q := url.Values{}
	q.Set("partNumber", strconv.Itoa(partNumber))
	q.Set("uploadId", uploadID)
	status, headers, body, err := c.doWithHeaders(ctx, http.MethodPut, objectKey, q, data, nil)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", s3HTTPErrorFromResponse(status, body)
	}
	etag := strings.Trim(strings.TrimSpace(headers.Get("ETag")), "\"")
	if etag == "" {
		// 少数兼容实现把 ETag 放在响应体里，兜底再解析一次。
		var parsed s3CompleteMultipartUploadResult
		if xmlErr := xml.Unmarshal(body, &parsed); xmlErr == nil {
			etag = strings.Trim(strings.TrimSpace(parsed.ETag), "\"")
		}
	}
	if etag == "" {
		return "", fmt.Errorf("第 %d 片上传成功但未返回 ETag，无法完成分片合并", partNumber)
	}
	return etag, nil
}

// completeMultipartUpload 合并分片。
func (c *S3Client) completeMultipartUpload(ctx context.Context, objectKey, uploadID string, parts []s3UploadedPart) (string, error) {
	payload := s3CompleteMultipartUploadPayload{Parts: make([]s3CompletedPartXML, 0, len(parts))}
	for _, p := range parts {
		payload.Parts = append(payload.Parts, s3CompletedPartXML{PartNumber: p.number, ETag: p.etag})
	}
	buf, err := xml.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("序列化 CompleteMultipartUpload 请求失败: %w", err)
	}
	q := url.Values{}
	q.Set("uploadId", uploadID)
	status, body, doErr := c.do(ctx, http.MethodPost, objectKey, q, buf, nil)
	if doErr != nil {
		return "", doErr
	}
	if status != http.StatusOK {
		return "", s3HTTPErrorFromResponse(status, body)
	}
	var parsed s3CompleteMultipartUploadResult
	if xmlErr := xml.Unmarshal(body, &parsed); xmlErr != nil {
		return "", nil
	}
	return parsed.ETag, nil
}

func (c *S3Client) abortMultipartUpload(ctx context.Context, objectKey, uploadID string) error {
	q := url.Values{}
	q.Set("uploadId", uploadID)
	status, body, err := c.do(ctx, http.MethodDelete, objectKey, q, nil, nil)
	if err != nil {
		return err
	}
	if status == http.StatusNoContent || status == http.StatusOK || status == http.StatusNotFound {
		return nil
	}
	return s3HTTPErrorFromResponse(status, body)
}

// UploadFile 上传本地文件（大文件自动走并发分片）。
// 这是 agent 在"本地落盘后再上传"路径上的入口。
func (c *S3Client) UploadFile(ctx context.Context, objectKey, localPath string) (*S3UploadResult, error) {
	f, err := os.Open(localPath)
	if err != nil {
		return nil, fmt.Errorf("打开待上传文件失败: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("读取待上传文件信息失败: %w", err)
	}
	return c.MultipartUpload(ctx, objectKey, f, info.Size())
}

// S3StreamSink 把 S3MultipartWriter 适配为 BackupStreamSink（Close() error）。
// 供"零落盘直传"使用：导出工具产生的字节流被直接分片并发写入对象存储。
type S3StreamSink struct {
	writer *S3MultipartWriter
	result *S3UploadResult
}

// NewS3StreamSink 基于分片写入器构造流式 sink。
func NewS3StreamSink(w *S3MultipartWriter) *S3StreamSink {
	return &S3StreamSink{writer: w}
}

// Write 写入字节流（内部按分片切分并后台上传）。
func (s *S3StreamSink) Write(p []byte) (int, error) {
	if s == nil || s.writer == nil {
		return 0, fmt.Errorf("S3 sink 未初始化")
	}
	return s.writer.Write(p)
}

// Close 完成分片合并。
func (s *S3StreamSink) Close() error {
	if s == nil || s.writer == nil {
		return nil
	}
	res, err := s.writer.Close()
	if err != nil {
		return err
	}
	s.result = res
	return nil
}

// RemoteTarget 满足 BackupStreamSink 接口。
func (s *S3StreamSink) RemoteTarget() string {
	if s == nil || s.writer == nil {
		return ""
	}
	return s.writer.RemoteTarget()
}

// Abort 放弃上传并清理已上传分片。
func (s *S3StreamSink) Abort() error {
	if s == nil || s.writer == nil {
		return nil
	}
	return s.writer.Abort()
}

// Result 返回完成后的上传结果（Close 之后可用）。
func (s *S3StreamSink) Result() *S3UploadResult {
	if s == nil {
		return nil
	}
	return s.result
}

// ---- 上传参数解析（供 agent 与预检共用） ----

// S3UploadTuningFromEnv 从 custom_env / 环境变量解析上传调优参数。
// 支持的键（custom_env 优先，其次同名大写环境变量）：
//
//	s3_part_size_mb  分片大小（MiB），默认 16
//	s3_concurrency   并发分片数，默认 4
//	s3_max_retries   单片重试次数，默认 3
//	s3_storage_class 存储类型（STANDARD/IA/ARCHIVE…）
func S3UploadTuningFromEnv(customEnv map[string]string) (partSize int64, concurrency int, maxRetries int, storageClass string) {
	lookup := func(key string) string {
		if customEnv != nil {
			if v, ok := customEnv[key]; ok && strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
			if v, ok := customEnv[strings.ToUpper(key)]; ok && strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		}
		return strings.TrimSpace(os.Getenv(strings.ToUpper(key)))
	}
	partBytes := int64(0)
	if raw := lookup("s3_part_size_mb"); raw != "" {
		if mb, err := strconv.ParseInt(raw, 10, 64); err == nil && mb > 0 {
			partBytes = mb << 20
		}
	}
	concurrency = 0
	if raw := lookup("s3_concurrency"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			concurrency = n
		}
	}
	maxRetries = -1
	if raw := lookup("s3_max_retries"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 0 {
			maxRetries = n
		}
	}
	if maxRetries < 0 {
		maxRetries = s3DefaultMaxRetries
	}
	storageClass = lookup("s3_storage_class")
	return partBytes, concurrency, maxRetries, storageClass
}

// IsObjectStorageType 判断存储目标类型是否为 S3 兼容对象存储。
// 对象存储上传链路覆盖全部 S3 兼容端点（含 MinIO / 华为 OBS）。
func IsObjectStorageType(t string) bool {
	switch strings.ToUpper(strings.TrimSpace(t)) {
	case "OSS_ALIYUN", "COS_TENCENT", "S3_MINIO", "OBS_HUAWEI":
		return true
	}
	return false
}
