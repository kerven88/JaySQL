package process

import (
	"strings"
	"testing"

	"github.com/simajiong/jaysql/internal/inspect/config"
)

// MySQL 引擎唯一索引命名：规范配了正则时必须按正则（完整匹配）判定，
// 不能因为默认前缀 UNIQ_ 命中就放行。对应线上漏检：
// CREATE UNIQUE INDEX uniq_t_demo_status ... 配 uk_\w+ 却无告警。
func TestCheckUniquePrefixHonoursRegex(t *testing.T) {
	idx := &IndexPrefix{
		Table:      "t_demo",
		UniqueKeys: []string{"uniq_t_demo_status"},
		InspectParams: &config.InspectParams{
			CHECK_UNIQ_INDEX_PREFIX: true,
			UNQI_INDEX_PREFIX:       "UNIQ_",
			UNIQ_INDEX_REGEX:        `uk_\w+`,
		},
	}
	err := idx.CheckUniquePrefix()
	if err == nil {
		t.Fatal("uniq_t_demo_status 不匹配 uk_\\w+，必须告警")
	}
	if !strings.Contains(err.Error(), `uk_\w+`) {
		t.Errorf("告警文案必须回显生效的正则 uk_\\w+，实际：%s", err.Error())
	}
}

// 合规名字不告警
func TestCheckUniquePrefixPassesCompliant(t *testing.T) {
	idx := &IndexPrefix{
		Table:      "t_demo",
		UniqueKeys: []string{"uk_t_demo_status"},
		InspectParams: &config.InspectParams{
			CHECK_UNIQ_INDEX_PREFIX: true,
			UNQI_INDEX_PREFIX:       "UNIQ_",
			UNIQ_INDEX_REGEX:        `uk_\w+`,
		},
	}
	if err := idx.CheckUniquePrefix(); err != nil {
		t.Fatalf("uk_t_demo_status 合规，不应告警，实际：%v", err)
	}
}

// 未配正则 => 回退前缀（不区分大小写）
func TestCheckUniquePrefixFallsBackToPrefix(t *testing.T) {
	ok := &IndexPrefix{
		Table:      "t_demo",
		UniqueKeys: []string{"uniq_t_demo"},
		InspectParams: &config.InspectParams{
			CHECK_UNIQ_INDEX_PREFIX: true,
			UNQI_INDEX_PREFIX:       "UNIQ_",
		},
	}
	if err := ok.CheckUniquePrefix(); err != nil {
		t.Fatalf("未配正则时 uniq_ 前缀应命中，实际：%v", err)
	}

	bad := &IndexPrefix{
		Table:      "t_demo",
		UniqueKeys: []string{"idx_t_demo"},
		InspectParams: &config.InspectParams{
			CHECK_UNIQ_INDEX_PREFIX: true,
			UNQI_INDEX_PREFIX:       "UNIQ_",
		},
	}
	err := bad.CheckUniquePrefix()
	if err == nil {
		t.Fatal("未配正则时 idx_ 前缀不命中 UNIQ_，必须告警")
	}
	if !strings.Contains(err.Error(), "UNIQ_") {
		t.Errorf("回退前缀时应回显前缀要求，实际：%s", err.Error())
	}
}

// 普通索引与唯一索引同口径：配了正则按正则判
func TestCheckSecondaryPrefixHonoursRegex(t *testing.T) {
	idx := &IndexPrefix{
		Table:         "t_demo",
		SecondaryKeys: []string{"idx_t_demo_status"},
		InspectParams: &config.InspectParams{
			CHECK_SECONDARY_INDEX_PREFIX: true,
			SECONDARY_INDEX_PREFIX:       "IDX_",
			SECONDARY_INDEX_REGEX:        `idx_\w+`,
		},
	}
	if err := idx.CheckSecondaryPrefix(); err != nil {
		t.Fatalf("idx_t_demo_status 完整匹配 idx_\\w+，不应告警，实际：%v", err)
	}

	idx.SecondaryKeys = []string{"UX_t_demo"}
	err := idx.CheckSecondaryPrefix()
	if err == nil {
		t.Fatal("UX_t_demo 不匹配 idx_\\w+，必须告警")
	}
	if !strings.Contains(err.Error(), `idx_\w+`) {
		t.Errorf("告警文案必须回显生效的正则，实际：%s", err.Error())
	}
}
