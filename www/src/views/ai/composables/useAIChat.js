import { computed, ref } from 'vue'
import { aiChatCancelApi, aiChatCompletionApi, AI_CHAT_STREAM_PATH } from '@/api/ai'
import { useUserStore } from '@/store/user'

export const AIChatMessageType = {
  TEXT: 'text',
  SQL: 'sql',
  ACTION_CARD: 'action_card',
}

export const AIChatRole = {
  USER: 'user',
  ASSISTANT: 'assistant',
}

const genId = () => `msg_${Date.now()}_${Math.random().toString(36).slice(2, 8)}`

// 一次运行的标识：只含字母数字（服务端按 alphanum 校验），随机后缀避免同毫秒碰撞。
// 与消息 id 分开：消息 id 是渲染用的，run_id 是「取消时指哪一次」用的，语义不同。
const genRunId = () => `run${Date.now().toString(36)}${Math.random().toString(36).slice(2, 10)}`

/**
 * 区分「用户主动取消」与「网络中断」——两者都表现为读流/请求失败，但收口方式相反：
 * 前者要落成「已停止生成」并保留已吐出的文字，后者才按「回答可能不完整」处理。
 * fetch 的 abort 抛 DOMException.name='AbortError'；axios 侧被 request.js 归一成
 * { canceled: true, code: 'ERR_CANCELED' }，两种形态都要认。
 */
const isAbortError = (err) =>
  !!err && (err.name === 'AbortError' || err.code === 'ERR_CANCELED' || err.canceled === true)

// 用户主动取消后的收口消息：
//   - 已经吐出一部分 → 保留可见内容 + 「已停止生成」标记，不抹掉用户已读到的文字；
//   - 一个字都没出来 → 给一句明确说明，避免留下一个空气泡。
// 取消标记走独立字段而不是拼进正文：正文要参与复制与下一轮上下文，不该带上提示语。
const buildCancelledReply = (partialText) => ({
  type: AIChatMessageType.TEXT,
  text: String(partialText || '').trim() || '已取消本次提问，未生成任何内容。',
  cancelled: true,
})

// 工具调用轨迹条目
const traceStep = (tool, arg, ms) => ({ tool, arg, ms })

// —— 真实 LLM 对话 ——
// 对话完全由后端默认服务商驱动。模型不可用（未接入 / 凭证异常 / 调用失败）时，
// 直接返回明确错误并给出排查入口 —— 不再用本地规则或演示数据兜底。

const buildLlmUnavailableReply = (reason, elapsedMs) => ({
  type: AIChatMessageType.ACTION_CARD,
  text: `AI 模型当前不可用，无法回答本次提问。\n原因：${reason}`,
  trace: [traceStep('llm_chat', `调用失败：${reason}`, `${elapsedMs} ms`)],
  action: {
    title: 'AI 模型不可用',
    description: '请到「系统管理 → AI 能力」检查服务商的接入状态、凭证与连通性后重试。',
    route: '/admin/ai',
    buttonText: '前往 AI 能力',
    tone: 'warning',
  },
})

