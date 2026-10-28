package services

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/simajiong/jaysql/internal/ai/adapters"
	aiForms "github.com/simajiong/jaysql/internal/ai/forms"
	aiModels "github.com/simajiong/jaysql/internal/ai/models"
	"github.com/simajiong/jaysql/internal/ai/tools"
	"github.com/simajiong/jaysql/internal/global"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ============================================================
// 运行时对话路由（P2 收口）
//
// 前置条件回顾：P1 只解决了「配置存得下、验得过」，对话链路当时
// 仍由前端本地规则模拟。本文件补上「配置真正被消费」的最后一环：
//
//	默认服务商解析 → 凭证解密 → 适配器调用 → 调用日志 → 降级策略
//
// 降级语义（ai.degrade_to_rule_engine）：
//   - 未接入服务商 / 调用失败时，返回 source=rule_engine（HTTP 200），
//     由前端回落本地规则引擎，保证对话永不白屏；
//   - degrade 关闭时才把错误作为接口错误抛出。
// ============================================================

// ============================================================
// 平台对话的系统提示词（服务端统一注入，不开放给调用方）
//
// 两套文案按路由分开：挂了工具的用 with-tools 版，通识直答用 direct 版。
// direct 版刻意不提「你有工具」，避免模型在无工具上下文里承诺去查平台数据。
// ============================================================

// chatSystemPromptWithTools 工具路由：允许模型调用平台只读/写工具
const chatSystemPromptWithTools = "你是 JaySQL 数据库管理平台的 AI 助手，服务对象是企业的 DBA 与开发人员。" +
	"请用简体中文、简洁专业地回答数据库相关的问题（MySQL/PostgreSQL/备份恢复/SQL 优化等）。" +
	"平台已为你提供一组工具，可直接查询平台的真实数据（实例清单与规格画像、库表清单与表结构、工单、备份执行结果、监控告警、慢查询等）。" +
	"凡涉及平台具体数据的问题，必须先调用相应工具拿到真实结果再回答，禁止凭空编造或使用示例数据；" +
	"工具返回为空或查不到时，直接说明「当前没有查到相关数据」，不要臆测。" +
	"与平台数据无关的通用技术问题（某工具的实现原理、语法用法、方案建议、概念解释等）直接用你自己的知识回答，不要调用工具。" +
	"涉及写操作（如创建工单、创建备份任务）时，你只能生成待用户确认的草稿，绝不自行提交。" +
	"工具返回 no_data_grant（无数据授权）时，请把返回里的 message 与 how_to_apply 原样转述给用户，" +
	"不要改用其它工具、不要换问法绕开权限，也不要依据常识猜测表结构或数据内容。"

// chatSystemPromptDirect 直答路由：平台只做转发，不挂任何工具
const chatSystemPromptDirect = "你是 JaySQL 数据库管理平台的 AI 助手，服务对象是企业的 DBA 与开发人员。" +
	"请用简体中文、简洁专业地回答数据库相关的问题（MySQL/PostgreSQL/备份恢复/SQL 优化等）。" +
	"本次对话没有挂载平台数据查询能力：如果用户的问题需要查询平台的实时数据（实例、工单、备份、告警、慢查询等），" +
	"请直接说明你查不到平台数据，并建议用户把具体对象说清楚后重新提问，绝不编造平台数据或示例数据。" +
	"涉及平台自身配置（如底层使用的大模型、部署环境）时同样不要臆测，说明以平台「系统管理 → AI 能力」配置页为准。" +
	"涉及写操作（如创建工单、创建备份任务）时，只说明需要用户明确描述需求后由平台生成草稿确认，不要声称已经提交。"

// chatMaxTokens 单次模型调用的输出上限（含工具编排的多轮调用）
const chatMaxTokens = 2048

