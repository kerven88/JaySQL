// 健康分级「检查项目录」——把平台内部的对象模型（等级 / 结果项 / 判定规则 / 事实）
// 投影成运维视角的一张阈值表。
//
// 设计要点（务必先读）：
// 1) 后端 schema 完全不动。本模块只做「读取投影 + 就地编辑」：
//    一行 = 一条只带单个可编辑条件的规则；改数字 = 改该条件的 value，
//    改级别 = 改该规则产出结果项的 level，启用开关 = 改 rule.enabled。
//    已存配置零迁移，消费方（大屏 / 明细 / 告警中心）零改动。
// 2) 匹配只在「加载时」做一次，按 ruleCode + fact 定位，**不依赖阈值本身**。
//    否则用户把 95 改成 90 就会立刻失配、绑定断掉。
// 3) 同一 fact 出现在多条规则里是真实存在的语义（磁盘 85% 是资源压力、
//    95% 是容量告急），所以匹配键里必须带 ruleCode 消歧，不能只按 fact 去重。
// 4) 没被任何 spec 命中的规则不会被藏起来——它们会变成「自定义检查项」行，
//    在主表里可见可停用，条件体编辑仍在高级区。绝不静默丢能力。

/** 检查项分类（表格里的分组标签与排序依据）。 */
export const CHECK_GROUP_LABEL = {
  availability: '可用性',
  capacity: '容量',
  performance: '性能',
}

/** 比较符的中文读法（与后端 op 一一对应）。 */
export const OP_TEXT = {
  eq: '=', ne: '≠', gt: '>', gte: '≥', lt: '<', lte: '≤',
  in: '属于', not_in: '不属于', between: '介于',
  exists: '存在', absent: '不存在', contains: '包含',
}

/**
 * 检查项目录。
 *
 * kind: 'number' 表示这一行有可编辑的数值阈值；'state' 表示触发条件是状态枚举
 * （如"采集状态 = failed"），没有阈值可调，只开放级别与启用。
 * match.fact 用于展示定位与条件摘要，match.ruleCode 用于消歧（同 fact 多档时必需）。
 */
