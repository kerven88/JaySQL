package services

import (
	"os/exec"
	"strings"
	"testing"
)

func TestKillAgentScriptMatchesOnlyExactNode(t *testing.T) {
	script := killAgentProcessesScript(1, "-TERM")
	if !strings.Contains(script, "pgrep -a -x") {
		t.Fatal("must exclude shell wrappers by exact process name")
	}
	fixture := `
pgrep() { printf '%s\n' '101 /opt/backup-agent -node-id 1 -token test' '102 /opt/backup-agent -node-id 10 -token other' '103 /opt/backup-agent -node-id=1 -token test'; }
kill() { printf 'SIGNAL:%s\n' "$*"; }
`
	output, err := exec.Command("sh", "-c", fixture+script).CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != "SIGNAL:-TERM 101 103" {
		t.Fatalf("unexpected targets: %s", output)
	}
}
