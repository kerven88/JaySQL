package tools

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/simajiong/jaysql/internal/ai/adapters"
)

// ============================================================
// 工具层：把平台能力暴露成大模型可调用的工具
//
// 设计要点（对齐行业标准做法）：
//   - 契约面：每个工具 = 名称 + 描述 + 入参 JSON Schema + 读写标记 + 权限点
//   - 模型只输出「要调哪个工具、参数是什么」，绝不直接触达业务系统
//   - 写工具（Write=true）不在执行器里落库，而是产出草稿（DraftAction）交给人工确认
//   - 工具粒度按业务意图切，不按 REST 接口切 —— 模型不是 HTTP 客户端
// ============================================================

// Context 工具执行上下文（身份随会话透传）
type Context struct {
	Username    string
	Permissions map[string]bool
}

// Has 判断是否具备某权限点；权限点为空表示不限制
func (c *Context) Has(permission string) bool {
	key := strings.TrimSpace(permission)
	if key == "" {
		return true
	}
	if c == nil || c.Permissions == nil {
		// 没有权限清单时按「不拦截」处理：入口路由已用 ai.chat.use 做过一次鉴权，
		// 工具级权限点属于更细的约束，缺清单不应导致全部工具不可用。
		return true
	}
	return c.Permissions[key]
}

// Tool 一个平台能力
type Tool struct {
	Name        string
	Description string
	Parameters  map[string]any
	Write       bool
	Permission  string
	Handler     func(ctx *Context, args map[string]any) (any, error)
}

