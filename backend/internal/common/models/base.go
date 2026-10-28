package models

import (
	"database/sql/driver"
	"fmt"
)

type Model struct {
	ID        uint64    `gorm:"primaryKey" json:"id"`
	CreatedAt LocalTime `gorm:"index:idx_created_at;autoCreateTime;comment:创建时间" json:"created_at"`
	UpdatedAt LocalTime `gorm:"index:idx_updated_at;autoUpdateTime;comment:更新时间" json:"updated_at"`
}

// 枚举类型
type EnumType string

// Scan 把驱动返回值落到 EnumType。
// 原实现是 `*rt = EnumType(value.([]byte))` 的硬断言：只有返回 []byte 的驱动（MySQL）没事，
// 一旦返回 string（SQLite 等）或列为 NULL（返回 nil）就会直接 panic，把整个进程带走。
// 这里改为按类型分支，NULL 归一化为空串，未知类型返回错误而不是 panic。
func (rt *EnumType) Scan(value interface{}) error {
	switch v := value.(type) {
	case nil:
		*rt = ""
	case []byte:
		*rt = EnumType(v)
	case string:
		*rt = EnumType(v)
	default:
		return fmt.Errorf("enum: 无法把 %T 解析为枚举值", value)
	}
	return nil
}

func (rt EnumType) Value() (driver.Value, error) {
	return string(rt), nil
}