// 后端结果 → 前端消息体（非流式与流式共用同一口径，避免两处漂移）
const buildReplyFromResult = (data, startedAt, streamedText = '') => {
  const text = data?.content || streamedText
  if (data?.source === 'llm' && text) {
    const label = [data.provider_name, data.model].filter(Boolean).join(' · ') || 'LLM'
    // 后端返回真实工具调用轨迹；没有工具调用时退化为一次模型调用记录
    const trace =
      Array.isArray(data.traces) && data.traces.length
        ? data.traces.map((step) => traceStep(step.tool, step.arg || '', step.ms || ''))
        : [traceStep('llm_chat', label, `${data.latency_ms || Date.now() - startedAt} ms`)]
    // 流式中途断开：内容可能不完整，用轨迹提示，不改写模型原话
    if (data.truncated) {
      trace.push(traceStep('stream', '连接中断，回答可能不完整', ''))
    }

    const payload = {
      type: AIChatMessageType.TEXT,
      text,
      viaModel: data.model || '',
      trace,
    }

    // 写操作草稿（如创建工单）：渲染确认卡片。
    // 两种确认方式：submitPath 非空表示就地提交（点确认直接落库，不跳页）；
    // 否则按 route 跳转到目标页并由该页消费 prefill。
    const draft = data.pending_action
    if (draft) {
      payload.type = AIChatMessageType.ACTION_CARD
      payload.sql = draft.sql || null
      payload.action = {
        // 草稿类型决定确认方式：create_order / create_backup 走就地提交，其余按 route 跳转
        type: draft.type || '',
        title: draft.title || '待确认的写操作',
        description: draft.description || '',
        route: draft.route || '',
        buttonText: draft.button_text || '前往确认',
        tone: draft.tone || 'primary',
        prefill: draft.prefill || null,
        submitPath: draft.submit_path || '',
      }
    }
    return payload
  }
  // 后端降级（source=rule_engine）：按不可用处理，不再回落本地规则
  return buildLlmUnavailableReply(data?.reason || '模型服务未返回内容', Date.now() - startedAt)
}

const realLLM = async (history, opts = {}) => {
  const startedAt = Date.now()
  try {
    const res = await aiChatCompletionApi(
      { messages: history, ...(opts.runId ? { run_id: opts.runId } : {}) },
      { silentBusinessError: true, silentHttpError: true, signal: opts.signal },
    )
    return buildReplyFromResult(res?.data, startedAt)
  } catch (err) {
    // 取消必须原样上抛成取消态：这里若回落成「模型不可用」卡片，
    // 用户点了取消却看到一张报错卡片，观感与事实都不对。
    if (isAbortError(err)) return { cancelled: true, text: '' }
    return buildLlmUnavailableReply(err?.message || '请求模型服务失败', Date.now() - startedAt)
  }
}

// —— 流式对话（SSE） ——
// 只消费后端判定的「直答」路由：start → delta* → done。
// 工具路由后端不逐字下发，直接给一个 done（携带完整结果），前端行为与旧版一致。
const STREAM_FLUSH_INTERVAL = 80

// 按 '\n\n' 切帧，解析 event / data（后端每条事件只写一行 data）
const parseSSEFrame = (frame) => {
  let event = 'message'
  const dataLines = []
  frame.split(/\r?\n/).forEach((line) => {
    if (line.startsWith('event:')) event = line.slice(6).trim()
    else if (line.startsWith('data:')) dataLines.push(line.slice(5).trim())
  })
  if (!dataLines.length) return null
  try {
    return { event, data: JSON.parse(dataLines.join('\n')) }
  } catch (_) {
    return null
  }
}

const readSSE = async (response, onEvent) => {
  const reader = response.body.getReader()
  const decoder = new TextDecoder('utf-8')
  let buffer = ''
  for (;;) {
    const { value, done } = await reader.read()
    if (done) break
    buffer += decoder.decode(value, { stream: true })
    let idx = buffer.indexOf('\n\n')
    while (idx >= 0) {
      const frame = buffer.slice(0, idx)
      buffer = buffer.slice(idx + 2)
      const parsed = parseSSEFrame(frame)
      if (parsed) onEvent(parsed.event, parsed.data)
      idx = buffer.indexOf('\n\n')
    }
  }
  const tail = buffer.trim()
  if (tail) {
    const parsed = parseSSEFrame(tail)
    if (parsed) onEvent(parsed.event, parsed.data)
  }
}

