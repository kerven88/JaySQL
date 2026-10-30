package services

import (
	"bytes"
	"context"
	backupModels "github.com/simajiong/jaysql/internal/backup/models"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestS3CompatibleTargetPrecheckAndUpload(t *testing.T) {
	tp, err := normalizeStorageTargetType(" s3_minio ")
	if err != nil {
		t.Fatal(err)
	}
	if isPhase2StorageType(tp) || !IsObjectStorageType(tp) {
		t.Fatal("S3-compatible target is not enabled")
	}
	if !pgBackRestStorageTargetIsS3(&backupModels.InsightBackupStorageTargets{Type: commonModels.EnumType(tp)}) {
		t.Fatal("physical backup rejected S3-compatible repository")
	}
	srv, _ := newMockS3Server(t)
	if err := validateStorageTargetForm(tp, true, "", 0, "", "", "", "", srv.URL, "us-east-1", "demo-bucket", "ak", "sk"); err != nil {
		t.Fatal(err)
	}
	result := (&PrecheckStorageTargetService{}).precheckS3(tp, srv.URL, "us-east-1", "demo-bucket", "ak", "sk", true)
	if !result.OK || !result.S3Capability.PutObjectOK || !result.S3Capability.ListBucketOK || !result.S3Capability.DeleteObjectOK {
		t.Fatalf("precheck: %+v", result)
	}
	client, err := buildStorageTargetObjectClient(&BackupExecutionStorageTarget{Type: tp, S3Endpoint: srv.URL, S3Region: "us-east-1", S3Bucket: "demo-bucket", S3AccessKey: "ak", S3SecretKey: "sk", S3ForcePathStyle: true})
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("backup contents\n")
	localPath := filepath.Join(t.TempDir(), "backup.sql")
	if err := os.WriteFile(localPath, payload, 0600); err != nil {
		t.Fatal(err)
	}
	uploaded, err := client.UploadFile(context.Background(), "instance/task/run/backup.sql", localPath)
	if err != nil {
		t.Fatal(err)
	}
	if uploaded.Size != int64(len(payload)) {
		t.Fatalf("uploaded size = %d", uploaded.Size)
	}
	var got bytes.Buffer
	_, err = client.GetObject(context.Background(), "instance/task/run/backup.sql", &got)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Bytes(), payload) {
		t.Fatalf("uploaded bytes = %q", got.Bytes())
	}
}

func TestAmazonS3UsesVirtualHostAndConfiguredSigningRegion(t *testing.T) {
	var receivedHost, receivedPath, authorization string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHost, receivedPath, authorization = r.Host, r.URL.Path, r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		if !bytes.Equal(body, []byte("backup")) {
			t.Errorf("request body = %q", body)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	client, err := buildStorageTargetObjectClient(&BackupExecutionStorageTarget{Type: "S3_MINIO", S3Endpoint: "http://s3.ap-southeast-1.amazonaws.com", S3Region: "ap-southeast-1", S3Bucket: "demo-bucket", S3AccessKey: "ak", S3SecretKey: "sk"})
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
	}}
	defer transport.CloseIdleConnections()
	client.httpClient = &http.Client{Transport: transport}
	err = client.PutObject(context.Background(), "instance/task/backup.sql", []byte("backup"), "application/octet-stream")
	if err != nil {
		t.Fatal(err)
	}
	if receivedHost != "demo-bucket.s3.ap-southeast-1.amazonaws.com" || receivedPath != "/instance/task/backup.sql" {
		t.Fatalf("address = %s%s", receivedHost, receivedPath)
	}
	if !strings.Contains(authorization, "/ap-southeast-1/s3/aws4_request") {
		t.Fatalf("wrong signing region: %s", authorization)
	}
}
