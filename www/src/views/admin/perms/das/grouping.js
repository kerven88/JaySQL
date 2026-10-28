import { getPureSchemaDisplayName, isPostgresSchemaDbType } from '@/utils/schemaDisplay'

const PREVIEW_LIMIT = 3

const INVALID_DATE_PREFIX = ['0001-', '0000-', '0002-', '0001/01/01', '0001/']

const pickValidDateText = (candidates) => {
  for (const raw of candidates) {
    if (raw == null) continue
    let s = typeof raw === 'string' ? raw.trim() : String(raw).trim()
    if (!s) continue
    // 去掉 ISO 格式的 T/Z 前缀比较
    const probe = s.replace(/^(\d{4}-\d{2}-\d{2})[ TZt].*/, '$1')
    const badPrefix = INVALID_DATE_PREFIX.some((p) => s.startsWith(p) || probe.startsWith(p))
    if (!badPrefix) return s
  }
  return ''
}

const pickCreatedAt = (item) =>
  pickValidDateText([
    item?.principal_earliest_created_at,
    item?.principalEarliestCreatedAt,
    item?.earliest_created_at,
    item?.earliestCreatedAt,
    item?.created_at,
    item?.createdAt,
    item?.create_time,
    item?.createTime,
    item?.CreatedAt,
    item?.created,
    item?.insert_time,
    item?.insertTime,
  ])

const pickEffectiveUpdatedAtRaw = (item) =>
  pickValidDateText([
    item?.table_max_updated_at,
    item?.tableMaxUpdatedAt,
    item?.max_table_updated_at,
    item?.updated_at,
    item?.updatedAt,
    item?.update_time,
    item?.updateTime,
    item?.UpdatedAt,
    item?.updated,
    item?.modify_time,
    item?.modifyTime,
    item?.change_time,
    item?.changeTime,
  ])

const pickUpdatedAt = (item) => pickEffectiveUpdatedAtRaw(item) || pickCreatedAt(item)

export const formatPrincipalType = (value) => (value === 'role' ? '角色' : '用户')

export const buildPrincipalKey = (principalType, principalID) => `${principalType}:${principalID}`

export const getInstanceName = (record) => {
  const remark = String(record.remark || '').trim()
  if (remark) return remark
  // 数据源已被删除的悬空授权：hostname 为空，直接拼会得到 ":0"
  const hostname = String(record.hostname || '').trim()
  if (!hostname) return '已删除的数据源'
  return `${hostname}:${record.port}`
}

export const getResourceLabel = (record) => {
  if (record.resource_type === 'instance') {
    return '整个实例'
  }
  if (record.resource_type === 'schema' && isPostgresSchemaDbType(record.db_type)) {
    return getPureSchemaDisplayName({
      dbType: record.db_type,
      schema: record.schema,
      displayName: record.display_name,
      schemaName: record.schema_name,
      databaseName: record.database_name,
      parentPath: record.parent_path,
    }) || record.display_name || record.schema
  }
  const base = record.display_name || record.schema
  // 表级授权（新授权表才有的粒度）要带上表名，否则同库多条表授权无法区分
  if (record.resource_type === 'table' && record.table) {
    return `${base} / ${record.table}`
  }
  return base
}

export const getGrantSummaryText = (record) => {
  const parts = []
  if (record.instanceGrantCount) {
    parts.push(`实例 ${record.instanceGrantCount}`)
  }
  if (record.databaseGrantCount) {
    parts.push(`Database ${record.databaseGrantCount}`)
  }
  if (record.schemaGrantCount) {
    parts.push(`Schema ${record.schemaGrantCount}`)
  }
  return parts.join(' / ') || '暂无资源'
}

export const compareDateText = (left, right) => {
  if (!left) return 1
  if (!right) return -1
  return String(right).localeCompare(String(left))
}

