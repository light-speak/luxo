import type { LuxoSchema } from '@luxo/client'

/**
 * Compile-time nested field tracking for Luxo API calls.
 *
 * Analyzes TypeScript source to find which fields are accessed
 * on API response objects, including nested relations:
 *
 *   post.title              → "title"
 *   post.user.name          → "user{name}"
 *   post.comments[0].content → "comments{content}"
 *   post.comments[0].user.id → "comments{user{id}}"
 *
 * Injects $select to fetch only accessed fields.
 */
export function analyzeAndTransform(
  code: string,
  _id: string,
  schema: LuxoSchema,
): string | null {
  let modified = false
  let result = code

  // Pattern: const/let/var <name> = await xxx.apiMethod(args)
  const varCallRegex = /(?:const|let|var)\s+(\w+)\s*=\s*await\s+\w+\.(get\w+|list\w+|create\w+|update\w+)\(([^)]*)\)/g
  let match: RegExpExecArray | null
  while ((match = varCallRegex.exec(code)) !== null) {
    const [fullMatch, varName, apiName, args] = match
    if (args.includes('$select')) continue

    const fields = collectNestedFields(code, varName, apiName, schema)
    if (!fields) continue

    result = injectSelect(result, fullMatch, apiName, args, fields)
    modified = true
  }

  // Pattern: const { name, email } = await xxx.apiMethod(args)
  const destructRegex = /(?:const|let|var)\s+\{([^}]+)\}\s*=\s*await\s+\w+\.(get\w+|list\w+|create\w+|update\w+)\(([^)]*)\)/g
  while ((match = destructRegex.exec(code)) !== null) {
    const [fullMatch, fieldsStr, apiName, args] = match
    if (args.includes('$select')) continue

    const apiMeta = Object.values(schema.apis).find(a => a.name === apiName)
    if (!apiMeta?.returnType) continue
    const model = schema.models[apiMeta.returnType]
    if (!model) continue

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

/** Build a FieldNode tree from property access chains */
class FieldNode {
  children = new Map<string, FieldNode>()
  constructor(public name: string) {}

  addChild(name: string): FieldNode {
    let child = this.children.get(name)
    if (!child) {
      child = new FieldNode(name)
      this.children.set(name, child)
    }
    return child
  }

  toSelectString(): string {
    if (this.children.size === 0) return ''
    const parts: string[] = []
    for (const child of this.children.values()) {
      const nested = child.toSelectString()
      parts.push(nested ? `${child.name}{${nested}}` : child.name)
    }
    return parts.join(',')
  }
}

/** Collect nested field accesses on a variable */
function collectNestedFields(
  code: string,
  varName: string,
  apiName: string,
  schema: LuxoSchema,
): string | null {
  const apiMeta = Object.values(schema.apis).find(a => a.name === apiName)
  if (!apiMeta?.returnType) return null
  const rootModel = schema.models[apiMeta.returnType]
  if (!rootModel) return null

  const root = new FieldNode('root')

  // Match property chains: varName.field1.field2, varName?.field1?.field2
  // Also: varName.field1[0].field2, varName.field1[idx].field2
  const chainRegex = new RegExp(
    `\\b${escapeRegex(varName)}((?:\\??\\.\\w+|\\[\\w+\\])+)`,
    'g',
  )

  let m: RegExpExecArray | null
  while ((m = chainRegex.exec(code)) !== null) {
    const chainStr = m[1]
    // Parse chain: ?.name → name, .name → name, [0] → (skip index)
    const segments = chainStr
      .split(/[.?[\]]+/)
      .filter(s => s && !/^\d+$/.test(s)) // filter empty and numeric indices

    if (segments.length === 0) continue

    // Walk segments, validating against schema models
    let currentModel: typeof rootModel | undefined = rootModel
    let currentNode = root

    for (const seg of segments) {
      const field = currentModel?.fields.find(f => f.name === seg)
      if (!field) break

      currentNode = currentNode.addChild(seg)

      // Resolve next model type for nested fields
      currentModel = field.type ? schema.models[field.type] : undefined
    }
  }

  const selectStr = root.toSelectString()
  if (!selectStr) return null

  // If all top-level fields are selected, skip injection
  const topFields = Array.from(root.children.keys())
  if (topFields.length >= rootModel.fields.length) return null

  return selectStr
}

/** Inject $select parameter into an API call */
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

function escapeRegex(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}
