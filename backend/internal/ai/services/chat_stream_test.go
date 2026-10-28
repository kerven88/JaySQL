package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/simajiong/jaysql/internal/ai/adapters"
	aiForms "github.com/simajiong/jaysql/internal/ai/forms"
	aiModels "github.com/simajiong/jaysql/internal/ai/models"
	"github.com/simajiong/jaysql/internal/config"
	"github.com/simajiong/jaysql/internal/global"

	"github.com/sirupsen/logrus"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// ============================================================================
// 流式对话（阶段一：只给直答路由开流式）的回归
//
// 事件协议：start → delta（N 次）→ done；降级与失败路径则只有 done / error。
// 覆盖六条：
//   1. AI 总开关关闭 / 无可用服务商 → 单个降级 done（source=rule_engine）；
//   2. 直答路由 → start → delta*N → done，内容与用量收口正确、调用日志落库；
//   3. 直答请求体不带 tools、但带 stream=true（纯转发的硬证据）；
//   4. 直答中途前端断开 → 保留已生成内容，done 标记 truncated，日志记 error；
//   5. 工具路由不走流式 → 编排完成后只发一个 done，请求体带工具清单且无 stream。
// ============================================================================

var llmStreamTestDBSeq int64

// llmStreamTestSchema 四张 LLM 表的建表语句。
//
// 为什么不用 AutoMigrate：commonModels.Model 给每张表都挂了同名索引
// （idx_created_at / idx_updated_at），MySQL 的索引名是表级作用域不冲突，
// 但 SQLite 的索引名是库级作用域，第二张表建索引时必然报
// “index idx_updated_at already exists”。这里直接用 DDL 建表（与
// internal/users/services/identity_test.go 同一套路）。
const llmStreamTestSchema = `
DROP TABLE IF EXISTS insight_llm_providers;
CREATE TABLE insight_llm_providers (
    id integer PRIMARY KEY AUTOINCREMENT,
    created_at datetime, updated_at datetime,
    provider_id char(15) NOT NULL DEFAULT '',
    vendor varchar(32) NOT NULL DEFAULT '',
    display_name varchar(128) NOT NULL DEFAULT '',
    base_url varchar(256) NOT NULL DEFAULT '',
    auth_mode varchar(24) NOT NULL DEFAULT 'bearer',
    api_key_enc varchar(1024) NOT NULL DEFAULT '',
    api_key_mask varchar(48) NOT NULL DEFAULT '',
    extra_config text,
    default_model_id char(15) NOT NULL DEFAULT '',
    capabilities text,
    timeout_seconds integer NOT NULL DEFAULT 60,
    max_retries integer NOT NULL DEFAULT 1,
    status varchar(16) NOT NULL DEFAULT 'draft',
    is_default boolean NOT NULL DEFAULT 0,
    is_shared boolean NOT NULL DEFAULT 0,
    owner varchar(64) NOT NULL DEFAULT '',
    last_validated_at datetime,
    last_validation_status varchar(16) NOT NULL DEFAULT 'unknown',
    last_validation_msg varchar(512) NOT NULL DEFAULT '',
    health_status varchar(16) NOT NULL DEFAULT 'unknown',
    health_checked_at datetime,
    last_latency_ms integer NOT NULL DEFAULT 0,
    remark varchar(256) NOT NULL DEFAULT ''
);

DROP TABLE IF EXISTS insight_llm_vendor_templates;
CREATE TABLE insight_llm_vendor_templates (
    id integer PRIMARY KEY AUTOINCREMENT,
    created_at datetime, updated_at datetime,
    template_id char(15) NOT NULL DEFAULT '',
    vendor varchar(32) NOT NULL DEFAULT '',
    label varchar(64) NOT NULL DEFAULT '',
    category varchar(24) NOT NULL DEFAULT 'custom',
    icon varchar(32) NOT NULL DEFAULT '',
    default_base_url varchar(256) NOT NULL DEFAULT '',
    auth_mode varchar(24) NOT NULL DEFAULT 'bearer',
    auth_header_name varchar(64) NOT NULL DEFAULT '',
    path_chat varchar(128) NOT NULL DEFAULT '',
    path_models varchar(128) NOT NULL DEFAULT '',
    required_fields text,
    extra_fields text,
    default_capabilities text,
    adapter varchar(32) NOT NULL DEFAULT 'openai_compat',
    is_builtin boolean NOT NULL DEFAULT 0,
    is_enabled boolean NOT NULL DEFAULT 1,
    sort_order integer NOT NULL DEFAULT 100,
    definition_hash char(32) NOT NULL DEFAULT '',
    docs_url varchar(256) NOT NULL DEFAULT '',
    remark varchar(256) NOT NULL DEFAULT ''
);

DROP TABLE IF EXISTS insight_llm_models;
CREATE TABLE insight_llm_models (
    id integer PRIMARY KEY AUTOINCREMENT,
    created_at datetime, updated_at datetime,
    model_uid char(15) NOT NULL DEFAULT '',
    provider_id char(15) NOT NULL DEFAULT '',
    model_name varchar(128) NOT NULL DEFAULT '',
    display_name varchar(128) NOT NULL DEFAULT '',
    context_window integer NOT NULL DEFAULT 0,
    max_output_tokens integer NOT NULL DEFAULT 0,
    capabilities text,
    input_price decimal(12,4) NOT NULL DEFAULT 0,
    output_price decimal(12,4) NOT NULL DEFAULT 0,
    is_default boolean NOT NULL DEFAULT 0,
    is_enabled boolean NOT NULL DEFAULT 1,
    source varchar(16) NOT NULL DEFAULT 'auto',
    status varchar(16) NOT NULL DEFAULT 'available',
    last_seen_at datetime,
    remark varchar(256) NOT NULL DEFAULT ''
);

DROP TABLE IF EXISTS insight_llm_call_logs;
CREATE TABLE insight_llm_call_logs (
    id integer PRIMARY KEY AUTOINCREMENT,
    created_at datetime, updated_at datetime,
    provider_id char(15) NOT NULL DEFAULT '',
    model_name varchar(128) NOT NULL DEFAULT '',
    scene varchar(32) NOT NULL DEFAULT '',
    username varchar(64) NOT NULL DEFAULT '',
    latency_ms integer NOT NULL DEFAULT 0,
    prompt_tokens integer NOT NULL DEFAULT 0,
    completion_tokens integer NOT NULL DEFAULT 0,
    total_tokens integer NOT NULL DEFAULT 0,
    status varchar(16) NOT NULL DEFAULT 'success',
    error_code varchar(32) NOT NULL DEFAULT '',
    error_message varchar(512) NOT NULL DEFAULT ''
);
`

