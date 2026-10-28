package services

import (
	"strings"
	"testing"
)

func TestSystemToolCandidatePathsIncludeGenericPostgresInstallDir(t *testing.T) {
	paths := systemToolCandidatePaths("postgres", "16", "pg_dump")
	want := "/usr/local/pgsql/bin/pg_dump"
	for _, path := range paths {
		if path == want {
			return
		}
	}
	t.Fatalf("expected postgres candidate paths to include %s, got %v", want, paths)
}

func TestBuildResolvePostgresToolAsDBUserScriptUsesCommandVAndGenericDir(t *testing.T) {
	script := buildResolvePostgresToolAsDBUserScript("pg_dump", "16.2")
	if !strings.Contains(script, `command -v "$TOOL_NAME"`) {
		t.Fatalf("expected script to lookup tool via command -v, got: %s", script)
	}
	if !strings.Contains(script, `"/usr/local/pgsql/bin/$TOOL_NAME"`) {
		t.Fatalf("expected script to include generic postgres install dir, got: %s", script)
	}
}

func TestBuildResolveMySQLToolAsDBUserScriptUsesCommandVAndCommonDirs(t *testing.T) {
	script := buildResolveMySQLToolAsDBUserScript("mysqldump", "8.0.36")
	if !strings.Contains(script, `command -v "$TOOL_NAME"`) {
		t.Fatalf("expected script to lookup tool via command -v, got: %s", script)
	}
	if !strings.Contains(script, `"/usr/local/mysql/bin/$TOOL_NAME"`) {
		t.Fatalf("expected script to include common mysql install dir, got: %s", script)
	}
	if !strings.Contains(script, `"/usr/local/mariadb/bin/$TOOL_NAME"`) {
		t.Fatalf("expected script to include common mariadb install dir, got: %s", script)
	}
}

func TestBuildSudoCommandPrefixDefersShellExpansionToTargetUser(t *testing.T) {
	realArgs := buildSudoWrappedArgs("postgres", "/home/postgres", []string{"PGPASSWORD"}, map[string]string{"PGPASSWORD": "secret"}, "", "pg_dump", []string{"--version"})
	joined := strings.Join(realArgs, " ")
	if strings.Contains(joined, `/bin/sh -lc cd / && umask 022 && if [ -f /etc/profile ]`) {
		t.Fatalf("expected old inline shell expansion to be removed, got: %s", joined)
	}
	if !strings.Contains(joined, `SHELL_INIT=`) || !strings.Contains(joined, `CMD=`) {
		t.Fatalf("expected SHELL_INIT and CMD to be passed via env, got: %s", joined)
	}
	if !strings.Contains(joined, `/bin/sh -lc eval "$CMD"`) {
		t.Fatalf("expected deferred command evaluation inside target shell, got: %s", joined)
	}
	if !strings.Contains(joined, `HOME=`) {
		t.Fatalf("expected target HOME to be passed into sudo env, got: %s", joined)
	}
}

func TestShellProfileInitSnippetUsesLoginProfilesOnly(t *testing.T) {
	snippet := shellProfileInitSnippet()
	if strings.Contains(snippet, ".bashrc") {
		t.Fatalf("expected interactive bashrc to be excluded, got: %s", snippet)
	}
	if strings.Contains(snippet, ".zshrc") {
		t.Fatalf("expected interactive zshrc to be excluded, got: %s", snippet)
	}
	if !strings.Contains(snippet, ".bash_profile") || !strings.Contains(snippet, ".zprofile") {
		t.Fatalf("expected login profile files to remain, got: %s", snippet)
	}
}
