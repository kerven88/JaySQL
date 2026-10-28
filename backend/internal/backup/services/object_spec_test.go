package services

import (
	"testing"

	"gorm.io/datatypes"
)

func TestPlanBackupArtifact(t *testing.T) {
	tests := []struct {
		name            string
		dbType          string
		driverType      string
		objectMode      string
		sourceDBName    string
		backupType      string
		objectSpec      datatypes.JSON
		driverConfig    datatypes.JSON
		wantName        string
		wantCompression string
		wantLevel       string
	}{
		{
			name:            "mysql all compresses by default",
			dbType:          "MySQL",
			driverType:      "LOGICAL",
			objectMode:      "ALL",
			backupType:      "FULL",
			wantName:        "all_databases.sql.gz",
			wantCompression: ArtifactCompressionGzip,
			wantLevel:       ArtifactCompressLevelFast,
		},
		{
			name:            "mysql all honours high level",
			dbType:          "MySQL",
			driverType:      "LOGICAL",
			objectMode:      "ALL",
			backupType:      "FULL",
			driverConfig:    datatypes.JSON(`{"compress_enabled":true,"compress_level":"HIGH"}`),
			wantName:        "all_databases.sql.gz",
			wantCompression: ArtifactCompressionGzip,
			wantLevel:       ArtifactCompressLevelHigh,
		},
		{
			name:            "mysql all stays plain when compression disabled",
			dbType:          "MySQL",
			driverType:      "LOGICAL",
			objectMode:      "ALL",
			backupType:      "FULL",
			driverConfig:    datatypes.JSON(`{"compress_enabled":false,"compress_level":"HIGH"}`),
			wantName:        "all_databases.sql",
			wantCompression: ArtifactCompressionNone,
			wantLevel:       "",
		},
		{
			name:            "mysql single database compresses by default",
			dbType:          "MySQL",
			driverType:      "LOGICAL",
			objectMode:      "CUSTOM",
			backupType:      "FULL",
			objectSpec:      datatypes.JSON(`{"databases":[{"name":"db1"}]}`),
			wantName:        "db1.sql.gz",
			wantCompression: ArtifactCompressionGzip,
			wantLevel:       ArtifactCompressLevelFast,
		},
		{
			name:            "mysql multi database uses zip",
			dbType:          "MySQL",
			driverType:      "LOGICAL",
			objectMode:      "CUSTOM",
			backupType:      "FULL",
			objectSpec:      datatypes.JSON(`{"databases":[{"name":"db1"},{"name":"db2"}]}`),
			wantName:        "backup.zip",
			wantCompression: ArtifactCompressionZip,
			wantLevel:       "",
		},
		{
			name:            "mysql physical uses xbstream",
			dbType:          "MySQL",
			driverType:      "XTRABACKUP",
			objectMode:      "ALL",
			backupType:      "FULL",
			wantName:        "backup.xbstream",
			wantCompression: ArtifactCompressionNone,
			wantLevel:       "",
		},
		{
			name:            "postgres logical uses zip",
			dbType:          "PostgreSQL",
			driverType:      "LOGICAL",
			objectMode:      "CUSTOM",
			backupType:      "FULL",
			objectSpec:      datatypes.JSON(`{"databases":[{"name":"postgres"}]}`),
			wantName:        "backup.zip",
			wantCompression: ArtifactCompressionZip,
			wantLevel:       "",
		},
		{
			name:            "pgbackrest uses metadata json",
			dbType:          "PostgreSQL",
			driverType:      "PG_BACKREST",
			objectMode:      "ALL",
			backupType:      "FULL",
			wantName:        "pgbackrest-backup.json",
			wantCompression: ArtifactCompressionNone,
			wantLevel:       "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := PlanBackupArtifact(tc.dbType, tc.driverType, tc.objectMode, tc.sourceDBName, tc.backupType, tc.objectSpec, tc.driverConfig)
			if err != nil {
				t.Fatalf("PlanBackupArtifact() error = %v", err)
			}
			if plan.ArtifactName != tc.wantName {
				t.Fatalf("ArtifactName = %q, want %q", plan.ArtifactName, tc.wantName)
			}
			if plan.StreamCompression != tc.wantCompression {
				t.Fatalf("StreamCompression = %q, want %q", plan.StreamCompression, tc.wantCompression)
			}
			if plan.CompressionLevel != tc.wantLevel {
				t.Fatalf("CompressionLevel = %q, want %q", plan.CompressionLevel, tc.wantLevel)
			}
		})
	}
}

func TestGzipArtifactName(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{in: "all_databases.sql", want: "all_databases.sql.gz"},
		{in: "all_databases.sql.gz", want: "all_databases.sql.gz"},
		{in: "  backup.sql  ", want: "backup.sql.gz"},
		{in: "", want: "all_databases.sql.gz"},
	}
	for _, tc := range cases {
		if got := gzipArtifactName(tc.in); got != tc.want {
			t.Fatalf("gzipArtifactName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestResolveArtifactCompression(t *testing.T) {
	cases := []struct {
		name        string
		raw         datatypes.JSON
		wantEnabled bool
		wantLevel   string
	}{
		{name: "missing config falls back to enabled fast", raw: nil, wantEnabled: true, wantLevel: ArtifactCompressLevelFast},
		{name: "empty json falls back to enabled fast", raw: datatypes.JSON(``), wantEnabled: true, wantLevel: ArtifactCompressLevelFast},
		{name: "explicit enabled keeps default level", raw: datatypes.JSON(`{"compress_enabled":true}`), wantEnabled: true, wantLevel: ArtifactCompressLevelFast},
		{name: "explicit disabled", raw: datatypes.JSON(`{"compress_enabled":false}`), wantEnabled: false, wantLevel: ArtifactCompressLevelFast},
		{name: "high level", raw: datatypes.JSON(`{"compress_enabled":true,"compress_level":"HIGH"}`), wantEnabled: true, wantLevel: ArtifactCompressLevelHigh},
		{name: "lowercase high level", raw: datatypes.JSON(`{"compress_level":"high"}`), wantEnabled: true, wantLevel: ArtifactCompressLevelHigh},
		{name: "illegal level falls back to fast", raw: datatypes.JSON(`{"compress_level":"TURBO"}`), wantEnabled: true, wantLevel: ArtifactCompressLevelFast},
		{name: "malformed json falls back to defaults", raw: datatypes.JSON(`{not-json`), wantEnabled: true, wantLevel: ArtifactCompressLevelFast},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveArtifactCompression(tc.raw)
			if got.Enabled != tc.wantEnabled {
				t.Fatalf("Enabled = %v, want %v", got.Enabled, tc.wantEnabled)
			}
			if got.Level != tc.wantLevel {
				t.Fatalf("Level = %q, want %q", got.Level, tc.wantLevel)
			}
		})
	}
}

func TestGzipLevelForArtifact(t *testing.T) {
	if got := gzipLevelForArtifact(ArtifactCompressLevelFast); got != 1 {
		t.Fatalf("FAST level = %d, want 1 (BestSpeed)", got)
	}
	if got := gzipLevelForArtifact(ArtifactCompressLevelHigh); got != -1 {
		t.Fatalf("HIGH level = %d, want -1 (DefaultCompression)", got)
	}
	if got := gzipLevelForArtifact("unknown"); got != 1 {
		t.Fatalf("非法级别应回落 FAST，got %d", got)
	}
}
