package services

import (
	"encoding/json"
	"strings"
	"testing"
)

// 「限制主键列类型」规则保存时的 pk_types 合法性校验
func TestValidatePrimaryKeyTypes(t *testing.T) {
	cases := []struct {
		name    string
		params  string
		wantErr bool
	}{
		{"空白名单合法(表示不限制类型)", `{"pk_types": []}`, false},
		{"无 pk_types 字段合法", `{}`, false},
		{"空字符串参数合法", ``, false},
		{"MySQL 类型合法", `{"pk_types": ["bigint","int","varchar","geometry"]}`, false},
		{"大小写与首尾空格归一后合法", `{"pk_types": [" BIGINT ", "Int"]}`, false},
		// 历史上白名单只有 MySQL 体系类型，PG/Oracle 规则的类型存不下来（页面报「不在可选范围内」）
		{"PostgreSQL 类型合法", `{"pk_types": ["bigserial","jsonb","uuid"]}`, false},
		{"Oracle 类型合法", `{"pk_types": ["number","varchar2","long","long raw"]}`, false},
		// 同一个能力项跨库合并成一行、类型清单写回所有勾选的库 => 校验口径是并集，
		// 因此跨库类型必须放行（否则勾选多库保存必失败）
		{"多库并集同时出现", `{"pk_types": ["bigint","number","uuid"]}`, false},
		{"合法值中夹带非法值", `{"pk_types": ["bigint","char2"]}`, true},
		{"引擎无法识别的类型名", `{"pk_types": ["字符型"]}`, true},
		{"可读但非可选值", `{"pk_types": ["char2"]}`, true},
	}
	for _, c := range cases {
		err := validatePrimaryKeyTypes(json.RawMessage(c.params))
		switch {
		case c.wantErr && err == nil:
			t.Errorf("%s: 期望被拦截，实际通过了", c.name)
		case !c.wantErr && err != nil:
			t.Errorf("%s: 不应报错，实际: %v", c.name, err)
		case c.wantErr && err != nil && !strings.Contains(err.Error(), "不在可选范围内"):
			t.Errorf("%s: 错误提示不符，实际: %v", c.name, err)
		}
	}
}

// 非法值报错时必须回显非法项与可选值提示，便于管理员修正
func TestValidatePrimaryKeyTypesErrorDetail(t *testing.T) {
	err := validatePrimaryKeyTypes(json.RawMessage(`{"pk_types": ["bigint","char2","字符型"]}`))
	if err == nil {
		t.Fatal("应拦截非法类型")
	}
	msg := err.Error()
	if !strings.Contains(msg, "char2") || !strings.Contains(msg, "字符型") {
		t.Errorf("错误提示应列出全部非法项，实际: %s", msg)
	}
	if !strings.Contains(msg, "bigint") {
		t.Errorf("错误提示应回显可选值，实际: %s", msg)
	}
}