export const CHECK_SPECS = [
  // ---------------- 可用性 ----------------
  {
    id: 'instance_down', label: '实例不可达', group: 'availability', kind: 'state',
    match: { ruleCode: 'instance_down', fact: 'metric:avail.up' },
  },
  {
    id: 'collect_failed', label: '采集失败', group: 'availability', kind: 'state',
    match: { ruleCode: 'collect_failed', fact: 'collect_status' },
  },
  {
    id: 'replication_stopped', label: '主从复制中断', group: 'availability', kind: 'state',
    match: { ruleCode: 'replication_stopped', fact: 'metric:repl.io_running' },
  },
  {
    id: 'data_stale', label: '数据过期', group: 'availability', kind: 'number', unit: '秒',
    match: { ruleCode: 'data_stale', fact: 'freshness_seconds' },
  },
  {
    id: 'collect_degraded', label: '采集连续失败', group: 'availability', kind: 'number', unit: '次',
    match: { ruleCode: 'collect_degraded', fact: 'consecutive_failures' },
  },
  {
    id: 'collect_warning', label: '采集质量降级', group: 'availability', kind: 'state',
    match: { ruleCode: 'collect_warning', fact: 'card_status' },
  },
  {
    id: 'alert_p1_active', label: '存在 P1 级待认领告警', group: 'availability', kind: 'state',
    match: { ruleCode: 'alert_p1_active', fact: 'alert_severity_max' },
  },
  {
    id: 'alert_active', label: '存在待认领告警', group: 'availability', kind: 'number', unit: '条',
    match: { ruleCode: 'alert_active', fact: 'active_alerts' },
  },
  {
    id: 'replication_lag', label: '复制延迟', group: 'availability', kind: 'number', unit: '秒',
    match: { ruleCode: 'replication_lag_high', fact: 'metric:repl.lag_seconds' },
  },

  // ---------------- 容量 ----------------
  {
    id: 'disk_exhausted', label: '磁盘使用率', scenario: '容量告急档', group: 'capacity', kind: 'number', unit: '%',
    match: { ruleCode: 'capacity_exhausted', fact: 'metric:res.disk' },
  },
  {
    id: 'conn_exhausted', label: '活跃连接占比', scenario: '容量告急档', group: 'capacity', kind: 'number', unit: '%',
    match: { ruleCode: 'capacity_exhausted', fact: 'metric:cap.conn_used_pct' },
  },
  {
    id: 'buffer_pool_exhausted', label: 'Buffer Pool 使用率', scenario: '容量告急档', group: 'capacity', kind: 'number', unit: '%',
    match: { ruleCode: 'capacity_exhausted', fact: 'metric:cap.buffer_pool_used_pct' },
  },
  {
    id: 'fd_exhausted', label: '文件句柄使用率', scenario: '容量告急档', group: 'capacity', kind: 'number', unit: '%',
    match: { ruleCode: 'capacity_exhausted', fact: 'metric:res.fd_used_pct' },
  },
  {
    id: 'cpu_pressure', label: 'CPU 使用率', group: 'capacity', kind: 'number', unit: '%',
    match: { ruleCode: 'resource_pressure', fact: 'metric:res.cpu' },
  },
  {
    id: 'memory_pressure', label: '内存使用率', group: 'capacity', kind: 'number', unit: '%',
    match: { ruleCode: 'resource_pressure', fact: 'metric:res.memory' },
  },
  {
    id: 'disk_pressure', label: '磁盘使用率', scenario: '资源压力档', group: 'capacity', kind: 'number', unit: '%',
    match: { ruleCode: 'resource_pressure', fact: 'metric:res.disk' },
  },
  {
    id: 'io_pressure', label: 'IO 使用率', group: 'capacity', kind: 'number', unit: '%',
    match: { ruleCode: 'resource_pressure', fact: 'metric:res.io' },
  },
  {
    id: 'swap_pressure', label: 'Swap 使用率', group: 'capacity', kind: 'number', unit: '%',
    match: { ruleCode: 'resource_pressure', fact: 'metric:res.swap_used_pct' },
  },

  // ---------------- 性能 ----------------
  {
    id: 'slow_query_storm', label: '慢查询速率', scenario: '激增档', group: 'performance', kind: 'number', unit: '条/秒',
    match: { ruleCode: 'slow_query_storm', fact: 'metric:eng.slow_queries' },
  },
  {
    id: 'slow_query_elevated', label: '慢查询速率', scenario: '存在档', group: 'performance', kind: 'number', unit: '条/秒',
    match: { ruleCode: 'slow_queries_elevated', fact: 'metric:eng.slow_queries' },
  },
  {
    id: 'conn_error_high', label: '连接错误速率', group: 'performance', kind: 'number', unit: '条/秒',
    match: { ruleCode: 'conn_error_high', fact: 'metric:eng.aborted_conns' },
  },
  {
    id: 'cache_hit_low', label: '缓存命中率', group: 'performance', kind: 'number', unit: '%',
    match: { ruleCode: 'cache_hit_low', fact: 'metric:perf.cache_hit_ratio' },
  },
  {
    id: 'long_trx', label: '长事务数', group: 'performance', kind: 'number', unit: '个',
    match: { ruleCode: 'long_trx_present', fact: 'metric:perf.long_trx_count' },
  },
  {
    id: 'lock_wait', label: '锁等待数', group: 'performance', kind: 'number', unit: '个',
    match: { ruleCode: 'lock_wait_present', fact: 'metric:perf.locks' },
  },
]

/** 单条条件的可读摘要（有 label 用 label，否则用事实字典名称，最后才落原始键）。 */
export function conditionText(cond, factMap) {
  if (!cond) return '—'
  const label = cond.label || factMap?.[cond.fact]?.name || cond.fact || '?'
  const op = OP_TEXT[cond.op] || cond.op
  if (cond.op === 'exists' || cond.op === 'absent') return `${label} ${op}`
  const value = Array.isArray(cond.value) ? cond.value.join(' / ') : String(cond.value ?? '')
  return `${label} ${op} ${value}`
}

/** 条件组摘要：any 组内"或"、all 组内"且"、两组之间再"且"。 */
export function ruleConditionText(rule, factMap) {
  const any = rule?.any || []
  const all = rule?.all || []
  const parts = []
  if (any.length) {
    const text = any.map((cond) => conditionText(cond, factMap)).join(' 或 ')
    parts.push(all.length && any.length > 1 ? `（${text}）` : text)
  }
  if (all.length) {
    const text = all.map((cond) => conditionText(cond, factMap)).join(' 且 ')
    parts.push(any.length && all.length > 1 ? `（${text}）` : text)
  }
  return parts.join(' 且 ')
}

/** 方向提示：由比较符推得，用户不必理解算子本身。 */
export function directionHint(cond) {
  switch (cond?.op) {
    case 'gt':
    case 'gte':
      return '实测值达到阈值即命中'
    case 'lt':
    case 'lte':
      return '实测值低于阈值即命中'
    case 'eq':
      return '实测值等于该值时命中'
    case 'ne':
      return '实测值不等于该值时命中'
    default:
      return '满足该条件即命中'
  }
}

const buildFactMap = (facts) => {
  const map = {}
  ;(facts || []).forEach((item) => {
    if (item?.key) map[item.key] = item
  })
  return map
}

