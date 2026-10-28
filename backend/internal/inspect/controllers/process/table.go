package process

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/simajiong/jaysql/pkg/utils"

	"github.com/simajiong/jaysql/internal/inspect/config"
)

type TableOptions struct {
	Table         string // 表名
	Type          string // 类型:create or alter
	Engine        string // 引擎
	Charset       string // 表字符集
	Collate       string // 表排序规则
	AutoIncrement uint64 // 表自增
	RowFormat     string // 行格式
	IsPartition   bool   // 是否分区
	PartitionType string // 分区类型
	HasComment    bool   // 表示有注释，代表不了注释为空
	Comment       string // 表注释
	InspectParams *config.InspectParams
}

// 检查表名长度
func (t *TableOptions) CheckTableLength() error {
	// MAX_TABLE_NAME_LENGTH=0 表示不限制（规范中无对应规则时的默认值）
	if t.InspectParams.MAX_TABLE_NAME_LENGTH > 0 && utf8.RuneCountInString(t.Table) > t.InspectParams.MAX_TABLE_NAME_LENGTH {
		return fmt.Errorf("表名`%s`命名长度超出限制，最大字符数%d", t.Table, t.InspectParams.MAX_TABLE_NAME_LENGTH)
	}
	return nil
}

// 检查表名合法性
func (t *TableOptions) CheckTableIdentifer() error {
	if t.InspectParams.CHECK_TABLE_IDENTIFIER {
		// 表名大小写要求由「限制表名大小写」规则的 name_case 参数决定，默认小写；
		// 报错文案回显当前生效的大小写要求与正则，便于确认配置是否生效
		pattern := utils.LowerNamePattern
		caseText := "小写"
		if strings.EqualFold(strings.TrimSpace(t.InspectParams.TABLE_NAME_CASE), "upper") {
			pattern = utils.UpperNamePattern
			caseText = "大写"
		}
		if ok := utils.IsMatchPattern(pattern, t.Table); !ok {
			return fmt.Errorf("表`%s`命名不符合要求：必须为%s，仅允许匹配正则`%s`", t.Table, caseText, pattern)
		}
	}
	return nil
}

// 检查表名是否为关键字
func (t *TableOptions) CheckTableIdentiferKeyword() error {
	if t.InspectParams.CHECK_IDENTIFER_KEYWORD {
		if _, ok := Keywords[strings.ToUpper(t.Table)]; ok {
			return fmt.Errorf("表`%s`的命名不允许使用关键字", t.Table)
		}
	}
	return nil
}

// 检查存储引擎
func (t *TableOptions) CheckTableEngine() error {
	if !t.InspectParams.CHECK_TABLE_ENGINE {
		// 规范中未开启「限制表存储引擎」规则时不检查
		return nil
	}
	if t.Type == "create" && len(t.Engine) == 0 {
		return fmt.Errorf("表`%s`必须显式指定存储引擎，支持的存储引擎为%s", t.Table, t.InspectParams.TABLE_SUPPORT_ENGINE)
	}
	if len(t.Engine) > 0 {
		if !utils.IsContain(t.InspectParams.TABLE_SUPPORT_ENGINE, t.Engine) {
			return fmt.Errorf("表`%s`指定的存储引擎`%s`不符合要求，支持的存储引擎为`%s`", t.Table, t.Engine, t.InspectParams.TABLE_SUPPORT_ENGINE)
		}
	}
	return nil
}

// 检查表行格式
func (t *TableOptions) CheckInnoDBRowFormat() error {
	// 行格式白名单为空表示不检查（规范中无对应规则时的默认值）
	if len(t.InspectParams.INNODB_ROW_FORMAT) == 0 {
		return nil
	}
	if !utils.IsContain(t.InspectParams.INNODB_ROW_FORMAT, t.RowFormat) {
		return fmt.Errorf("表`%s`允许设置的行格式为%s，当前ROW_FORMAT=%s", t.Table, strings.Join(t.InspectParams.INNODB_ROW_FORMAT, ","), t.RowFormat)
	}
	return nil
}

