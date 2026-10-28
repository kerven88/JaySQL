package process

import (
	"fmt"
	"strings"

	"github.com/simajiong/jaysql/internal/inspect/config"

	"github.com/pingcap/tidb/pkg/parser/mysql"
)

type PrimaryKey struct {
	Table            string // 表名
	Column           string // 列
	Tp               byte   // 类型
	Flag             uint   // flag
	HasNotNull       bool   // 是否not null
	HasAutoIncrement bool   // 是否自增
	InspectParams    *config.InspectParams
}

// pkTypeNames 类型名 → TiDB parser 类型字节码（主键列类型白名单匹配用）
var pkTypeNames = map[string]byte{
	"tinyint":    mysql.TypeTiny,
	"smallint":   mysql.TypeShort,
	"mediumint":  mysql.TypeInt24,
	"int":        mysql.TypeLong,
	"integer":    mysql.TypeLong,
	"bigint":     mysql.TypeLonglong,
	"float":      mysql.TypeFloat,
	"double":     mysql.TypeDouble,
	"decimal":    mysql.TypeNewDecimal,
	"numeric":    mysql.TypeNewDecimal,
	"char":       mysql.TypeString,
	"varchar":    mysql.TypeVarchar,
	"binary":     mysql.TypeString,
	"varbinary":  mysql.TypeVarchar,
	"tinytext":   mysql.TypeTinyBlob,
	"text":       mysql.TypeBlob,
	"mediumtext": mysql.TypeMediumBlob,
	"longtext":   mysql.TypeLongBlob,
	"tinyblob":   mysql.TypeTinyBlob,
	"blob":       mysql.TypeBlob,
	"mediumblob": mysql.TypeMediumBlob,
	"longblob":   mysql.TypeLongBlob,
	"date":       mysql.TypeDate,
	"time":       mysql.TypeDuration,
	"datetime":   mysql.TypeDatetime,
	"timestamp":  mysql.TypeTimestamp,
	"year":       mysql.TypeYear,
	"bit":        mysql.TypeBit,
	"json":       mysql.TypeJSON,
	"enum":       mysql.TypeEnum,
	"set":        mysql.TypeSet,
	// 以下为语法别名/扩展类型，保证与主键列类型可选值字典（config.PrimaryKeySupportedTypes）
	// 逐项对齐——用户能选到的类型，引擎必须能识别，否则会被误判为不合规。
	"serial":     mysql.TypeLonglong, // SERIAL = BIGINT UNSIGNED NOT NULL AUTO_INCREMENT UNIQUE
	"real":       mysql.TypeDouble,   // MySQL 中 REAL 默认等价于 DOUBLE
	"bool":       mysql.TypeTiny,     // BOOL/BOOLEAN 是 TINYINT(1) 的同义词
	"boolean":    mysql.TypeTiny,
	"geometry":   mysql.TypeGeometry,
	"point":      mysql.TypeGeometry,
	"linestring": mysql.TypeGeometry,
	"polygon":    mysql.TypeGeometry,
}

// CheckBigint 校验主键列类型是否落在规则配置的白名单内。
// 白名单来自规则参数 pk_types（用户配置）；**未配置（空）表示不限制类型**，直接放行。
// 注：本规则不再校验 unsigned（历史版本曾要求主键必须为 unsigned，现已移除）。
func (p *PrimaryKey) CheckBigint() error {
	if !p.InspectParams.CHECK_PRIMARYKEY_USE_BIGINT {
		return nil
	}
	allowed := p.InspectParams.PRIMARYKEY_SUPPORT_TYPES
	if len(allowed) == 0 {
		// 未配置白名单 => 不限制主键列类型
		return nil
	}
	for _, name := range allowed {
		if tp, ok := pkTypeNames[strings.ToLower(strings.TrimSpace(name))]; ok && tp == p.Tp {
			return nil
		}
	}
	return fmt.Errorf("主键`%s`类型不符合要求，仅允许使用%s（表`%s`）", p.Column, strings.Join(allowed, ", "), p.Table)
}

func (p *PrimaryKey) CheckAutoIncrement() error {
	if !p.HasAutoIncrement && p.InspectParams.CHECK_PRIMARYKEY_USE_AUTO_INCREMENT {
		// 必须定义AUTO_INCREMENT
		return fmt.Errorf("主键`%s`必须定义auto_increment（表`%s`）", p.Column, p.Table)
	}
	return nil
}

// 注：不提供主键列 NOT NULL 检查。MySQL 主键列天然隐式 NOT NULL（即使建表语句
// 未显式书写 NOT NULL），按「显式文本」判断只会产生语义性误报，故移除。
