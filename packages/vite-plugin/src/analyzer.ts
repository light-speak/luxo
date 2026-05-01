import type { LuxoSchema } from '@luxo/client'

/**
 * Compile-time field tracking for Luxo API calls.
 *
 * Analyzes TypeScript source code to find which fields are accessed
 * on API response objects, then injects $select to fetch only those fields.
 *
 * Handles:
 * - Direct: user.name
 * - Optional chain: user?.name
 * - Template: `${user.name}`
 * - Destructuring: const { name, email } = await client.getUser(1)
 * - Rename destructure: const { name: userName } = ...
 */
export function analyzeAndTransform(
  code: string,
  _id: string,
  schema: LuxoSchema,
): string | null {
  let modified = false
  let result = code

  // Pattern 1: const user = await xxx.getUser(args)
  const varCallRegex = /(?:const|let|var)\s+(\w+)\s*=\s*await\s+\w+\.(get\w+|list\w+|create\w+|update\w+)\(([^)]*)\)/g
  let match: RegExpExecArray | null
  while ((match = varCallRegex.exec(code)) !== null) {
    const [fullMatch, varName, apiName, args] = match
    if (args.includes('$select')) continue

    const fields = collectUsedFields(code, varName, apiName, schema)
    if (!fields) continue

    result = injectSelect(result, fullMatch, apiName, args, fields)
    modified = true
  }

  // Pattern 2: const { name, email } = await xxx.getUser(args)
  const destructRegex = /(?:const|let|var)\s+\{([^}]+)\}\s*=\s*await\s+\w+\.(get\w+|list\w+|create\w+|update\w+)\(([^)]*)\)/g
  while ((match = destructRegex.exec(code)) !== null) {
    const [fullMatch, fieldsStr, apiName, args] = match
    if (args.includes('$select')) continue

    const apiMeta = Object.values(schema.apis).find(a => a.name === apiName)
    if (!apiMeta?.returnType) continue
    const model = schema.models[apiMeta.returnType]
    if (!model) continue

    // Parse destructured names (handle rename { name: userName } and skip spread)
    const names = fieldsStr.split(',')
      .map(f => f.trim().split(':')[0].trim())
      .filter(f => f && !f.startsWith('...'))

    const valid = names.filter(f => model.fields.some(mf => mf.name === f))
    if (valid.length === 0 || valid.length >= model.fields.length) continue

    result = injectSelect(result, fullMatch, apiName, args, valid.join(','))
    modified = true
  }

  return modified ? result : null
}

/** Collect fields accessed on a variable from API response */
function collectUsedFields(
  code: string,
  varName: string,
  apiName: string,
  schema: LuxoSchema,
): string | null {
  const apiMeta = Object.values(schema.apis).find(a => a.name === apiName)
  if (!apiMeta?.returnType) return null
  const model = schema.models[apiMeta.returnType]
  if (!model) return null

  // Match: varName.field, varName?.field
  const accessRegex = new RegExp(`\\b${varName}\\??\\.(?!\\()(\\w+)`, 'g')
  const used = new Set<string>()
  let m: RegExpExecArray | null
  while ((m = accessRegex.exec(code)) !== null) {
    if (model.fields.some(f => f.name === m![1])) {
      used.add(m[1])
    }
  }

  if (used.size === 0 || used.size >= model.fields.length) return null
  return Array.from(used).join(',')
}

/** Inject $select parameter into an API call string */
function injectSelect(code: string, fullMatch: string, apiName: string, args: string, selectStr: string): string {
  const trimmed = args.trim()
  let replacement: string

  if (trimmed === '') {
    replacement = fullMatch.replace(`${apiName}()`, `${apiName}({ $select: '${selectStr}' })`)
  } else {
    replacement = fullMatch.replace(
      `${apiName}(${args})`,
      `${apiName}(${args}, { $select: '${selectStr}' })`,
    )
  }
  return code.replace(fullMatch, replacement)
}
