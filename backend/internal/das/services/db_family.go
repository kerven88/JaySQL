package services

import (
	"github.com/simajiong/jaysql/internal/common/dbfamily"
)

// 本文件是 das 包内「数据库类型 → 引擎家族」的判定入口，实际口径委托给
// internal/common/dbfamily，避免 das 与 common 各写一份 switch 而漂移。
//
// 用法约定：
//   - 手里只有 db_type（拿不到 extra_config）→ 用 IsMySQLFamily / IsPostgresFamily
//     （OceanBase 按 MySQL 模式处理）；
//   - 手里同时有 extra_config → 用 *Cfg 版本，OceanBase 会按租户模式精确判定。

func normalizeDBType(value string) string {
	return dbfamily.Normalize(value)
}

// IsMySQLFamily 判定 MySQL 系（MySQL / TiDB / Doris / MariaDB / PolarDB MySQL）。
func IsMySQLFamily(dbType string) bool {
	return dbfamily.IsMySQL(dbType, nil)
}

// IsPostgresFamily 判定 PostgreSQL 系。
func IsPostgresFamily(dbType string) bool {
	return dbfamily.IsPostgres(dbType, nil)
}

// IsMySQLFamilyCfg / IsPostgresFamilyCfg / IsOracleFamilyCfg 是带 extra_config 的精确判定：
// OceanBase 会依据 extra_config.oceanbase_mode 落到 mysql 或 oracle 家族。
func IsMySQLFamilyCfg(dbType string, extraConfig []byte) bool {
	return dbfamily.IsMySQL(dbType, extraConfig)
}

func IsPostgresFamilyCfg(dbType string, extraConfig []byte) bool {
	return dbfamily.IsPostgres(dbType, extraConfig)
}

// HasSchemaLevel 判定该数据库类型是否存在独立的「模式」层级。
//
// PostgreSQL / SQL Server 是「库 → 模式 → 表」三层，申请时必须先选库再选模式；
// MySQL 系只有「库 → 表」，库名与 schema 名是同一个东西。
// 这个判定同时驱动两处：申请页「申请范围」是否出现第四项（模式），
// 以及模式级授权的 schema_name 必须是「库.模式」完整路径的校验。
// OceanBase 只影响 MySQL/Oracle 分支，不会落到 PG/SQLServer，所以这里不看 extra_config。
func HasSchemaLevel(dbType string) bool {
	return IsPostgresFamily(dbType) || normalizeDBType(dbType) == "sqlserver"
}

func IsOracleFamilyCfg(dbType string, extraConfig []byte) bool {
	return dbfamily.IsOracle(dbType, extraConfig)
}

// resolveFamily 返回实例的引擎家族（小写），OceanBase 按租户模式落到 mysql / oracle。
func resolveFamily(dbType string, extraConfig []byte) string {
	return dbfamily.EffectiveFamily(dbType, extraConfig)
}