// 返回 null 表示「本路径不可用，调用方回落非流式接口」：
// 网络失败 / 接口不存在（如后端尚未发布）/ 一个事件都没收到。
//
// 返回 { cancelled: true } 表示用户主动取消 —— 调用方必须据此短路、**不得**回落非流式，
// 否则「点取消」会变成「又发一次请求」，正好与用户意图相反。
const streamLLM = async (history, onText, opts = {}) => {
  const startedAt = Date.now()
  const token = useUserStore().token
  let response
  try {
    response = await fetch(AI_CHAT_STREAM_PATH, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        ...(token ? { Authorization: `JWT ${token}` } : {}),
      },
      body: JSON.stringify({ messages: history, ...(opts.runId ? { run_id: opts.runId } : {}) }),
      ...(opts.signal ? { signal: opts.signal } : {}),
    })
  } catch (err) {
    if (isAbortError(err)) return { cancelled: true, text: '' }
    return null
  }
  if (!response.ok || !response.body) return null

  let finalData = null
  let streamedText = ''
  let failureReason = ''
  let sawEvent = false
  try {
    await readSSE(response, (event, data) => {
      sawEvent = true
      if (event === 'delta' && data?.text) {
        streamedText += data.text
        onText(streamedText)
      } else if (event === 'done') {
        finalData = data
      } else if (event === 'error') {
        failureReason = data?.reason || '模型服务未返回内容'
      }
    })
  } catch (err) {
    // 用户主动取消：把已吐出的那部分带回去，别让屏幕上的文字凭空消失
    if (isAbortError(err) || opts.signal?.aborted) {
      return { cancelled: true, text: streamedText }
    }
    // 流被网络中断：已生成的内容照常收口
    if (!streamedText) return null
  }

  if (!sawEvent) return null
  if (finalData) return buildReplyFromResult(finalData, startedAt, streamedText)
  if (streamedText) return buildReplyFromResult({ source: 'llm', content: '' }, startedAt, streamedText)
  return buildLlmUnavailableReply(failureReason || '模型服务未返回内容', Date.now() - startedAt)
}

