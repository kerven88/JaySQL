package services

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"io"
	"testing"
)

// artifactStreamFakeSink 仅用于验证流式包装产物形态。
type artifactStreamFakeSink struct {
	buf    bytes.Buffer
	closed bool
}

func (f *artifactStreamFakeSink) Write(p []byte) (int, error) { return f.buf.Write(p) }

func (f *artifactStreamFakeSink) Close() error {
	f.closed = true
	return nil
}

func (f *artifactStreamFakeSink) RemoteTarget() string { return "fake://artifact" }

func TestBackupArtifactStreamGzipRoundTrip(t *testing.T) {
	sink := &artifactStreamFakeSink{}
	stream := newBackupArtifactStream(sink, ArtifactCompressionGzip, ArtifactCompressLevelFast)

	payload := "CREATE TABLE t (id int);\nINSERT INTO t VALUES (1);\n"
	writer, err := stream.WriterForEntry("all_databases.sql")
	if err != nil {
		t.Fatalf("WriterForEntry() error = %v", err)
	}
	if _, err := io.WriteString(writer, payload); err != nil {
		t.Fatalf("写入产物失败: %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if !sink.closed {
		t.Fatal("Close() 未关闭 sink：gzip 与 sink 的关闭顺序可能被破坏")
	}

	reader, err := gzip.NewReader(bytes.NewReader(sink.buf.Bytes()))
	if err != nil {
		t.Fatalf("产物不是合法 gzip: %v", err)
	}
	defer reader.Close()
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("解压失败: %v", err)
	}
	if string(got) != payload {
		t.Fatalf("解压内容不一致: got %q want %q", string(got), payload)
	}
	if stream.SizeBytes() != int64(sink.buf.Len()) {
		t.Fatalf("SizeBytes = %d, want %d（应为压缩后字节数）", stream.SizeBytes(), sink.buf.Len())
	}
}

func TestBackupArtifactStreamZipRoundTrip(t *testing.T) {
	sink := &artifactStreamFakeSink{}
	stream := newBackupArtifactStream(sink, ArtifactCompressionZip, "")

	for name, body := range map[string]string{"db1.sql": "sql-1", "db2.sql": "sql-2"} {
		writer, err := stream.WriterForEntry(name)
		if err != nil {
			t.Fatalf("WriterForEntry(%s) error = %v", name, err)
		}
		if _, err := io.WriteString(writer, body); err != nil {
			t.Fatalf("写入 %s 失败: %v", name, err)
		}
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reader, err := zip.NewReader(bytes.NewReader(sink.buf.Bytes()), int64(sink.buf.Len()))
	if err != nil {
		t.Fatalf("产物不是合法 zip: %v", err)
	}
	if len(reader.File) != 2 {
		t.Fatalf("zip 条目数 = %d, want 2", len(reader.File))
	}
	want := map[string]string{"db1.sql": "sql-1", "db2.sql": "sql-2"}
	for _, file := range reader.File {
		rc, err := file.Open()
		if err != nil {
			t.Fatalf("打开条目 %s 失败: %v", file.Name, err)
		}
		content, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("读取条目 %s 失败: %v", file.Name, err)
		}
		if string(content) != want[file.Name] {
			t.Fatalf("条目 %s 内容 = %q, want %q", file.Name, string(content), want[file.Name])
		}
	}
}

func TestBackupArtifactStreamPlainPassThrough(t *testing.T) {
	sink := &artifactStreamFakeSink{}
	stream := newBackupArtifactStream(sink, ArtifactCompressionNone, "")

	payload := "plain-sql-body"
	writer, err := stream.WriterForEntry("all_databases.sql")
	if err != nil {
		t.Fatalf("WriterForEntry() error = %v", err)
	}
	if _, err := io.WriteString(writer, payload); err != nil {
		t.Fatalf("写入产物失败: %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if sink.buf.String() != payload {
		t.Fatalf("明文模式产物 = %q, want %q", sink.buf.String(), payload)
	}
}