// DraftAction 写操作的待确认草稿。
// 工具自身不落库：模型只负责把参数填好，真正的创建由用户在卡片上确认后完成。
type DraftAction struct {
	Type        string         `json:"type"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	SQL         string         `json:"sql"`
	ButtonText  string         `json:"button_text"`
	Tone        string         `json:"tone"`
	Route       string         `json:"route"`
	Prefill     map[string]any `json:"prefill"`
	// SubmitPath 非空表示这是「就地提交」型草稿：前端把 Prefill 原样作为请求体
	// POST 到该路径即可完成落库，无需跳转到别的页面。为空则按 Route 跳转。
	SubmitPath string `json:"submit_path,omitempty"`
}

var (
	registry = map[string]*Tool{}
	ordered  = make([]string, 0)
)

func register(tool *Tool) {
	if tool == nil || strings.TrimSpace(tool.Name) == "" {
		return
	}
	if _, exists := registry[tool.Name]; !exists {
		ordered = append(ordered, tool.Name)
	}
	registry[tool.Name] = tool
}

// All 全部已注册工具（按注册顺序）
func All() []*Tool {
	out := make([]*Tool, 0, len(ordered))
	for _, name := range ordered {
		if tool, ok := registry[name]; ok {
			out = append(out, tool)
		}
	}
	return out
}

// Get 按名称取工具
func Get(name string) (*Tool, bool) {
	tool, ok := registry[strings.TrimSpace(name)]
	return tool, ok
}

// Names 工具名列表（排障与日志用）
func Names() []string {
	out := append([]string(nil), ordered...)
	sort.Strings(out)
	return out
}

// Definitions 生成下发给模型的工具清单。
// includeWrite=false 时只下发只读工具。
func Definitions(includeWrite bool) []adapters.ToolDef {
	out := make([]adapters.ToolDef, 0, len(ordered))
	for _, tool := range All() {
		if tool.Write && !includeWrite {
			continue
		}
		out = append(out, adapters.ToolDef{
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  tool.Parameters,
		})
	}
	return out
}

// Execute 执行工具：查找 → 权限校验 → 调用处理函数。
// 返回 *DraftAction 表示这是写工具的待确认草稿（未落库）。
func Execute(ctx *Context, name string, args map[string]any) (any, error) {
	tool, ok := Get(name)
	if !ok {
		return nil, fmt.Errorf("未注册的工具：%s", name)
	}
	if !ctx.Has(tool.Permission) {
		return nil, fmt.Errorf("当前账号缺少权限 %s，无法执行 %s", tool.Permission, tool.Name)
	}
	if tool.Handler == nil {
		return nil, fmt.Errorf("工具 %s 未实现", name)
	}
	if args == nil {
		args = map[string]any{}
	}
	return tool.Handler(ctx, args)
}

// ResultText 工具结果 → 回灌给模型的文本（编排层用）
func ResultText(value any) string {
	return marshalResult(value)
}

// ParseArguments 解析模型输出的参数 JSON。
// 宽松处理：解析失败返回空 map，由具体工具的必填校验给出可读错误。
func ParseArguments(raw string) map[string]any {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(trimmed), &out); err != nil {
		return map[string]any{}
	}
	return out
}

// ------------------------------------------------------------------
// 参数读取
// 模型输出的 JSON 一律不可信：类型可能不符、参数可能缺失，
// 这里统一做宽松但可控的转换，业务侧只拿规范化后的值。
// ------------------------------------------------------------------

func argString(args map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := args[key]
		if !ok || value == nil {
			continue
		}
		switch typed := value.(type) {
		case string:
			if strings.TrimSpace(typed) != "" {
				return strings.TrimSpace(typed)
			}
		case float64:
			return strconv.FormatFloat(typed, 'f', -1, 64)
		case bool:
			return strconv.FormatBool(typed)
		default:
			text := strings.TrimSpace(fmt.Sprintf("%v", typed))
			if text != "" && text != "<nil>" {
				return text
			}
		}
	}
	return ""
}

// argStringList 读取「字符串数组」参数。
// 模型可能给真正的数组，也可能给一个逗号分隔的字符串，这里一并兜住。
func argStringList(args map[string]any, keys ...string) []string {
	out := make([]string, 0)
	seen := map[string]struct{}{}
	for _, key := range keys {
		value, ok := args[key]
		if !ok || value == nil {
			continue
		}
		switch typed := value.(type) {
		case []any:
			for _, item := range typed {
				// appendUniqueString 返回新切片（可能 append 后扩容换底层数组），必须接住返回值
				out = appendUniqueString(out, seen, plainString(item))
			}
		case []string:
			for _, item := range typed {
				out = appendUniqueString(out, seen, item)
			}
		default:
			for _, item := range strings.Split(plainString(typed), ",") {
				out = appendUniqueString(out, seen, item)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return out
}

func appendUniqueString(out []string, seen map[string]struct{}, text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return out
	}
	if _, ok := seen[strings.ToLower(text)]; ok {
		return out
	}
	seen[strings.ToLower(text)] = struct{}{}
	return append(out, text)
}

func argInt(args map[string]any, key string, def int) int {
	value, ok := args[key]
	if !ok || value == nil {
		return def
	}
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	case int64:
		return int(typed)
	case json.Number:
		if parsed, err := typed.Int64(); err == nil {
			return int(parsed)
		}
	case string:
		if parsed, err := strconv.Atoi(strings.TrimSpace(typed)); err == nil {
			return parsed
		}
	}
	return def
}

// marshalResult 工具结果 → 给模型看的紧凑 JSON（超长截断，避免撑爆上下文）
func marshalResult(value any) string {
	body, err := json.Marshal(toPlain(value))
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	text := string(body)
	if len(text) > 6000 {
		text = text[:6000] + `...（结果过长已截断）`
	}
	return text
}

// toPlain 把任意值转成 JSON 可序列化的形态（时间统一转字符串）
func toPlain(value any) any {
	switch typed := value.(type) {
	case nil:
		return nil
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = toPlain(item)
		}
		return out
	case []map[string]any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, toPlain(item))
		}
		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, toPlain(item))
		}
		return out
	case fmt.Stringer:
		return typed.String()
	default:
		if _, err := json.Marshal(typed); err != nil {
			return fmt.Sprintf("%v", typed)
		}
		return typed
	}
}
