package services

import (
	"encoding/json"
	"reflect"
	"testing"

	"gorm.io/datatypes"
)

func TestResolveMySQLPhysicalRedoBaseDir(t *testing.T) {
	tests := []struct {
		name    string
		datadir string
		raw     string
		want    string
	}{
		{name: "empty uses datadir", datadir: "/data/mysql", raw: "", want: "/data/mysql"},
		{name: "dot uses datadir", datadir: "/data/mysql", raw: ".", want: "/data/mysql"},
		{name: "absolute path kept", datadir: "/data/mysql", raw: "/redo/custom", want: "/redo/custom"},
		{name: "relative path joined", datadir: "/data/mysql", raw: "redo", want: "/data/mysql/redo"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveMySQLPhysicalRedoBaseDir(tc.datadir, tc.raw); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestResolveMySQLPhysicalUndoBaseDir(t *testing.T) {
	tests := []struct {
		name    string
		datadir string
		raw     string
		want    string
	}{
		{name: "empty uses datadir", datadir: "/data/mysql", raw: "", want: "/data/mysql"},
		{name: "dot uses datadir", datadir: "/data/mysql", raw: ".", want: "/data/mysql"},
		{name: "absolute path kept", datadir: "/data/mysql", raw: "/undo/custom", want: "/undo/custom"},
		{name: "relative path joined", datadir: "/data/mysql", raw: "undo", want: "/data/mysql/undo"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveMySQLPhysicalUndoBaseDir(tc.datadir, tc.raw); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestNormalizeMySQLPhysicalExtraDataDirs(t *testing.T) {
	got := normalizeMySQLPhysicalExtraDataDirs("/data/mysql", "/ext/a;ext/b\n/data/mysql")
	want := []string{"/data/mysql", "/data/mysql/ext/b", "/ext/a"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
}

func TestMySQLPrivilegeGrantee(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "normal user host", raw: "backup@%", want: "'backup'@'%'"},
		{name: "empty", raw: "", want: ""},
		{name: "no host", raw: "backup", want: "'backup'"},
		{name: "quote escaped", raw: "o'hara@local", want: "'o''hara'@'local'"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := mysqlPrivilegeGrantee(tc.raw); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestParseMySQLPrivilegesFromGrantLines(t *testing.T) {
	required := []string{"BACKUP_ADMIN", "LOCK TABLES", "PROCESS", "RELOAD", "REPLICATION CLIENT"}
	lines := []string{
		"GRANT USAGE ON *.* TO `root`@`%`",
		"GRANT SELECT, PROCESS, RELOAD, LOCK TABLES, REPLICATION CLIENT, BACKUP_ADMIN ON *.* TO `root`@`%`",
		"GRANT `r_backup`@`%` TO `root`@`%`",
	}
	got := parseMySQLPrivilegesFromGrantLines(lines, required)
	for _, privilege := range required {
		if _, ok := got[privilege]; !ok {
			t.Fatalf("expected privilege %s in %#v", privilege, got)
		}
	}
}

func TestParseMySQLPrivilegesFromGrantLinesAllPrivileges(t *testing.T) {
	required := []string{"BACKUP_ADMIN", "LOCK TABLES", "PROCESS", "RELOAD", "REPLICATION CLIENT"}
	lines := []string{
		"GRANT ALL PRIVILEGES ON *.* TO `root`@`%` WITH GRANT OPTION",
	}
	got := parseMySQLPrivilegesFromGrantLines(lines, required)
	for _, privilege := range required {
		if _, ok := got[privilege]; !ok {
			t.Fatalf("expected privilege %s in %#v", privilege, got)
		}
	}
}

func TestMySQLPhysicalStringValue(t *testing.T) {
	tests := []struct {
		name  string
		value any
		want  string
	}{
		{name: "nil", value: nil, want: ""},
		{name: "nil string", value: "<nil>", want: ""},
		{name: "null string", value: "null", want: ""},
		{name: "normal text", value: "/data/mysql", want: "/data/mysql"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := mysqlPhysicalStringValue(tc.value); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestParseMySQLCurrentRoles(t *testing.T) {
	got := parseMySQLCurrentRoles("`backup_role`@`%`, `ops_role`@`localhost`")
	want := []string{"'backup_role'@'%'", "'ops_role'@'localhost'"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
}

func TestBuildMySQLShowGrantsUsingQuery(t *testing.T) {
	got := buildMySQLShowGrantsUsingQuery([]string{"'backup_role'@'%'", "'ops_role'@'localhost'"})
	want := "show grants for current_user() using 'backup_role'@'%', 'ops_role'@'localhost'"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestNormalizeMySQLPhysicalDriverConfigAcceptsLegacyOSUserAliases(t *testing.T) {
	raw := map[string]any{
		"process_max":  2,
		"process_user": "root",
		"dump_path":    "/usr/local/goinsight/tools/mysql/8.0/bin/xtrabackup",
	}
	got, err := normalizeMySQLPhysicalDriverConfig(raw, "FULL")
	if err != nil {
		t.Fatalf("normalize error: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(got, &payload); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if payload["os_user"] != "root" {
		t.Fatalf("os_user got %#v want root", payload["os_user"])
	}
	if payload["tool_path"] != "/usr/local/goinsight/tools/mysql/8.0/bin/xtrabackup" {
		t.Fatalf("tool_path got %#v", payload["tool_path"])
	}
}

func TestLoadMySQLPhysicalDriverConfigAcceptsLegacyOSUserAliases(t *testing.T) {
	raw := datatypes.JSON(`{"process_user":"root","xtrabackup_path":"/opt/app/xtrabackup"}`)
	cfg, err := loadMySQLPhysicalDriverConfig(raw)
	if err != nil {
		t.Fatalf("load error: %v", err)
	}
	if cfg.OSUser != "root" {
		t.Fatalf("os_user got %q want root", cfg.OSUser)
	}
	if cfg.ToolPath != "/opt/app/xtrabackup" {
		t.Fatalf("tool_path got %q want /opt/app/xtrabackup", cfg.ToolPath)
	}
}
