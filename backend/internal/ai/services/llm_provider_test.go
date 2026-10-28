package services

import (
	"strings"
	"testing"

	"github.com/simajiong/jaysql/internal/ai/adapters"
	aiForms "github.com/simajiong/jaysql/internal/ai/forms"
	"github.com/simajiong/jaysql/internal/ai/seeds"
)

// TestMaskAPIKey 掩码规则：永不回显完整密钥
func TestMaskAPIKey(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		want     string
		contains string
	}{
		{"标准长度", "sk-abcdefghijklmn", "sk-a***klmn", ""},
		{"短密钥全掩盖", "sk-1234", "*******", ""},
		{"空值", "   ", "", ""},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			got := maskAPIKey(item.input)
			if got != item.want {
				t.Fatalf("maskAPIKey(%q) = %q, want %q", item.input, got, item.want)
			}
			// 掩码里不允许出现原始密钥的完整中间段（仅对足够长的密钥检查）
			plain := strings.TrimSpace(item.input)
			if len(plain) > 8 {
				middle := plain[4 : len(plain)-4]
				if middle != "" && strings.Contains(got, middle) {
					t.Fatalf("掩码 %q 泄露了密钥中间段", got)
				}
			}
		})
	}
}

// TestVendorTemplateDefinitionHash 定义指纹：同定义同值，改定义则变化
func TestVendorTemplateDefinitionHash(t *testing.T) {
	base := seeds.BuiltinVendorTemplates[0]
	hashA := vendorTemplateDefinitionHash(base)
	if hashA == "" {
		t.Fatal("定义指纹不应为空")
	}
	if hashA != vendorTemplateDefinitionHash(base) {
		t.Fatal("同一份定义必须得到相同的指纹")
	}

	changed := base
	changed.DefaultBaseURL = "https://example.internal/v1"
	if hashA == vendorTemplateDefinitionHash(changed) {
		t.Fatal("定义变化后指纹必须变化，否则播种不会更新")
	}

	// 所有内置模板的定义指纹必须互不相同（避免播种互相覆盖判断失误）
	seen := map[string]string{}
	for _, def := range seeds.BuiltinVendorTemplates {
		hash := vendorTemplateDefinitionHash(def)
		if prev, ok := seen[hash]; ok {
			t.Fatalf("模板 `%s` 与 `%s` 的定义指纹相同", def.Vendor, prev)
		}
		seen[hash] = def.Vendor
	}
}

// TestBuiltinTemplatesAreWellFormed 内置模板自身必须合法：
// 适配器存在、字段类型在白名单内、必填项有定义
func TestBuiltinTemplatesAreWellFormed(t *testing.T) {
	validTypes := map[string]bool{"string": true, "password": true, "number": true, "boolean": true, "select": true}
	validAdapters := map[string]bool{
		adapters.AdapterOpenAICompat: true,
		adapters.AdapterAzureOpenAI:  true,
		adapters.AdapterAnthropic:    true,
		adapters.AdapterOpenAICustom: true,
	}

	if len(seeds.BuiltinVendorTemplates) == 0 {
		t.Fatal("内置模板不应为空")
	}

	for _, def := range seeds.BuiltinVendorTemplates {
		if !validAdapters[def.Adapter] {
			t.Fatalf("模板 `%s` 使用了未注册的适配器 `%s`", def.Vendor, def.Adapter)
		}
		if !strings.HasPrefix(def.PathChat, "/") {
			t.Fatalf("模板 `%s` 的对话路径必须以 / 开头", def.Vendor)
		}
		keys := map[string]bool{}
		for _, field := range def.ExtraFields {
			if !validTypes[field.Type] {
				t.Fatalf("模板 `%s` 的字段 `%s` 类型 `%s` 不在白名单内", def.Vendor, field.Key, field.Type)
			}
			keys[field.Key] = true
		}
		baseFields := map[string]bool{"base_url": true, "api_key": true, "auth_mode": true, "auth_header_name": true}
		for _, required := range def.RequiredFields {
			if baseFields[required] || keys[required] {
				continue
			}
			t.Fatalf("模板 `%s` 的必填字段 `%s` 未在差异化字段中定义", def.Vendor, required)
		}
	}
}