export function useAIChat() {
  const messages = ref([])
  const loading = ref(false)
  const inputText = ref('')

  /**
   * 当前正在跑的那一轮。用普通变量而不是 ref：它只服务于「取消」这一个动作，
   * 不参与渲染，落成 ref 反而会在每次流式分片时触发无谓的依赖追踪。
   * runId 给服务端认这次运行，controller 用于断开本地连接 —— 两者缺一不可：
   * 只断开连接时，工具路由中途不写响应体，服务端可能感知不到已经没人听了。
   */
  let activeRun = null

  const hasMessages = computed(() => messages.value.length > 0)

  const lastMessageAt = computed(() => {
    const list = messages.value
    const last = list[list.length - 1]
    return last?.createdAt || null
  })

  /**
   * 终止当前轮。顺序上先投递服务端取消、再断开本地连接：
   * 取消请求走独立的 axios 实例，不会被下面的 abort 波及，因此这样写能保证
   * 「服务端收到信号」不依赖于「本地连接先断开」这个不确定的传导过程。
   * 重复调用是安全的（cancelled 标志 + abort 幂等）。
   */
  const abortActiveRun = () => {
    const run = activeRun
    if (!run || run.cancelled) return
    run.cancelled = true
    aiChatCancelApi({ run_id: run.runId }).catch(() => {
      /* 取消是尽力而为：接口失败也不该拦住本地收口 */
    })
    try {
      run.controller.abort()
    } catch (_) {
      /* 极老运行时没有 abort：忽略，仍有服务端通道生效 */
    }
  }

  // 切换会话：直接接管目标数组，后续 push 即写入该会话（无需回写同步）。
  // 若上一轮还在生成，先终止 —— 否则流式回填是按「messages 的最后一条」写的，
  // 会落到新会话的首条消息上（串会话）。
  const setMessages = (list) => {
    abortActiveRun()
    messages.value = Array.isArray(list) ? list : []
  }

  const pushUserMessage = (text) => {
    const trimmed = text.trim()
    if (!trimmed) return null
    const msg = {
      id: genId(),
      role: AIChatRole.USER,
      type: AIChatMessageType.TEXT,
      text: trimmed,
      createdAt: Date.now(),
    }
    messages.value.push(msg)
    return msg
  }

  const pushAssistantLoading = () => {
    const msg = {
      id: genId(),
      role: AIChatRole.ASSISTANT,
      loading: true,
      text: '',
      type: AIChatMessageType.TEXT,
      createdAt: Date.now(),
    }
    messages.value.push(msg)
    return msg
  }

  const replaceLast = (payload) => {
    const idx = messages.value.length - 1
    if (idx < 0) return
    const last = messages.value[idx]
    messages.value[idx] = {
      ...last,
      loading: false,
      type: payload.type || AIChatMessageType.TEXT,
      text: payload.text || '',
      sql: payload.sql || null,
      action: payload.action || null,
      trace: payload.trace || null,
      viaModel: payload.viaModel || '',
      // 主动取消标记：正文只保留模型已生成的部分，状态另存一个字段，
      // 这样复制出来的内容与后续上下文里都不会混进「已停止生成」这类提示语。
      cancelled: !!payload.cancelled,
      createdAt: Date.now(),
    }
  }

  // 流式过程中增量改写最后一条消息（保持 loading 态，只更新正文）
  const patchLast = (patch) => {
    const idx = messages.value.length - 1
    if (idx < 0) return
    messages.value[idx] = { ...messages.value[idx], ...patch }
  }

  // 文本刷新节流：模型分片很密，按固定间隔整体刷新，避免 markdown 反复重渲染
  const createTextFlusher = (apply) => {
    let timer = null
    let latest = ''
    const flush = () => {
      timer = null
      apply(latest)
    }
    return (text) => {
      latest = text
      if (timer) return
      timer = setTimeout(flush, STREAM_FLUSH_INTERVAL)
    }
  }

  const sendMessage = async (rawText) => {
    const text = (rawText ?? inputText.value).trim()
    if (!text || loading.value) return
    pushUserMessage(text)
    inputText.value = ''
    loading.value = true

    // 本轮句柄：runId 让服务端能认出「要取消的是哪一次」，controller 负责断开本地连接
    const run = { runId: genRunId(), controller: new AbortController(), cancelled: false }
    activeRun = run
    const opts = { runId: run.runId, signal: run.controller.signal }

    // 对话历史快照：取本轮之前的近 10 条纯文本消息
    // （排除 loading 占位、卡片类消息，以及被主动取消的半截回答 ——
    //  半截内容进上下文只会让模型以为自己上轮说完了）
    const history = messages.value
      .filter((m) => !m.loading && !m.cancelled && m.text && m.type === AIChatMessageType.TEXT)
      .slice(-10)
      .map((m) => ({ role: m.role === AIChatRole.USER ? 'user' : 'assistant', content: m.text }))
    pushAssistantLoading()
    try {
      let reply = null
      // 全部交给真实模型：优先流式（逐字），拿不到流(如后端未发布)再回落非流式。
      // 注意 run.cancelled 这道闸：取消后绝不回落，否则会重新发一次请求。
      if (!reply) {
        const flushText = createTextFlusher((t) => patchLast({ text: t }))
        reply = await streamLLM(history, flushText, opts)
        if (!reply && !run.cancelled) {
          reply = await realLLM(history, opts)
        }
      }
      if (reply?.cancelled || run.cancelled) {
        replaceLast(buildCancelledReply(reply?.text || ''))
      } else {
        replaceLast(reply || buildLlmUnavailableReply('模型服务未返回内容', 0))
      }
    } catch (err) {
      // 取消引发的异常按取消收口：用户点了取消却看到「模型不可用」，观感与事实都不对
      replaceLast(
        run.cancelled
          ? buildCancelledReply('')
          : buildLlmUnavailableReply(err?.message || '未知错误', 0),
      )
    } finally {
      if (activeRun === run) activeRun = null
      loading.value = false
    }
  }

  const clearChat = () => {
    abortActiveRun()
    // 原地清空：messages.value 可能正被某个会话对象持有引用
    messages.value.splice(0, messages.value.length)
  }

  return {
    messages,
    loading,
    inputText,
    hasMessages,
    lastMessageAt,
    setMessages,
    sendMessage,
    cancelMessage: abortActiveRun,
    clearChat,
  }
}
