package models

import (
	"database/sql/driver"
	"fmt"
	"time"
)

type LocalTime time.Time

// 注意：必须用值接收者。若用指针接收者，结构体以值类型经 any 序列化时
// 反射值不可寻址，MarshalJSON 不会被调用，LocalTime 会按默认规则渲染成 {}。
func (t LocalTime) MarshalJSON() ([]byte, error) {
	tTime := time.Time(t)
	// 零值（数据库 NULL）统一序列化为 null，前端据此显示 '-'，避免出现 "0001-01-01" 或 {}
	if tTime.IsZero() {
		return []byte("null"), nil
	}
	return []byte(fmt.Sprintf("\"%v\"", tTime.Format("2006-01-02 15:04:05"))), nil
}

func (t LocalTime) Value() (driver.Value, error) {
	var zeroTime time.Time
	tlt := time.Time(t)
	//判断给定时间是否和默认零时间的时间戳相同
	if tlt.UnixNano() == zeroTime.UnixNano() {
		return nil, nil
	}
	return tlt, nil
}

func (t *LocalTime) Scan(v interface{}) error {
	if value, ok := v.(time.Time); ok {
		*t = LocalTime(value)
		return nil
	}
	return fmt.Errorf("can not convert %v to timestamp", v)
}