// TestValidateLlmVendorTemplateForm 模板落库前的 schema 校验
func TestValidateLlmVendorTemplateForm(t *testing.T) {
	valid := &aiForms.LlmVendorTemplateForm{
		Vendor:         "my_gateway",
		Label:          "自研网关",
		Adapter:        adapters.AdapterOpenAICustom,
		DefaultBaseURL: "https://llm.internal.example.com/v1",
		PathChat:       "/chat/completions",
		RequiredFields: []string{"base_url", "tenant"},
		ExtraFields: []map[string]any{
			{"key": "tenant", "label": "租户", "type": "string", "required": true},
		},
	}
	if err := validateLlmVendorTemplateForm(valid, true); err != nil {
		t.Fatalf("合法模板不应报错: %v", err)
	}

	badType := *valid
	badType.ExtraFields = []map[string]any{{"key": "tenant", "label": "租户", "type": "file"}}
	if err := validateLlmVendorTemplateForm(&badType, true); err == nil {
		t.Fatal("非法字段类型必须被拒绝")
	}

	missingRef := *valid
	missingRef.RequiredFields = []string{"base_url", "not_defined"}
	if err := validateLlmVendorTemplateForm(&missingRef, true); err == nil {
		t.Fatal("必填字段引用不存在的 key 必须被拒绝")
	}

	badAdapter := *valid
	badAdapter.Adapter = "not_registered"
	if err := validateLlmVendorTemplateForm(&badAdapter, true); err == nil {
		t.Fatal("未注册的适配器必须被拒绝")
	}

	badVendorName := *valid
	badVendorName.Vendor = "Bad-Name!"
	if err := validateLlmVendorTemplateForm(&badVendorName, true); err == nil {
		t.Fatal("非法厂商标识必须被拒绝")
	}

	badPath := *valid
	badPath.PathChat = "chat/completions"
	if err := validateLlmVendorTemplateForm(&badPath, true); err == nil {
		t.Fatal("路径模板缺少前导斜杠必须被拒绝")
	}
}

// TestBuildURLReplacesPlaceholders Azure 风格端点占位符替换
func TestBuildURLReplacesPlaceholders(t *testing.T) {
	cfg := adapters.ProviderConfig{
		BaseURL: "https://{resource}.openai.azure.com",
		Extra: map[string]any{
			"resource":    "my-aoai",
			"deployment":  "gpt-4o",
			"api_version": "2024-06-01",
		},
	}
	full, err := adapters.BuildURL(cfg,
		"/openai/deployments/{deployment}/chat/completions?api-version={api_version}")
	if err != nil {
		t.Fatalf("占位符替换失败: %v", err)
	}
	want := "https://my-aoai.openai.azure.com/openai/deployments/gpt-4o/chat/completions?api-version=2024-06-01"
	if full != want {
		t.Fatalf("BuildURL = %q, want %q", full, want)
	}

	// 占位符未替换时必须报错（而不是打到一个坏地址）
	if _, err := adapters.BuildURL(cfg, "/openai/deployments/{missing}/chat"); err == nil {
		t.Fatal("存在未替换占位符时必须报错")
	}

	// 非 http(s) 协议必须被拒绝
	badScheme := adapters.ProviderConfig{BaseURL: "ftp://example.com", Extra: map[string]any{}}
	if _, err := adapters.BuildURL(badScheme, "/chat"); err == nil {
		t.Fatal("非 http(s) 端点必须被拒绝")
	}
}

// TestResolveAdapter 适配器注册表
func TestResolveAdapter(t *testing.T) {
	for _, name := range []string{
		adapters.AdapterOpenAICompat,
		adapters.AdapterAzureOpenAI,
		adapters.AdapterAnthropic,
		adapters.AdapterOpenAICustom,
	} {
		if _, err := adapters.Resolve(name); err != nil {
			t.Fatalf("适配器 `%s` 未注册: %v", name, err)
		}
	}
	if _, err := adapters.Resolve("nonexistent"); err == nil {
		t.Fatal("不存在的适配器必须报错")
	}
	// 空名回退到 openai_compat
	if _, err := adapters.Resolve(""); err != nil {
		t.Fatalf("空适配器名应回退到 openai_compat: %v", err)
	}
}
