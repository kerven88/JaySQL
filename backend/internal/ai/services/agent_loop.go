package services

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/simajiong/jaysql/internal/ai/adapters"
	"github.com/simajiong/jaysql/internal/ai/tools"
)

// ============================================================
// Agent 编排循环
//
// 一次用户提问可能包含多步工具调用，标准做法是「循环」而不是单次问答：
//
//	模型（带工具清单）→ 返回 tool_calls → 平台执行 → 结果回灌 → 模型再推理
//
// 两条硬约束（对齐行业共识）：
//   - maxAgentSteps 上限：防止模型陷入「调工具→再调工具」的死循环
//   - 写工具产出的草稿只在第一轮生成，后续轮次只回灌「等待用户确认」，
//     绝不因为在循环里重复出现而真正落库
// ============================================================

// maxAgentSteps 单轮对话内允许的最大工具调用轮数
const maxAgentSteps = 5

// ToolTrace 一步工具调用轨迹（前端右栏「工具调用」与消息内轨迹共用这一结构）
type ToolTrace struct {
	Tool   string `json:"tool"`
	Arg    string `json:"arg"`
	Ms     string `json:"ms"`
	Status string `json:"status,omitempty"`
}

type agentLoopResult struct {
	Content string
	Model   string
	Route   chatRoute
	Traces  []ToolTrace
	Pending *tools.DraftAction
	Usage   adapters.Usage
}

// chatToolDefinitions 下发给模型的平台工具清单（写工具一并挂着，只产草稿不落库）
func chatToolDefinitions() []adapters.ToolDef {
	return tools.Definitions(true)
}

// runAgentLoop 执行完整编排循环。
// route=routeDirect 时不挂任何工具，单次调用把模型回复原样返回（系统只做转发）。
// 返回的 *adapters.ProviderError 表示模型调用本身失败（由调用方决定降级还是报错）。
func runAgentLoop(
	ctx context.Context,
	adapter adapters.ProviderAdapter,
	cfg adapters.ProviderConfig,
	model string,
	history []adapters.ChatMessage,
	toolCtx *tools.Context,
	providerID string,
	username string,
	route chatRoute,
	onCall func(scene string, resp *adapters.ChatResponse, pErr *adapters.ProviderError, elapsedMs int),
) (*agentLoopResult, *adapters.ProviderError) {

	if route == routeDirect {
		return runDirectChat(ctx, adapter, cfg, model, history, onCall)
	}

	messages := append([]adapters.ChatMessage(nil), history...)

	result := &agentLoopResult{
		Traces: make([]ToolTrace, 0),
		Model:  model,
		Route:  route,
	}

	for step := 0; step < maxAgentSteps; step++ {
		req := buildChatRequest(routeTools, model, messages)

		scene := "chat"
		if step > 0 {
			scene = "chat.tool"
		}

		startedAt := time.Now()
		resp, pErr := callProviderChat(ctx, adapter, cfg, req)
		if pErr != nil {
			if onCall != nil {
				onCall(scene, nil, pErr, int(time.Since(startedAt).Milliseconds()))
			}
			return nil, pErr
		}
		if onCall != nil {
			onCall(scene, resp, nil, int(time.Since(startedAt).Milliseconds()))
		}

		result.Model = firstNonEmpty(resp.Model, model)
		result.Usage.PromptTokens += resp.Usage.PromptTokens
		result.Usage.CompletionTokens += resp.Usage.CompletionTokens
		result.Usage.TotalTokens += resp.Usage.TotalTokens

		// 没有工具调用 → 模型给出最终回答，循环结束
		if len(resp.ToolCalls) == 0 {
			result.Content = resp.Content
			break
		}

		// 把「模型要求调什么」原样记入历史，保持多轮上下文一致
		messages = append(messages, adapters.ChatMessage{
			Role:      "assistant",
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		})

		for _, call := range resp.ToolCalls {
			callStart := time.Now()
			args := tools.ParseArguments(call.Arguments)
			summary := summarizeArgs(args)

			tool, ok := tools.Get(call.Name)
			if !ok {
				result.Traces = append(result.Traces, ToolTrace{
					Tool: call.Name, Arg: "未注册的工具", Ms: msText(time.Since(callStart)), Status: "error",
				})
				messages = append(messages, adapters.ChatMessage{
					Role: "tool", ToolCallID: call.ID,
					Content: "该工具不存在，请改用平台提供的其他工具，不要凭空编造数据。",
				})
				continue
			}

			// ---- 写工具：只产出草稿，绝不落库 ----
			if tool.Write {
				if result.Pending != nil {
					messages = append(messages, adapters.ChatMessage{
						Role: "tool", ToolCallID: call.ID,
						Content: "已生成一张待确认草稿，请勿重复调用，等待用户确认。",
					})
					continue
				}
				value, err := tools.Execute(toolCtx, call.Name, args)
				if err != nil {
					result.Traces = append(result.Traces, ToolTrace{
						Tool: call.Name, Arg: summary, Ms: msText(time.Since(callStart)), Status: "error",
					})
					messages = append(messages, adapters.ChatMessage{
						Role: "tool", ToolCallID: call.ID, Content: "草稿生成失败：" + err.Error(),
					})
					continue
				}
				draft, isDraft := value.(*tools.DraftAction)
				if !isDraft {
					result.Traces = append(result.Traces, ToolTrace{
						Tool: call.Name, Arg: summary, Ms: msText(time.Since(callStart)), Status: "error",
					})
					messages = append(messages, adapters.ChatMessage{
						Role: "tool", ToolCallID: call.ID, Content: "写操作未返回草稿，已阻止执行。",
					})
					continue
				}
				result.Pending = draft
				result.Traces = append(result.Traces, ToolTrace{
					Tool: call.Name, Arg: summary, Ms: msText(time.Since(callStart)),
				})
				messages = append(messages, adapters.ChatMessage{
					Role: "tool", ToolCallID: call.ID,
					Content: "草稿已生成，等待用户确认后落库。请用一两句话说明这张草稿的关键内容，不要重复调用本工具。",
				})
				continue
			}

			// ---- 只读工具：直接执行并回灌结果 ----
			value, err := tools.Execute(toolCtx, call.Name, args)
			if err != nil {
				result.Traces = append(result.Traces, ToolTrace{
					Tool: call.Name, Arg: summary, Ms: msText(time.Since(callStart)), Status: "error",
				})
				messages = append(messages, adapters.ChatMessage{
					Role: "tool", ToolCallID: call.ID, Content: "执行失败：" + err.Error(),
				})
				continue
			}
			result.Traces = append(result.Traces, ToolTrace{
				Tool: call.Name, Arg: summary, Ms: msText(time.Since(callStart)),
			})
			messages = append(messages, adapters.ChatMessage{
				Role: "tool", ToolCallID: call.ID, Content: tools.ResultText(value),
			})
		}

	}

	if strings.TrimSpace(result.Content) == "" {
		switch {
		case result.Pending != nil:
			result.Content = pendingDraftFallbackText(result.Pending)
		case len(result.Traces) > 0:
			result.Content = "已获取到数据，但模型未返回总结。请重试或换个问法。"
		default:
			result.Content = "模型未返回任何内容，请重试。"
		}
	}
	return result, nil
}

