package services

import "testing"

func TestPhysicalDriversBypassLogicalDumpJobs(t *testing.T) {
	if !isMySQLPhysicalDriverType("XTRABACKUP") {
		t.Fatalf("expected XTRABACKUP to be treated as MySQL physical driver")
	}
	if !isPostgresPhysicalDriverType("PG_BACKREST") {
		t.Fatalf("expected PG_BACKREST to be treated as PostgreSQL physical driver")
	}
	if isMySQLPhysicalDriverType("LOGICAL") {
		t.Fatalf("logical backup should not be treated as MySQL physical driver")
	}
	if isPostgresPhysicalDriverType("LOGICAL") {
		t.Fatalf("logical backup should not be treated as PostgreSQL physical driver")
	}
}