// 检查表分区
func (t *TableOptions) CheckTablePartition() error {
	if !t.InspectParams.ENABLE_PARTITION_TABLE && t.IsPartition {
		return fmt.Errorf("表`%s`不允许定义分区表", t.Table)
	}
	if t.InspectParams.ENABLE_PARTITION_TABLE && t.IsPartition {
		supportPartTypes := []string{"RANGE", "HASH", "LIST", "KEY", "SYSTEM_TIME"}
		if !utils.IsContain(supportPartTypes, t.PartitionType) {
			return fmt.Errorf("表`%s`不支持自定义的分区类型`%s`，支持的分区类型%s", t.Table, t.PartitionType, supportPartTypes)
		}
	}
	return nil
}

// 检查表注释
func (t *TableOptions) CheckTableComment() error {
	if t.InspectParams.CHECK_TABLE_COMMENT {
		if t.Type == "create" && !t.HasComment {
			return fmt.Errorf("表`%s`必须要有注释", t.Table)
		}
		if t.HasComment && len(strings.TrimSpace(t.Comment)) == 0 {
			return fmt.Errorf("表`%s`的注释不能为空或空字符", t.Table)
		}
		if t.HasComment && t.InspectParams.TABLE_COMMENT_LENGTH > 0 && utf8.RuneCountInString(strings.TrimSpace(t.Comment)) > t.InspectParams.TABLE_COMMENT_LENGTH {
			return fmt.Errorf("表`%s`的注释长度超出限制，最大字符限制为%d", t.Table, t.InspectParams.TABLE_COMMENT_LENGTH)
		}
	}
	return nil
}

// 检查表字符集
func (t *TableOptions) CheckTableCharset() error {
	// 获取支持的字符集
	var tblSupportCharset []string
	// 获取推荐的排序规则
	var tblRecommendCollation string
	for _, item := range t.InspectParams.TABLE_SUPPORT_CHARSET {
		tblSupportCharset = append(tblSupportCharset, item["charset"])
		if len(t.Charset) > 0 && item["charset"] == t.Charset {
			tblRecommendCollation = item["recommend"]
		}
	}
	charset := Charset{
		SupportCharset:     tblSupportCharset,
		RecommendCollation: tblRecommendCollation,
		Table:              TableCharset{Table: t.Table, Charset: t.Charset, Collate: t.Collate},
	}
	// 表字符集检查
	if t.InspectParams.CHECK_TABLE_CHARSET {
		switch t.Type {
		case "create":
			if err := charset.CheckTable(); err != nil {
				return err
			}
		case "alter":
			if len(t.Charset) > 0 || len(t.Collate) > 0 {
				if err := charset.CheckTable(); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// 检查表排序规则（规则 limit_tbl_collation：限制表校验规则）
// 显式指定的 COLLATE 必须在白名单内；未指定时不拦截（字符集匹配检查由 CheckTableCharset 负责）。
func (t *TableOptions) CheckTableCollation() error {
	if !t.InspectParams.CHECK_TABLE_COLLATION || len(t.InspectParams.TABLE_SUPPORT_COLLATION) == 0 {
		return nil
	}
	if len(t.Collate) == 0 {
		return nil
	}
	for _, item := range t.InspectParams.TABLE_SUPPORT_COLLATION {
		if strings.EqualFold(strings.TrimSpace(item), t.Collate) {
			return nil
		}
	}
	return fmt.Errorf("表`%s`指定的排序规则`%s`不符合要求，可选排序规则为%s", t.Table, t.Collate, strings.Join(t.InspectParams.TABLE_SUPPORT_COLLATION, ","))
}

// 建表时，自增初始值必须显式指定且不得超过规范配置的上限（默认 1）
func (t *TableOptions) CheckTableAutoIncrementInitValue() error {
	if t.InspectParams.CHECK_TABLE_AUTOINCREMENT_INIT_VALUE {
		limit := t.InspectParams.TABLE_AUTOINCREMENT_INIT_VALUE
		if limit <= 0 {
			limit = 1
		}
		if t.Type == "create" && (t.AutoIncrement == 0 || t.AutoIncrement > uint64(limit)) {
			// create语句自增值需要显式指定且不超过上限
			return fmt.Errorf("表`%s`的自增初始值必须显式指定且不得超过%d", t.Table, limit)
		}
	}
	return nil
}