// pendingDraftFallbackText 编排结束但模型没给总结时的兜底文案：按草稿类型说清「要确认什么」
func pendingDraftFallbackText(draft *tools.DraftAction) string {
	if draft != nil && draft.Type == "create_backup" {
		return "已为你准备好备份任务草稿，请核对下方内容后确认创建。"
	}
	return "已为你准备好工单草稿，请核对下方内容后确认创建。"
}

// ------------------------------------------------------------------
// 直答路径（系统只做转发）
// ------------------------------------------------------------------

// runDirectChat 通识问题：单次调用、不带任何工具，模型回复原样返回。
// 模型即便返回 tool_calls 也不执行 —— 本路径没有平台数据能力，绝不越权。
func runDirectChat(
	ctx context.Context,
	adapter adapters.ProviderAdapter,
	cfg adapters.ProviderConfig,
	model string,
	history []adapters.ChatMessage,
	onCall func(scene string, resp *adapters.ChatResponse, pErr *adapters.ProviderError, elapsedMs int),
) (*agentLoopResult, *adapters.ProviderError) {

	req := buildChatRequest(routeDirect, model, history)

	startedAt := time.Now()
	resp, pErr := callProviderChat(ctx, adapter, cfg, req)
	if pErr != nil {
		if onCall != nil {
			onCall("chat.direct", nil, pErr, int(time.Since(startedAt).Milliseconds()))
		}
		return nil, pErr
	}
	if onCall != nil {
		onCall("chat.direct", resp, nil, int(time.Since(startedAt).Milliseconds()))
	}

	result := &agentLoopResult{
		Model: firstNonEmpty(resp.Model, model),
		Route: routeDirect,
	}
	result.Usage.PromptTokens = resp.Usage.PromptTokens
	result.Usage.CompletionTokens = resp.Usage.CompletionTokens
	result.Usage.TotalTokens = resp.Usage.TotalTokens
	result.Content = strings.TrimSpace(resp.Content)
	if result.Content == "" {
		result.Content = "模型未返回任何内容，请重试。"
	}
	return result, nil
}

// ------------------------------------------------------------------
// 小工具
// ------------------------------------------------------------------

func msText(elapsed time.Duration) string {
	ms := elapsed.Milliseconds()
	if ms <= 0 {
		ms = 1
	}
	return fmt.Sprintf("%d ms", ms)
}

// summarizeArgs 生成轨迹里展示的参数摘要（长值截断，避免 SQL 撑爆一行）
func summarizeArgs(args map[string]any) string {
	if len(args) == 0 {
		return "无参数"
	}
	keys := make([]string, 0, len(args))
	for key := range args {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+truncateArg(args[key]))
	}
	text := strings.Join(parts, ", ")
	if len(text) > 140 {
		text = text[:140] + "…"
	}
	return text
}

func truncateArg(value any) string {
	text := strings.TrimSpace(fmt.Sprintf("%v", value))
	text = strings.ReplaceAll(text, "\n", " ")
	if len(text) > 36 {
		text = text[:36] + "…"
	}
	return text
}
