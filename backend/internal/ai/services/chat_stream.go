package services

import (
	"context"
	"strings"
	"time"

	"github.com/simajiong/jaysql/internal/ai/adapters"
	aiForms "github.com/simajiong/jaysql/internal/ai/forms"
	"github.com/simajiong/jaysql/internal/global"
)

// ============================================================
// 流式对话（阶段一：只给「直答」路由开流式）
//
// 为什么只开直答：
//   - 直答没有工具、没有写操作草稿，语义最干净，SSE 可以把上游增量原样转发；
//   - 工具路由必须拿到完整响应才能判断是否回灌工具结果、是否产出草稿卡片，
//     半截文本已流到屏幕上就没法改写成「待确认草稿」，
//     因此工具路由仍走非流式，跑完编排后整体下发 done 事件（前端行为与旧版一致）。
//
// 事件协议（事件名 + JSON data）：
//   start  {route, model, provider_name}   已连上模型，开始生成
//   delta  {text}                          增量文本
//   done   LlmChatCompletionResult         收口结果（含降级结果）
//   error  {reason}                        失败且无可展示内容
// ============================================================

// 流式事件名
const (
	ChatStreamEventStart = "start"
	ChatStreamEventDelta = "delta"
	ChatStreamEventDone  = "done"
	ChatStreamEventError = "error"
)

// ChatStreamStartPayload start 事件负载
type ChatStreamStartPayload struct {
	Route        string `json:"route"`
	Model        string `json:"model"`
	ProviderName string `json:"provider_name,omitempty"`
}

// ChatStreamDeltaPayload delta 事件负载
type ChatStreamDeltaPayload struct {
	Text string `json:"text"`
}

// ChatStreamEmitter 事件下发器（由视图层实现为「写 SSE 帧 + Flush」）
type ChatStreamEmitter func(event string, payload any) error

// ChatCompletionStream 流式对话入口。
// ctx 取调用方（HTTP 请求）上下文，客户端断开即中止上游生成。
// 另有一条显式通道：前端携带 run_id 时可由 /chat/cancel 主动掐断 —— 这条通道是必要的，
// 因为工具路由一次编排可能跑好几轮模型调用、中途一个字节都不写响应，
// 只靠「连接断开」在中间隔了代理时未必能及时传导。
func ChatCompletionStream(
	ctx context.Context,
	form *aiForms.LlmChatCompletionForm,
	username string,
	emit ChatStreamEmitter,
) error {
	rt, degraded, err := prepareChatRuntime(form)
	if err != nil {
		return err
	}
	if degraded != nil {
		// 降级（未接服务商/未开开关等）：交给前端按「模型不可用」处理
		return emit(ChatStreamEventDone, degraded)
	}

	// 取消双通道汇合点（同 ChatCompletion）
	runCtx, release := RegisterChatRun(ctx, form.RunID, username)
	defer release()

	streamCtx, cancel := context.WithTimeout(runCtx,
		time.Duration(rt.requestTimeoutSecond+5)*time.Second)
	defer cancel()

	if rt.route != routeDirect {
		result, err := rt.runAgent(streamCtx, username)
		if err != nil {
			return err
		}
		return emit(ChatStreamEventDone, result)
	}
	return rt.streamDirect(streamCtx, username, emit)
}

// streamDirect 直答：上游增量直接转发，平台只做搬运
func (rt *chatRuntime) streamDirect(ctx context.Context, username string, emit ChatStreamEmitter) error {
	providerName := firstNonEmpty(rt.provider.DisplayName, rt.provider.Vendor)
	if err := emit(ChatStreamEventStart, ChatStreamStartPayload{
		Route:        string(routeDirect),
		Model:        rt.model,
		ProviderName: providerName,
	}); err != nil {
		return err
	}

	startedAt := time.Now()
	resp, pErr := adapters.StreamChat(ctx, rt.adapter, rt.cfg,
		buildChatRequest(routeDirect, rt.model, rt.history),
		func(delta string) error {
			return emit(ChatStreamEventDelta, ChatStreamDeltaPayload{Text: delta})
		})

	elapsedMs := int(time.Since(startedAt).Milliseconds())
	content := ""
	model := rt.model
	var usage adapters.Usage
	if resp != nil {
		content = strings.TrimSpace(resp.Content)
		model = firstNonEmpty(resp.Model, rt.model)
		usage = resp.Usage
	}

	if pErr != nil {
		writeLlmCallLog(rt.provider.ProviderID, model, "chat.direct", username,
			elapsedMs, usage.PromptTokens, usage.CompletionTokens, "error", pErr.Category, pErr.Message)
	} else {
		writeLlmCallLog(rt.provider.ProviderID, model, "chat.direct", username,
			elapsedMs, usage.PromptTokens, usage.CompletionTokens, "success", "", "")
	}

	reason := ""
	if pErr != nil {
		reason = pErr.Message
		if pErr.Hint != "" {
			reason += "；" + pErr.Hint
		}
	}

	// 一个字符都没吐出来：按原有降级/报错语义处理
	if content == "" {
		if pErr != nil {
			if rt.degradeToRuleEngine {
				return emit(ChatStreamEventDone, degradeChatResult("模型调用失败："+reason))
			}
			return emit(ChatStreamEventError, map[string]string{"reason": reason})
		}
		return emit(ChatStreamEventDone, &LlmChatCompletionResult{
			Source:       "llm",
			Content:      "模型未返回任何内容，请重试。",
			Model:        model,
			Route:        string(routeDirect),
			ProviderID:   rt.provider.ProviderID,
			ProviderName: providerName,
			LatencyMs:    elapsedMs,
		})
	}

	// 已有内容：即便是中途断开也把已生成内容收口返回，前端保留可见文本
	if pErr != nil {
		global.App.Log.Warnf("ai chat stream interrupted user=%s provider=%s after=%dms: %s",
			username, rt.provider.ProviderID, elapsedMs, reason)
	}
	return emit(ChatStreamEventDone, &LlmChatCompletionResult{
		Source:           "llm",
		Content:          content,
		Model:            model,
		Route:            string(routeDirect),
		ProviderID:       rt.provider.ProviderID,
		ProviderName:     providerName,
		LatencyMs:        elapsedMs,
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		Truncated:        pErr != nil,
	})
}