// setupLLMStreamEnv 最小可用运行环境：内存库（含四张 LLM 表）+ 日志器。
// 四张表齐备后 writeLlmCallLog 才能真正落库，测试才能断言「留痕」。
func setupLLMStreamEnv(t *testing.T) *gorm.DB {
	t.Helper()
	global.App.Log = logrus.New()
	dsn := fmt.Sprintf("file:llmstream%d?mode=memory&cache=shared", atomic.AddInt64(&llmStreamTestDBSeq, 1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	if err := db.Exec(llmStreamTestSchema).Error; err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	global.App.DB = db
	return db
}

// seedLLMProvider 播种「厂商模板 + 默认模型 + 已启用服务商」，让 prepareChatRuntime 能跑通。
// 刻意让 APIKeyEnc 为空（私有化服务允许无 Key 的场景），从而绕开凭证解密，测试零外部依赖。
func seedLLMProvider(t *testing.T, db *gorm.DB, baseURL string) {
	t.Helper()
	tpl := aiModels.InsightLlmVendorTemplates{
		TemplateID: "vt-streamtest",
		Vendor:     "deepseek-test",
		Label:      "DeepSeek 测试",
		Category:   aiModels.LlmVendorCategoryDomestic,
		AuthMode:   aiModels.LlmAuthBearer,
		Adapter:    adapters.AdapterOpenAICompat,
		IsEnabled:  true,
		SortOrder:  1,
	}
	if err := db.Create(&tpl).Error; err != nil {
		t.Fatalf("播种厂商模板失败: %v", err)
	}
	model := aiModels.InsightLlmModels{
		ModelUID:    "mdl-streamtest",
		ProviderID:  "lp-streamtest",
		ModelName:   "deepseek-chat",
		DisplayName: "DeepSeek Chat",
		IsDefault:   true,
		IsEnabled:   true,
		Source:      aiModels.LlmModelSourceManual,
		Status:      aiModels.LlmModelStatusAvailable,
	}
	if err := db.Create(&model).Error; err != nil {
		t.Fatalf("播种模型失败: %v", err)
	}
	provider := aiModels.InsightLlmProviders{
		ProviderID:     "lp-streamtest",
		Vendor:         "deepseek-test",
		DisplayName:    "DeepSeek 测试",
		BaseURL:        baseURL,
		AuthMode:       aiModels.LlmAuthBearer,
		DefaultModelID: "mdl-streamtest",
		TimeoutSeconds: 30,
		Status:         aiModels.LlmProviderStatusEnabled,
		IsDefault:      true,
	}
	if err := db.Create(&provider).Error; err != nil {
		t.Fatalf("播种服务商失败: %v", err)
	}
}

// eventRecorder 模拟视图层：收集事件名与负载；failOn 用于模拟「前端已断开」
type eventRecorder struct {
	events   []string
	payloads []any
	failOn   string
	failed   bool
}

func (r *eventRecorder) emit(event string, payload any) error {
	r.events = append(r.events, event)
	r.payloads = append(r.payloads, payload)
	if r.failOn != "" && !r.failed && event == r.failOn {
		r.failed = true
		return errors.New("客户端已断开")
	}
	return nil
}

func (r *eventRecorder) doneResult(t *testing.T) *LlmChatCompletionResult {
	t.Helper()
	for i, event := range r.events {
		if event != ChatStreamEventDone {
			continue
		}
		result, ok := r.payloads[i].(*LlmChatCompletionResult)
		if !ok {
			t.Fatalf("done 事件负载应为 *LlmChatCompletionResult，实际 %T", r.payloads[i])
		}
		return result
	}
	t.Fatalf("未收到 done 事件，实际 %v", r.events)
	return nil
}

func (r *eventRecorder) deltaText() string {
	var builder strings.Builder
	for i, event := range r.events {
		if event != ChatStreamEventDelta {
			continue
		}
		payload, ok := r.payloads[i].(ChatStreamDeltaPayload)
		if !ok {
			continue
		}
		builder.WriteString(payload.Text)
	}
	return builder.String()
}

// sseServer 假模型服务端：按行吐 SSE，同时把请求体交给调用方断言
func sseServer(t *testing.T, lines []string) (*httptest.Server, *string) {
	t.Helper()
	gotBody := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for _, line := range lines {
			_, _ = io.WriteString(w, line)
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	t.Cleanup(server.Close)
	return server, &gotBody
}

func sseDelta(text string) string {
	return `data: {"model":"deepseek-chat","choices":[{"delta":{"content":"` + text + `"}}]}` + "\n\n"
}

func chatForm(text string) *aiForms.LlmChatCompletionForm {
	return &aiForms.LlmChatCompletionForm{
		Messages: []aiForms.LlmChatMessage{{Role: "user", Content: text}},
	}
}

// AI 总开关关闭 → 单个降级 done，不碰数据库也不需要服务商
func TestChatCompletionStreamDegradesWhenDisabled(t *testing.T) {
	setupLLMStreamEnv(t)
	global.App.Config = config.Configuration{AI: config.AIConfig{Enabled: false}}

	rec := &eventRecorder{}
	if err := ChatCompletionStream(context.Background(), chatForm("介绍 mysqldump 的实现原理"), "tester", rec.emit); err != nil {
		t.Fatalf("降级路径不应返回接口错误: %v", err)
	}
	if len(rec.events) != 1 || rec.events[0] != ChatStreamEventDone {
		t.Fatalf("降级应只发一个 done 事件，实际 %v", rec.events)
	}
	result := rec.doneResult(t)
	if result.Source != "rule_engine" {
		t.Fatalf("降级结果 source 应为 rule_engine，实际 %q", result.Source)
	}
	if !strings.Contains(result.Reason, "未开启") {
		t.Fatalf("降级原因应指向开关，实际 %q", result.Reason)
	}
}

// 已开启 AI 但库里没有任何已启用服务商 → 降级提示应可操作
func TestChatCompletionStreamDegradesWithoutProvider(t *testing.T) {
	setupLLMStreamEnv(t)
	global.App.Config = config.Configuration{AI: config.AIConfig{Enabled: true}}

	rec := &eventRecorder{}
	if err := ChatCompletionStream(context.Background(), chatForm("介绍 mysqldump 的实现原理"), "tester", rec.emit); err != nil {
		t.Fatalf("降级路径不应返回接口错误: %v", err)
	}
	if len(rec.events) != 1 || rec.events[0] != ChatStreamEventDone {
		t.Fatalf("降级应只发一个 done 事件，实际 %v", rec.events)
	}
	result := rec.doneResult(t)
	if result.Source != "rule_engine" || !strings.Contains(result.Reason, "未找到已启用的模型服务商") {
		t.Fatalf("降级结果不符，实际 source=%s reason=%q", result.Source, result.Reason)
	}
}

// 直答全链路：路由判定 → start → delta*3 → done；内容/用量/日志三处都要对
func TestChatCompletionStreamDirectEmitsStartDeltaDone(t *testing.T) {
	db := setupLLMStreamEnv(t)
	global.App.Config = config.Configuration{AI: config.AIConfig{Enabled: true, RequestTimeoutSecond: 30}}
	server, gotBody := sseServer(t, []string{
		sseDelta("mysqldump "),
		sseDelta("的原理是"),
		sseDelta("先加全局读锁"),
		`data: {"model":"deepseek-chat","choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\n",
		`data: {"model":"deepseek-chat","choices":[],"usage":{"prompt_tokens":12,"completion_tokens":34,"total_tokens":46}}` + "\n\n",
		"data: [DONE]\n\n",
	})
	seedLLMProvider(t, db, server.URL)

	rec := &eventRecorder{}
	err := ChatCompletionStream(context.Background(), chatForm("介绍 mysqldump 的实现原理"), "tester", rec.emit)
	if err != nil {
		t.Fatalf("直答流式不应返回接口错误: %v", err)
	}

	wantEvents := []string{
		ChatStreamEventStart,
		ChatStreamEventDelta, ChatStreamEventDelta, ChatStreamEventDelta,
		ChatStreamEventDone,
	}
	if strings.Join(rec.events, ",") != strings.Join(wantEvents, ",") {
		t.Fatalf("事件顺序应为 start→delta*3→done，实际 %v", rec.events)
	}

	start, ok := rec.payloads[0].(ChatStreamStartPayload)
	if !ok {
		t.Fatalf("start 负载类型错误: %T", rec.payloads[0])
	}
	if start.Route != string(routeDirect) || start.Model != "deepseek-chat" || start.ProviderName != "DeepSeek 测试" {
		t.Fatalf("start 负载应回传路由/模型/服务商，实际 %+v", start)
	}

	want := "mysqldump 的原理是先加全局读锁"
	if rec.deltaText() != want {
		t.Fatalf("delta 拼接应为 %q，实际 %q", want, rec.deltaText())
	}

	result := rec.doneResult(t)
	if result.Source != "llm" || result.Content != want {
		t.Fatalf("done 应携带汇总内容，实际 source=%s content=%q", result.Source, result.Content)
	}
	if result.Route != string(routeDirect) {
		t.Fatalf("done 应标注 direct 路由，实际 %q", result.Route)
	}
	if result.PromptTokens != 12 || result.CompletionTokens != 34 {
		t.Fatalf("done 应带 token 用量，实际 prompt=%d completion=%d", result.PromptTokens, result.CompletionTokens)
	}
	if result.Truncated {
		t.Fatal("正常收口不应标记 truncated")
	}

	// 请求侧：直答必须「不带工具、带 stream」——纯转发的硬证据
	var payload map[string]any
	if err := json.Unmarshal([]byte(*gotBody), &payload); err != nil {
		t.Fatalf("请求体不是合法 JSON: %s", *gotBody)
	}
	if _, ok := payload["tools"]; ok {
		t.Fatalf("直答请求不应包含 tools: %s", *gotBody)
	}
	if _, ok := payload["tool_choice"]; ok {
		t.Fatalf("直答请求不应包含 tool_choice: %s", *gotBody)
	}
	if stream, _ := payload["stream"].(bool); !stream {
		t.Fatalf("直答流式请求必须带 stream=true: %s", *gotBody)
	}

	var logRow aiModels.InsightLlmCallLogs
	if err := db.Where("scene = ?", "chat.direct").First(&logRow).Error; err != nil {
		t.Fatalf("应落库一条 chat.direct 调用日志: %v", err)
	}
	if logRow.Status != "success" || logRow.Username != "tester" || logRow.ModelName != "deepseek-chat" {
		t.Fatalf("调用日志内容不符，实际 %+v", logRow)
	}
	if logRow.PromptTokens != 12 || logRow.CompletionTokens != 34 || logRow.TotalTokens != 46 {
		t.Fatalf("调用日志应记录用量，实际 %+v", logRow)
	}
}

// 直答中途前端断开：已生成内容不丢，done 打 truncated，日志记 error
func TestChatCompletionStreamDirectMarksTruncated(t *testing.T) {
	db := setupLLMStreamEnv(t)
	global.App.Config = config.Configuration{AI: config.AIConfig{Enabled: true, RequestTimeoutSecond: 30}}
	server, _ := sseServer(t, []string{
		sseDelta("第一段"),
		sseDelta("第二段"),
		sseDelta("第三段"),
		"data: [DONE]\n\n",
	})
	seedLLMProvider(t, db, server.URL)

	rec := &eventRecorder{failOn: ChatStreamEventDelta}
	// 前端断开只影响「继续喂增量」：上游停读、已生成内容收口成 done，
	// 因此入口不报错（视图层写 SSE 到已断开的连接本就没有意义）。
	if err := ChatCompletionStream(context.Background(), chatForm("介绍 mysqldump 的实现原理"), "tester", rec.emit); err != nil {
		t.Fatalf("中断应走「保留内容」分支而不是抛错: %v", err)
	}
	if !rec.failed {
		t.Fatal("测试前置条件未生效：应有一次 delta 下发失败")
	}

	result := rec.doneResult(t)
	if !result.Truncated {
		t.Fatal("中断后 done 必须标记 truncated")
	}
	if result.Content != "第一段" {
		t.Fatalf("已生成内容应保留，实际 %q", result.Content)
	}

	var logRow aiModels.InsightLlmCallLogs
	if err := db.Where("scene = ?", "chat.direct").First(&logRow).Error; err != nil {
		t.Fatalf("中断也应留痕: %v", err)
	}
	if logRow.Status != "error" {
		t.Fatalf("中断的调用日志 status 应为 error，实际 %q", logRow.Status)
	}
}

// 工具路由保持非流式：编排跑完只发一个 done，请求体带工具清单且无 stream
func TestChatCompletionStreamToolsRouteEmitsSingleDone(t *testing.T) {
	db := setupLLMStreamEnv(t)
	global.App.Config = config.Configuration{AI: config.AIConfig{Enabled: true, RequestTimeoutSecond: 30}}

	var gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"model": "deepseek-chat",
			"choices": [{"message": {"content": "当前没有查到相关数据。"}, "finish_reason": "stop"}],
			"usage": {"prompt_tokens": 100, "completion_tokens": 20, "total_tokens": 120}
		}`)
	}))
	t.Cleanup(server.Close)
	seedLLMProvider(t, db, server.URL)

	rec := &eventRecorder{}
	if err := ChatCompletionStream(context.Background(), chatForm("昨天备份成功了吗"), "tester", rec.emit); err != nil {
		t.Fatalf("工具路由不应返回接口错误: %v", err)
	}
	if len(rec.events) != 1 || rec.events[0] != ChatStreamEventDone {
		t.Fatalf("工具路由应只发一个 done（不做流式），实际 %v", rec.events)
	}

	result := rec.doneResult(t)
	if result.Route != string(routeTools) {
		t.Fatalf("工具路由结果应标注 tools，实际 %q", result.Route)
	}
	if !strings.Contains(result.Content, "没有查到相关数据") {
		t.Fatalf("done 应携带模型回答，实际 %q", result.Content)
	}
	if !strings.Contains(gotBody, `"tools"`) || !strings.Contains(gotBody, `"tool_choice":"auto"`) {
		t.Fatalf("工具路由请求体必须带工具清单: %s", gotBody)
	}
	if strings.Contains(gotBody, `"stream":true`) {
		t.Fatalf("工具路由不应申明流式: %s", gotBody)
	}

	var logRow aiModels.InsightLlmCallLogs
	if err := db.Where("scene = ?", "chat").First(&logRow).Error; err != nil {
		t.Fatalf("工具路由首个调用应落库 scene=chat 日志: %v", err)
	}
	if logRow.ProviderID != "lp-streamtest" {
		t.Fatalf("调用日志应记录 ProviderID，实际 %q", logRow.ProviderID)
	}
}