/** 在规则集合里定位 spec 对应的条件；命中返回可用索引与引用。 */
function findBinding(rules, spec) {
  for (let ruleIndex = 0; ruleIndex < rules.length; ruleIndex += 1) {
    const rule = rules[ruleIndex]
    // 指定了 ruleCode 就只认这一条规则：同 fact 多档（磁盘 95 / 85）靠它消歧
    if (spec.match.ruleCode && rule?.code !== spec.match.ruleCode) continue
    const groups = ['any', 'all']
    for (const group of groups) {
      const list = rule?.[group] || []
      for (let condIndex = 0; condIndex < list.length; condIndex += 1) {
        if (list[condIndex]?.fact === spec.match.fact) {
          return { ruleIndex, rule, group, condIndex, condition: list[condIndex] }
        }
      }
    }
  }
  return null
}

/**
 * 把当前配置投影成表格行。
 *
 * @param {{rules:Array,results:Array,facts:Array}} config
 * @returns {{rows:Array, missing:Array}} rows 为可编辑行；missing 为当前配置里
 *   不存在对应规则的内置检查项（用于表格脚注提示，避免"项少了却没人知道"）。
 */
export function resolveHealthChecks(config) {
  const rules = config?.rules || []
  const results = config?.results || []
  const factMap = buildFactMap(config?.facts)
  const resultByCode = {}
  results.forEach((item) => {
    if (item?.code) resultByCode[item.code] = item
  })

  // 同一结果项被多行共享时（如容量告急 4 项），级别是联动的——必须让用户知道
  const levelShare = {}
  CHECK_SPECS.forEach((spec) => {
    const hit = findBinding(rules, spec)
    if (hit?.rule?.result) levelShare[hit.rule.result] = (levelShare[hit.rule.result] || 0) + 1
  })
  // 同一规则被多行共享时（容量告急 4 个阈值共享一条规则），启用开关也联动
  const ruleShare = {}
  CHECK_SPECS.forEach((spec) => {
    const hit = findBinding(rules, spec)
    if (hit) ruleShare[hit.rule.code] = (ruleShare[hit.rule.code] || 0) + 1
  })

  const rows = []
  const missing = []
  const boundRules = new Set()

  CHECK_SPECS.forEach((spec) => {
    const hit = findBinding(rules, spec)
    if (!hit) {
      missing.push(spec)
      return
    }
    boundRules.add(hit.rule.code)
    const result = resultByCode[hit.rule.result] || null
    rows.push({
      key: spec.id,
      custom: false,
      id: spec.id,
      label: spec.label,
      scenario: spec.scenario || '',
      group: spec.group,
      groupLabel: CHECK_GROUP_LABEL[spec.group] || '其他',
      unit: spec.unit || '',
      kind: spec.kind,
      // 触发条件的可读摘要：数值行只显示方向 + 数字；状态行只取「命中组」，
      // 门槛组（all，如"仅主从/集群"）单独在 gateText 展示，避免同一条件说两遍
      triggerText: spec.kind === 'state'
        ? (hit.rule[hit.group] || []).map((cond) => conditionText(cond, factMap)).join(hit.group === 'any' ? ' 或 ' : ' 且 ')
        : '',
      directionTip: spec.kind === 'number' ? directionHint(hit.condition) : '',
      // 前置门槛（如"仅主从/集群形态才判定复制中断"）——只读，但必须让人看见
      gateText: (hit.rule.all || [])
        .filter((cond) => cond !== hit.condition)
        .map((cond) => conditionText(cond, factMap))
        .join(' 且 '),
      ruleCode: hit.rule.code,
      rule: hit.rule,
      ruleShared: ruleShare[hit.rule.code] || 1,
      enabled: hit.rule,
      condition: hit.condition,
      resultCode: hit.rule.result,
      result,
      levelShared: levelShare[hit.rule.result] || 1,
      op: hit.condition?.op || 'gte',
      opText: OP_TEXT[hit.condition?.op] || '≥',
    })
  })

  // 未被目录覆盖的规则（用户自定义组合条件）同样进主表，条件体在高级区维护
  rules.forEach((rule) => {
    if (boundRules.has(rule.code)) return
    const result = resultByCode[rule.result] || null
    rows.push({
      key: `custom::${rule.code}`,
      custom: true,
      id: `custom::${rule.code}`,
      label: result?.name || rule.code,
      scenario: '自定义',
      group: 'custom',
      groupLabel: '自定义',
      unit: '',
      kind: 'custom',
      triggerText: ruleConditionText(rule, factMap) || '（无条件）',
      directionTip: '',
      gateText: '',
      ruleCode: rule.code,
      rule,
      ruleShared: 1,
      enabled: rule,
      condition: null,
      resultCode: rule.result,
      result,
      levelShared: 1,
      op: '',
      opText: '',
    })
  })

  return { rows, missing, factMap }
}

/** 表格「检查项」列的展示名：未绑定的结果项名称兜底为规则 code。 */
export function rowDisplayName(row) {
  return row.label || row.ruleCode
}
