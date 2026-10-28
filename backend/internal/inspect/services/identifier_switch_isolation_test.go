package services

import (
	"testing"

	"github.com/simajiong/jaysql/internal/inspect/config"
	"github.com/simajiong/jaysql/internal/inspect/models"
)

// 回归（2026-09-29）：表名与列名的大小写规则曾经共写同一个 CHECK_IDENTIFIER，
// 导致「限制字段名大小写」关闭后列名检查仍被执行（被「限制表名大小写」打开），
// 而该规则已不在 ruleLevels 中 → 工单无法归级 → 兜底到规范占优级别（生产=must），
// 表现为「页面上建议改进、工单里必须改进」。
func TestIdentifierSwitchIsolation(t *testing.T) {
	colOff := models.InsightSQLStandardRule{RuleKey: "mysql.struct.limit_col_name_case", Level: "off"}
	tblOn := models.InsightSQLStandardRule{RuleKey: "mysql.struct.limit_tbl_name_case", Level: "suggest"}

	// 顺序 A：先关列名、后开表名
	pA := config.DefaultInspectParams()
	applyStandardRule(&pA, colOff)
	applyStandardRule(&pA, tblOn)
	if pA.CHECK_COLUMN_IDENTIFIER {
		t.Errorf("列名大小写规则为 off，CHECK_COLUMN_IDENTIFIER 必须为 false（否则被关闭的规则仍会告警）")
	}
	if !pA.CHECK_TABLE_IDENTIFIER {
		t.Errorf("表名大小写规则为 suggest，CHECK_TABLE_IDENTIFIER 必须为 true")
	}

	// 顺序 B：先开表名、后关列名（开关必须与顺序无关）
	pB := config.DefaultInspectParams()
	applyStandardRule(&pB, tblOn)
	applyStandardRule(&pB, colOff)
	if pB.CHECK_COLUMN_IDENTIFIER || !pB.CHECK_TABLE_IDENTIFIER {
		t.Errorf("开关结果不应依赖规则应用顺序: column=%v table=%v",
			pB.CHECK_COLUMN_IDENTIFIER, pB.CHECK_TABLE_IDENTIFIER)
	}
}

// 索引名基础字符集校验跟随「索引名格式」规则，不再由表名/列名大小写规则误开。
func TestIndexIdentifierSwitchFollowsIndexRules(t *testing.T) {
	p := config.DefaultInspectParams()
	// 只开表名/列名大小写：索引名字符集校验不应被打开
	applyStandardRule(&p, models.InsightSQLStandardRule{RuleKey: "mysql.struct.limit_tbl_name_case", Level: "must"})
	applyStandardRule(&p, models.InsightSQLStandardRule{RuleKey: "mysql.struct.limit_col_name_case", Level: "must"})
	if p.CHECK_INDEX_IDENTIFIER {
		t.Errorf("索引名字符集校验必须由索引名格式规则控制，不应被表名/列名大小写规则打开")
	}

	p2 := config.DefaultInspectParams()
	applyStandardRule(&p2, models.InsightSQLStandardRule{RuleKey: "mysql.struct.limit_normal_idx_name_fmt", Level: "suggest"})
	if !p2.CHECK_INDEX_IDENTIFIER {
		t.Errorf("普通索引名格式规则开启时，索引名字符集校验应同步开启")
	}
}
