package services

import (
	"archive/zip"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestInspectMySQLRestoreZipSourcesFiltersSelectedDatabases(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	zipPath := filepath.Join(dir, "mysql.zip")
	file, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("create zip: %v", err)
	}
	zw := zip.NewWriter(file)
	for _, name := range []string{"db_a.sql", "db_b.sql.gz", "notes.txt"} {
		w, createErr := zw.Create(name)
		if createErr != nil {
			t.Fatalf("create entry %s: %v", name, createErr)
		}
		if _, writeErr := w.Write([]byte("stub")); writeErr != nil {
			t.Fatalf("write entry %s: %v", name, writeErr)
		}
	}
	if closeErr := zw.Close(); closeErr != nil {
		t.Fatalf("close zip writer: %v", closeErr)
	}
	if closeErr := file.Close(); closeErr != nil {
		t.Fatalf("close zip file: %v", closeErr)
	}

	plans, err := inspectMySQLRestoreZipSources(zipPath, logicalRestoreRunOptions{
		SelectedDatabases: []string{"db_b"},
		TargetDBSuffix:    "_restore",
	})
	if err != nil {
		t.Fatalf("inspect zip: %v", err)
	}
	if len(plans) != 1 {
		t.Fatalf("expected 1 plan, got %d", len(plans))
	}
	if plans[0].SourceObjectName != "db_b" {
		t.Fatalf("unexpected source object: %#v", plans[0])
	}
	if plans[0].TargetObjectName != "db_b_restore" {
		t.Fatalf("unexpected target object: %#v", plans[0])
	}
	if plans[0].Format != "sql.gz" {
		t.Fatalf("unexpected format: %#v", plans[0])
	}
}

func TestSourceHigherThanTarget(t *testing.T) {
	t.Parallel()

	if !sourceHigherThanTarget("16.2", "15.7", "PostgreSQL") {
		t.Fatalf("expected postgres source version to be higher")
	}
	if !sourceHigherThanTarget("8.0.36", "8.0.35", "MySQL") {
		t.Fatalf("expected mysql source version to be higher")
	}
	if sourceHigherThanTarget("8.0.35", "8.0.36", "MySQL") {
		t.Fatalf("did not expect mysql source version to be higher")
	}
}

func TestValidateMySQLDumpFileGzip(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	gzipPath := filepath.Join(dir, "dump.sql.gz")
	file, err := os.Create(gzipPath)
	if err != nil {
		t.Fatalf("create gzip file: %v", err)
	}
	gz := gzip.NewWriter(file)
	if _, err := gz.Write([]byte("-- MySQL dump 10.13 Distrib 8.0.36\nCREATE TABLE t1(id int);\n")); err != nil {
		t.Fatalf("write gzip payload: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip writer: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close gzip file: %v", err)
	}

	if err := validateMySQLDumpFile(gzipPath); err != nil {
		t.Fatalf("validate gzip dump: %v", err)
	}
}
