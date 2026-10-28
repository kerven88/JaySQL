package utils

import "testing"

// 索引命名判定：正则优先且要求完整匹配，未配正则才回退前缀。
// 这是 MySQL / PG 两套审核引擎共用的判定口径，必须锁死行为。
func TestMatchIndexNameRegexFirst(t *testing.T) {
	const regex = `uk_\w+`

	// 配了正则：前缀不参与判定，哪怕前缀命中也要按正则判
	if MatchIndexName("uniq_t_demo_status", regex, "UNIQ_") {
		t.Error("uniq_t_demo_status 不匹配 uk_\\w+，即便前缀 UNIQ_ 命中也不应通过（这正是本次漏检的根因）")
	}
	if !MatchIndexName("uk_t_demo_status", regex, "UNIQ_") {
		t.Error("uk_t_demo_status 完整匹配 uk_\\w+，应通过")
	}
}

// 完整匹配：不能因“子串命中”而放行（regexp.MatchString 的非锚定缺陷）
func TestMatchIndexNameRequiresFullMatch(t *testing.T) {
	const regex = `uk_\w+`
	for _, name := range []string{"xuk_abc", "uk_abc_tail", "a_uk_b", "uk"} {
		// 注意 uk_abc_tail 完整匹配 uk_\w+ 是成立的，单独断言
		if name == "uk_abc_tail" {
			if !MatchIndexName(name, regex, "") {
				t.Errorf("%s 完整匹配 uk_\\w+，应通过", name)
			}
			continue
		}
		if MatchIndexName(name, regex, "") {
			t.Errorf("%s 仅子串命中 uk_\\w+，必须判为不合规（非锚定会误放行）", name)
		}
	}
}

// 大小写：正则默认区分大小写（与前缀的“不区分大小写”语义并存但只在各自分支生效）
func TestMatchIndexNameCaseSensitivity(t *testing.T) {
	if MatchIndexName("UK_t_demo", `uk_\w+`, "") {
		t.Error("正则 uk_\\w+ 区分大小写，UK_t_demo 应判不合规")
	}
	// 未配正则时回退前缀，前缀不区分大小写
	if !MatchIndexName("UNIQ_t_demo", "", "uniq_") {
		t.Error("未配正则时前缀比对不区分大小写，UNIQ_t_demo 应通过")
	}
}

// 未配正则 => 回退前缀；两者都为空 => 不限制
func TestMatchIndexNamePrefixFallback(t *testing.T) {
	if MatchIndexName("idx_t_demo", "", "IDX_") != true {
		t.Error("前缀 IDX_ 应命中 idx_t_demo")
	}
	if MatchIndexName("uniq_t_demo", "", "IDX_") {
		t.Error("uniq_t_demo 不以 IDX_ 开头，应判不合规")
	}
	if !MatchIndexName("whatever_name", "", "") {
		t.Error("正则与前缀均为空表示不限制，应直接通过")
	}
}

// 非法正则：按不合规处理，不能静默放行
func TestMatchIndexNameInvalidRegex(t *testing.T) {
	if MatchIndexName("uk_abc", `uk_(\w+`, "") {
		t.Error("正则非法时应判不合规，避免静默放行")
	}
}

// 告警文案必须回显“当前实际生效的要求”，否则用户无法判断配置是否生效
func TestIndexNameRequirementText(t *testing.T) {
	if got := IndexNameRequirementText(`uk_\w+`, "UNIQ_"); got != "完整匹配正则`uk_\\w+`" {
		t.Errorf("配了正则时应回显正则，实际：%s", got)
	}
	if got := IndexNameRequirementText("", "IDX_"); got != "以`IDX_`为前缀（不区分大小写）" {
		t.Errorf("未配正则时应回显前缀，实际：%s", got)
	}
	if got := IndexNameRequirementText("  ", "  "); got == "" {
		t.Error("两者皆空时应给出兜底描述，不能返回空串")
	}
}