// LlmChatCompletionResult 对话结果。
// Source=llm 表示真实模型返回；source=rule_engine 表示已降级（前端按不可用处理）。
// Route=direct 表示通识问题直答（未挂工具，系统只做转发）；tools 表示走了工具编排。
// Traces 为真实工具调用轨迹；PendingAction 为写工具产出的待确认草稿（未落库）。
type LlmChatCompletionResult struct {
	Source           string             `json:"source"`
	Reason           string             `json:"reason,omitempty"`
	Content          string             `json:"content,omitempty"`
	Model            string             `json:"model,omitempty"`
	Route            string             `json:"route,omitempty"`
	ProviderID       string             `json:"provider_id,omitempty"`
	ProviderName     string             `json:"provider_name,omitempty"`
	LatencyMs        int                `json:"latency_ms,omitempty"`
	PromptTokens     int                `json:"prompt_tokens,omitempty"`
	CompletionTokens int                `json:"completion_tokens,omitempty"`
	Traces           []ToolTrace        `json:"traces,omitempty"`
	PendingAction    *tools.DraftAction `json:"pending_action,omitempty"`
	// Truncated：流式响应中途断开、内容可能不完整（仅流式路径会出现）
	Truncated bool `json:"truncated,omitempty"`
}

// ChatCompletion 运行时对话主入口（非流式：一次性返回完整回答）。
//
// ctx 必须由调用方传入 HTTP 请求上下文：早期这里写死 context.Background()，
// 导致用户关掉页面或点取消后，服务端的工具编排仍会一路跑完（最长 5 轮模型调用），
// 既白烧 token 又让「已取消」变成假象。
func ChatCompletion(ctx context.Context, form *aiForms.LlmChatCompletionForm, username string) (*LlmChatCompletionResult, error) {
	rt, degraded, err := prepareChatRuntime(form)
	if err != nil {
		return nil, err
	}
	if degraded != nil {
		return degraded, nil
	}

	// 取消双通道汇合点：请求 ctx（客户端断开）+ run_id（显式取消接口）都落到同一个 cancel。
	runCtx, release := RegisterChatRun(ctx, form.RunID, username)
	defer release()

	// 外层超时保护：平台配置 request_timeout_seconds 兜底。
	// 一次提问可能包含多轮工具调用，这里额外留出余量。
	callCtx, cancel := context.WithTimeout(runCtx,
		time.Duration(rt.requestTimeoutSecond+5)*time.Second)
	defer cancel()

	return rt.runAgent(callCtx, username)
}

// runAgent 跑一次 Agent 编排（工具路由）并收口成对外结果
func (rt *chatRuntime) runAgent(ctx context.Context, username string) (*LlmChatCompletionResult, error) {
	callStartedAt := time.Now()
	loopResult, pErr := runAgentLoop(
		ctx, rt.adapter, rt.cfg, rt.model, rt.history,
		&tools.Context{Username: username},
		rt.provider.ProviderID, username, rt.route,
		rt.callLogger(username),
	)

	if pErr != nil {
		reason := pErr.Message
		if pErr.Hint != "" {
			reason += "；" + pErr.Hint
		}
		if rt.degradeToRuleEngine {
			return degradeChatResult("模型调用失败：" + reason), nil
		}
		return nil, pErr
	}

	global.App.Log.Infof("ai chat route=%s tools=%d user=%s provider=%s model=%s prompt_tokens=%d",
		rt.route, len(loopResult.Traces), username, rt.provider.ProviderID, loopResult.Model, loopResult.Usage.PromptTokens)

	return &LlmChatCompletionResult{
		Source:           "llm",
		Content:          loopResult.Content,
		Model:            loopResult.Model,
		Route:            string(loopResult.Route),
		ProviderID:       rt.provider.ProviderID,
		ProviderName:     firstNonEmpty(rt.provider.DisplayName, rt.provider.Vendor),
		LatencyMs:        int(time.Since(callStartedAt).Milliseconds()),
		PromptTokens:     loopResult.Usage.PromptTokens,
		CompletionTokens: loopResult.Usage.CompletionTokens,
		Traces:           loopResult.Traces,
		PendingAction:    loopResult.Pending,
	}, nil
}

// degradeChatResult 降级结果（source=rule_engine，前端按「模型不可用」处理）
func degradeChatResult(reason string) *LlmChatCompletionResult {
	return &LlmChatCompletionResult{Source: "rule_engine", Reason: reason}
}