export const buildGroupRecords = (records) => {
  const groups = new Map()
  records.forEach((item) => {
    const key = buildPrincipalKey(item.principal_type, item.principal_id)
    const deletedHint = String(item.principal_deleted_hint || '').trim()

    // 主体级（用户/角色）第一次授权时间：优先用后端返回的 principal_earliest_created_at
    // 它是按主体聚合的全局 MIN(created_at)，不受「老记录被删、新记录创建时间晚」影响
    const principalLevelFirstAt =
      pickValidDateText([
        item?.principal_earliest_created_at,
        item?.principalEarliestCreatedAt,
        item?.earliest_created_at,
        item?.earliestCreatedAt,
      ]) || ''

    const createdAtForThisItem =
      pickValidDateText([
        item?.created_at,
        item?.createdAt,
        item?.create_time,
        item?.createTime,
        item?.CreatedAt,
        item?.created,
        item?.insert_time,
        item?.insertTime,
      ]) || ''

    const updatedAt = pickUpdatedAt(item)

    if (!groups.has(key)) {
      groups.set(key, {
        key,
        principal_id: item.principal_id,
        principal_name: item.principal_name,
        principal_type: item.principal_type,
        principal_type_label: formatPrincipalType(item.principal_type),
        principal_code: item.username || `ID: ${item.principal_id}`,
        principal_deleted_hint: deletedHint,
        latestUpdatedAt: updatedAt,
        // 固定使用主体级第一次操作时间（一旦确定后后续不再被覆盖）
        earliestCreatedAt: principalLevelFirstAt || createdAtForThisItem,
        instanceGrantCount: 0,
        schemaGrantCount: 0,
        databaseGrantCount: 0,
        records: [],
        environmentList: [],
        instanceList: [],
        resourceList: [],
        _environmentSet: new Set(),
        _instanceMap: new Map(),
        _resourceSet: new Set(),
      })
    }

    const group = groups.get(key)
    // 如果第一次创建 group 时没取到主体级时间（principalLevelFirstAt 空），后面的记录取到了要补上
    if (!group.earliestCreatedAt && principalLevelFirstAt) {
      group.earliestCreatedAt = principalLevelFirstAt
    }
    // 兜底：在没拿到后端主体级时间的场景，再退化为所有记录的创建时间最小值
    if (!group.earliestCreatedAt && createdAtForThisItem) {
      group.earliestCreatedAt = createdAtForThisItem
    } else if (
      !principalLevelFirstAt &&
      group.earliestCreatedAt &&
      createdAtForThisItem &&
      compareDateText(createdAtForThisItem, group.earliestCreatedAt) < 0
    ) {
      group.earliestCreatedAt = createdAtForThisItem
    }

    if (deletedHint && !group.principal_deleted_hint) {
      group.principal_deleted_hint = deletedHint
    }
    const normalized = {
      ...item,
      _created_at: createdAtForThisItem,
      _updated_at: updatedAt,
      resource_label: getResourceLabel(item),
      instance_name: getInstanceName(item),
    }
    group.records.push(normalized)
    // 「资源数」按授权条数统计；instance/schema 各自单独计数，其余（database、table）
    // 归入 databaseGrantCount，与页面原有的「实例 / Database / Schema」三个摘要口径一致。
    if (item.resource_type === 'instance') {
      group.instanceGrantCount += 1
    } else if (item.resource_type === 'schema') {
      group.schemaGrantCount += 1
    } else {
      group.databaseGrantCount += 1
    }
    if (compareDateText(group.latestUpdatedAt, updatedAt) > 0) {
      group.latestUpdatedAt = updatedAt
    }

    const environmentName = String(item.environment || '未设置')
    if (!group._environmentSet.has(environmentName)) {
      group._environmentSet.add(environmentName)
      group.environmentList.push(environmentName)
    }

    if (!group._instanceMap.has(item.instance_id)) {
      group._instanceMap.set(item.instance_id, normalized.instance_name)
      group.instanceList.push(normalized.instance_name)
    }

    const resourceText = item.resource_type === 'instance'
      ? `Instance: ${normalized.instance_name}`
      : `${item.resource_type === 'schema' ? 'Schema' : item.resource_type === 'table' ? 'Table' : 'Database'}: ${normalized.resource_label}`
    if (!group._resourceSet.has(resourceText)) {
      group._resourceSet.add(resourceText)
      group.resourceList.push(resourceText)
    }
  })

  return Array.from(groups.values())
    .map((group) => ({
      ...group,
      records: [...group.records].sort((left, right) => compareDateText(left._updated_at || pickUpdatedAt(left), right._updated_at || pickUpdatedAt(right))),
      resourceCount: group.records.length,
      instanceCount: group.instanceList.length,
      environmentCount: group.environmentList.length,
      resourcePreview: group.resourceList.slice(0, PREVIEW_LIMIT),
      hiddenResourceCount: Math.max(group.resourceList.length - PREVIEW_LIMIT, 0),
      environmentPreview: group.environmentList.slice(0, PREVIEW_LIMIT),
      environmentHiddenCount: Math.max(group.environmentList.length - PREVIEW_LIMIT, 0),
      instancePreview: group.instanceList.slice(0, PREVIEW_LIMIT),
      instanceHiddenCount: Math.max(group.instanceList.length - PREVIEW_LIMIT, 0),
    }))
    .sort((left, right) => compareDateText(left.latestUpdatedAt, right.latestUpdatedAt))
}

export const matchGroup = (group, keyword) => {
  if (!keyword) return true
  const lowerKeyword = keyword.trim().toLowerCase()
  if (!lowerKeyword) return true
  const fields = [
    group.principal_name,
    group.principal_type_label,
    group.principal_code,
    ...group.environmentList,
    ...group.instanceList,
    ...group.resourceList,
    ...group.records.map((item) => item.hostname),
    ...group.records.map((item) => item.remark),
    ...group.records.map((item) => item.db_type),
  ]
  return fields.some((item) => String(item || '').toLowerCase().includes(lowerKeyword))
}
