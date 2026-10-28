package services

import (
	"strings"
	"testing"

	commonModels "github.com/simajiong/jaysql/internal/common/models"
)

// 三种引擎各自的失败码全集。
//
// 下面的测试会遍历它们，断言每一个码都能反查到至少一个前提项——
// 这条断言的价值在于：以后有人新增了一个失败码却忘了挂前提项，
// 前端就会出现「状态列报了错，但排查指引里找不到对应条目」的哑火状态，
// 而这类问题在联调阶段几乎发现不了（只有真的触发该失败路径才会暴露）。
var slowLogFailureCodesByEngine = map[string][]string{
	"MySQL": {
		failMySQLNoCredential, failMySQLConnectFailed, failMySQLVariableMissing,
		failMySQLSlowLogDisabled, failMySQLLogOutputNoFile, failMySQLLongQueryTimeBad,
		failMySQLSSHUnreachable, failMySQLFileMissing, failMySQLFileNoRead,
		failMySQLParseFailed,
	},
	"PostgreSQL": {
		failPGNoCredential, failPGConnectFailed, failPGVersionUnsupported,
		failPGCollectorOff, failPGMinDurationDisabled, failPGMinDurationZero,
		failPGPrefixMissingUserDB, failPGNoMonitorRole, failPGLogFileUnreadable,
		failPGParseFailed,
	},
	"Oracle": {
		failOracleNoCredential, failOracleConnectFailed, failOracleNoVSQLAccess,
		failOracleStatsBasic, failOracleVersionTooOld, failOracleSampleFailed,
		failOracleAWRNoLicense, failOraclePersistFailed,
	},
}

// 引擎级失败码：跨引擎通用，文件采集链路（MySQL / PostgreSQL）都会产出。
var engineLevelFailureCodes = []string{
	failEngineUnsupported, failEngineNoCredential, failEngineSSHFailed,
	failEngineFileMissing, failEngineParseFailed, failEnginePersistFailed,
}

var slowLogPrerequisiteKinds = map[string]bool{
	prereqKindVersion:    true,
	prereqKindParameter:  true,
	prereqKindPermission: true,
	prereqKindFeature:    true,
	prereqKindChannel:    true,
	prereqKindFile:       true,
	prereqKindCaliber:    true,
}

// TestSlowLogPrerequisitesCoversAllEngines 校验每种引擎都声明了完整的前提项。
func TestSlowLogPrerequisitesCoversAllEngines(t *testing.T) {
	for dbType := range slowLogFailureCodesByEngine {
		items := SlowLogPrerequisites(dbType, nil)
		if len(items) == 0 {
			t.Fatalf("引擎 %s 的前提项为空", dbType)
		}
		seen := map[string]bool{}
		for _, item := range items {
			if strings.TrimSpace(item.Key) == "" {
				t.Fatalf("引擎 %s 存在缺 Key 的前提项", dbType)
			}
			if seen[item.Key] {
				t.Fatalf("引擎 %s 的前提项 Key 重复：%s", dbType, item.Key)
			}
			seen[item.Key] = true
			if !slowLogPrerequisiteKinds[item.Kind] {
				t.Fatalf("引擎 %s 的前提项 %s 使用了未定义的 kind：%s", dbType, item.Key, item.Kind)
			}
			if strings.TrimSpace(item.Requirement) == "" {
				t.Fatalf("引擎 %s 的前提项 %s 缺少 requirement", dbType, item.Key)
			}
			if strings.TrimSpace(item.Consequence) == "" {
				t.Fatalf("引擎 %s 的前提项 %s 缺少 consequence", dbType, item.Key)
			}
		}
	}
}

// TestSlowLogPrerequisiteKeysCoverAllFailureCodes 是这份声明库的核心回归测试：
// 任何一个采集失败码都必须能关联到至少一个前提项，否则用户会看到「报错但无指引」。
func TestSlowLogPrerequisiteKeysCoverAllFailureCodes(t *testing.T) {
	for dbType, codes := range slowLogFailureCodesByEngine {
		for _, code := range codes {
			keys := MatchPrerequisiteKeys(dbType, nil, code)
			if len(keys) == 0 {
				t.Errorf("引擎 %s 的失败码 %s 未关联到任何前提项", dbType, code)
			}
		}
	}

	// 引擎级失败码（文件采集链路共用），MySQL / PostgreSQL 两条链路都要能命中
	for _, dbType := range []string{"MySQL", "PostgreSQL"} {
		for _, code := range engineLevelFailureCodes {
			if code == failEngineUnsupported {
				// UNSUPPORTED 表示引擎不在支持范围内，不属于「前提条件未满足」
				continue
			}
			keys := MatchPrerequisiteKeys(dbType, nil, code)
			if len(keys) == 0 {
				t.Errorf("引擎 %s 的引擎级失败码 %s 未关联到任何前提项", dbType, code)
			}
		}
	}
}

// TestSlowLogPrerequisitesUnknownEngine 未知引擎返回空清单，
// 前端据此显示「暂不支持」，而不是渲染一份空面板。
func TestSlowLogPrerequisitesUnknownEngine(t *testing.T) {
	if items := SlowLogPrerequisites("MongoDB", nil); len(items) != 0 {
		t.Fatalf("未知引擎应返回空前提项，实际 %d 项", len(items))
	}
	if items := SlowLogPrerequisites("", nil); len(items) != 0 {
		t.Fatalf("空类型应返回空前提项，实际 %d 项", len(items))
	}
}

// TestSlowLogEngineSupportedByFamily 引擎族判定：
// 确保新增兼容引擎自动纳入支持范围，不必在每处白名单里逐个补名字。
func TestSlowLogEngineSupportedByFamily(t *testing.T) {
	supported := []string{"MySQL", "MariaDB", "TiDB", "Doris", "PolarDB", "PostgreSQL", "Postgres", "Oracle", "OceanBase"}
	for _, dbType := range supported {
		if !slowLogEngineSupported(commonModels.EnumType(dbType), nil) {
			t.Errorf("引擎 %s 应被判定为支持", dbType)
		}
	}
	unsupported := []string{"ClickHouse", "SQLServer", "MongoDB", "Redis"}
	for _, dbType := range unsupported {
		if slowLogEngineSupported(commonModels.EnumType(dbType), nil) {
			t.Errorf("引擎 %s 不应被判定为支持", dbType)
		}
	}
}
