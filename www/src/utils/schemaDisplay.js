const normalizeText = (value) => String(value || '').trim()

export const MAX_DATASOURCE_NAME_LENGTH = 15

export const truncateByRune = (text, max = MAX_DATASOURCE_NAME_LENGTH) => {
  const raw = String(text == null ? '' : text)
  const chars = Array.from(raw)
  if (chars.length <= max) return raw
  return `${chars.slice(0, max).join('')}...`
}

export const isPostgresSchemaDbType = (value) => {
  const normalized = normalizeText(value).toLowerCase()
  return ['postgresql', 'postgres', 'polardb-pg', 'polardb-postgresql', 'polardbpg'].includes(normalized)
}

const stripDatabasePrefix = (value, databaseName) => {
  const text = normalizeText(value)
  const database = normalizeText(databaseName)
  if (!text || !database) return text

  const prefixedSeparators = [' / ', '/', '.', ' · ', ': ']
  for (const separator of prefixedSeparators) {
    const prefix = `${database}${separator}`
    if (text.startsWith(prefix)) {
      return text.slice(prefix.length).trim()
    }
  }
  return text
}

export const getPureSchemaDisplayName = ({
  dbType,
  schema = '',
  displayName = '',
  schemaName = '',
  databaseName = '',
  parentPath = '',
} = {}) => {
  if (!isPostgresSchemaDbType(dbType)) {
    return normalizeText(schemaName || displayName || schema)
  }

  const database = normalizeText(databaseName || parentPath)
  const explicitSchemaName = normalizeText(schemaName)
  if (explicitSchemaName) return explicitSchemaName

  const normalizedDisplayName = stripDatabasePrefix(displayName, database)
  if (normalizedDisplayName && normalizedDisplayName !== normalizeText(displayName)) {
    return normalizedDisplayName
  }

  const rawSchema = normalizeText(schema)
  const normalizedSchema = stripDatabasePrefix(rawSchema, database)
  if (normalizedSchema && normalizedSchema !== rawSchema) {
    return normalizedSchema
  }

  if (database && rawSchema.startsWith(`${database}.`)) {
    return rawSchema.slice(database.length + 1).trim()
  }

  if (normalizedDisplayName && normalizedDisplayName !== normalizedSchema) {
    return normalizedDisplayName
  }

  return normalizedSchema || normalizedDisplayName
}