// chatRuntime 一次对话所需的运行时就绪状态：服务商 → 适配器 → 模型 → 路由。
// 流式与非流式两条入口共用同一套前置校验，避免两处口径漂移。
type chatRuntime struct {
	degradeToRuleEngine  bool
	requestTimeoutSecond int
	provider             *aiModels.InsightLlmProviders
	adapter              adapters.ProviderAdapter
	cfg                  adapters.ProviderConfig
	model                string
	history              []adapters.ChatMessage
	route                chatRoute
}

// prepareChatRuntime 解析并校验运行时依赖。
// 第二个返回值非 nil 表示「无需报错，直接按降级结果返回」（source=rule_engine）。
func prepareChatRuntime(form *aiForms.LlmChatCompletionForm) (*chatRuntime, *LlmChatCompletionResult, error) {
	settings := global.App.Config.AISettings()

	// 平台总开关（老配置文件没有 ai 节点时 Enabled=false，同样走降级语义）
	if !settings.Enabled {
		return nil, degradeChatResult("AI 能力未开启（ai.enabled=false），请在配置中开启后重试"), nil
	}

	provider, tpl, err := resolveChatProvider(form.ProviderID)
	if err != nil {
		return nil, nil, err
	}
	if provider == nil {
		return nil, degradeChatResult("未找到已启用的模型服务商，请到「系统管理 → AI 能力」完成接入"), nil
	}

	cfg, err := buildRuntimeConfig(provider, tpl)
	if err != nil {
		// 凭证解密失败属于平台侧配置问题：降级保对话可用，但必须留痕
		global.App.Log.Warnf("llm chat build runtime config provider=%s failed: %v", provider.ProviderID, err)
		if settings.DegradeToRuleEngine {
			return nil, degradeChatResult("模型凭证不可用：" + err.Error()), nil
		}
		return nil, nil, err
	}
	cfg.TimeoutSeconds = normalizeTimeout(provider.TimeoutSeconds)

	if err := validateProviderFormat(cfg, tpl); err != nil {
		if settings.DegradeToRuleEngine {
			return nil, degradeChatResult("服务商配置未通过格式校验：" + err.Error()), nil
		}
		return nil, nil, err
	}

	adapter, err := adapters.Resolve(tpl.Adapter)
	if err != nil {
		if settings.DegradeToRuleEngine {
			return nil, degradeChatResult(err.Error()), nil
		}
		return nil, nil, err
	}

	model := resolveChatModelName(provider)
	if strings.TrimSpace(model) == "" {
		msg := "未指定调用模型：请到「AI 能力 → 模型管理」为该服务商设置默认模型"
		if settings.DegradeToRuleEngine {
			return nil, degradeChatResult(msg), nil
		}
		return nil, nil, errors.New(msg)
	}

	history := make([]adapters.ChatMessage, 0, len(form.Messages))
	for _, m := range form.Messages {
		history = append(history, adapters.ChatMessage{Role: m.Role, Content: m.Content})
	}

	return &chatRuntime{
		degradeToRuleEngine:  settings.DegradeToRuleEngine,
		requestTimeoutSecond: settings.RequestTimeoutSecond,
		provider:             provider,
		adapter:              adapter,
		cfg:                  cfg,
		model:                model,
		history:              history,
		// 提问路由：通识问题直答（不挂工具，纯转发），涉及平台数据才进工具编排
		route: decideChatRoute(history),
	}, nil, nil
}

// callLogger 构造调用日志回调（工具编排的多轮调用逐个落库）
func (rt *chatRuntime) callLogger(username string) func(scene string, resp *adapters.ChatResponse, pErr *adapters.ProviderError, elapsedMs int) {
	return func(scene string, resp *adapters.ChatResponse, callErr *adapters.ProviderError, elapsedMs int) {
		if callErr != nil {
			writeLlmCallLog(rt.provider.ProviderID, rt.model, scene, username,
				elapsedMs, 0, 0, "error", callErr.Category, callErr.Message)
			return
		}
		writeLlmCallLog(rt.provider.ProviderID, firstNonEmpty(resp.Model, rt.model), scene, username,
			resp.LatencyMs, resp.Usage.PromptTokens, resp.Usage.CompletionTokens, "success", "", "")
	}
}

