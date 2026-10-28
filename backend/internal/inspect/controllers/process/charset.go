package process

import (
	"fmt"

	"github.com/simajiong/jaysql/pkg/utils"
)

// 字符集
type TableCharset struct {
	Table   string
	Charset string
	Collate string
}
type ColumnCharset struct {
	Table   string
	Column  string
	Tp      byte
	Charset string
	Collate string
}
type Charset struct {
	SupportCharset     []string
	RecommendCollation string
	Table              TableCharset
	Cols               []ColumnCharset
}

// 检查表的字符集
func (c *Charset) CheckTable() error {
	/*
		1.表必须指定字符集
		1.指定的字符集必须是可选的字符集
		2.指定排序规则的同时必须指定字符集
		3.检查字符集排序规则是否符合要求
	*/
	if !utils.IsContain(c.SupportCharset, c.Table.Charset) {
		if c.Table.Charset == "" {
			return fmt.Errorf("表`%s`必须指定字符集，可选字符集为%s", c.Table.Table, c.SupportCharset)
		} else {
			return fmt.Errorf("表`%s`指定的字符集`%s`不符合要求，可选字符集为%s", c.Table.Table, c.Table.Charset, c.SupportCharset)
		}
	}

	if len(c.Table.Collate) > 0 && len(c.Table.Charset) > 0 && !utils.HasPrefix(c.Table.Collate, c.Table.Charset+"_", false) {
		// 检查排序规则的前缀，前缀必须为字符集+"_"
		return fmt.Errorf("表`%s`指定的字符集排序规则`%s`不符合要求，应指定前缀为%s的排序规则，推荐的字符集排序规则为%s", c.Table.Table, c.Table.Charset, c.Table.Charset+"_", c.RecommendCollation)
	}
	return nil
}