// CurrentUsername 从 JWT claims 中取当前用户名（供 views 层使用的导出包装）
func CurrentUsername(c *gin.Context) string {
	return currentUsername(c)
}

// resolveChatProvider 解析对话用服务商。
// 指定 provider_id：必须存在且 enabled；未指定：默认服务商 → 任一 enabled。
// 返回 nil provider 表示「没有可用服务商」（降级语义，不是错误）。
func resolveChatProvider(providerID string) (*aiModels.InsightLlmProviders, *aiModels.InsightLlmVendorTemplates, error) {
	var provider aiModels.InsightLlmProviders

	load := func(query *gorm.DB) error {
		return query.Where("status = ?", aiModels.LlmProviderStatusEnabled).
			Order("is_default desc, id asc").First(&provider).Error
	}

	id := strings.TrimSpace(providerID)
	err := load(func() *gorm.DB {
		q := global.App.DB
		if id != "" {
			q = q.Where("provider_id = ?", id)
		} else {
			q = q.Where("is_default = ?", true)
		}
		return q
	}())

	if errors.Is(err, gorm.ErrRecordNotFound) && id == "" {
		// 没有设默认：放宽到任一已启用服务商（多服务商场景下保证开箱即用）
		err = load(global.App.DB)
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if id != "" {
			return nil, nil, errors.New("指定的服务商不存在或未启用")
		}
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}

	tpl, err := loadVendorTemplate(provider.Vendor)
	if err != nil {
		return nil, nil, err
	}
	return &provider, tpl, nil
}

// resolveChatModelName 对话用模型名：默认模型 → extra.model（设默认时已写回）
func resolveChatModelName(provider *aiModels.InsightLlmProviders) string {
	if strings.TrimSpace(provider.DefaultModelID) != "" {
		var m aiModels.InsightLlmModels
		if err := global.App.DB.Where("model_uid = ?", provider.DefaultModelID).First(&m).Error; err == nil {
			if name := strings.TrimSpace(m.ModelName); name != "" {
				return name
			}
		}
	}
	return adapters.ExtraString(decodeExtraConfig(provider.ExtraConfig), "model")
}

// callProviderChat 适配器无状态的通用对话调用。
// BuildChatRequest / ParseChatResponse / MapError 都在适配器契约里，
// 因此这一段对所有厂商适配器通用，无需按厂商分支。
func callProviderChat(ctx context.Context, adapter adapters.ProviderAdapter,
	cfg adapters.ProviderConfig, req adapters.ChatRequest) (*adapters.ChatResponse, *adapters.ProviderError) {

	httpReq, err := adapter.BuildChatRequest(cfg, req)
	if err != nil {
		var pErr *adapters.ProviderError
		if errors.As(err, &pErr) {
			return nil, pErr
		}
		return nil, &adapters.ProviderError{Category: adapters.ErrCategoryConfig, Message: err.Error()}
	}
	httpReq = httpReq.WithContext(ctx)

	client := adapters.NewHTTPClient(cfg.TimeoutSeconds)
	start := time.Now()
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, adapters.ClassifyTransportError(err)
	}
	body, err := adapters.ReadBody(resp)
	if err != nil {
		return nil, &adapters.ProviderError{Category: adapters.ErrCategoryHTTP, Message: "读取响应失败", Raw: err.Error()}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, adapter.MapError(resp.StatusCode, body)
	}
	parsed, err := adapter.ParseChatResponse(body)
	if err != nil {
		var pErr *adapters.ProviderError
		if errors.As(err, &pErr) {
			return nil, pErr
		}
		return nil, &adapters.ProviderError{Category: adapters.ErrCategoryHTTP, Message: err.Error()}
	}
	parsed.LatencyMs = int(time.Since(start).Milliseconds())
	return parsed, nil
}
