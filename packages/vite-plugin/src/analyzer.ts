import { parse } from '@babel/parser'
import _traverse from '@babel/traverse'
import * as t from '@babel/types'
import type { LuxoSchema, LuxoModel } from '@luxo/client'

// babel/traverse CJS/ESM interop
const traverse = (_traverse as unknown as { default: typeof _traverse }).default ?? _traverse

/** Max nesting depth before warning */
const MAX_NESTING_DEPTH = 5

/**
 * Compile-time nested field tracking for Luxo API calls.
 * Uses Babel AST for accurate analysis including:
 * - Direct access: user.name
 * - Optional chain: user?.name
 * - Nested relations: post.user.name → "user{name}"
 * - Lambda params: post.comments.forEach(c => c.user.name)
 * - Destructuring: const { name, email } = await client.getUser(1)
 * - Variable aliasing: const user = post.user; user.name
 */
export function analyzeAndTransform(
  code: string,
  _id: string,
  schema: LuxoSchema,
): string | null {
  let ast: t.File
  try {
    ast = parse(code, {
      sourceType: 'module',
      plugins: ['typescript', 'jsx'],
    })
  } catch {
    return null // unparseable file — skip
  }

  // Phase 1: collect variable → API mapping + variable → parent chain
  const varToAPI = new Map<string, string>()
  const varToType = new Map<string, string>()
  const varToParent = new Map<string, { var: string; field: string }>()
  const apiTrees = new Map<string, FieldNode>()

  traverse(ast, {
    VariableDeclarator(path) {
      const id = path.node.id
      const init = path.node.init
      if (!init) return

      // Pattern: const { name, email } = await client.getUser(args)
      if (t.isObjectPattern(id)) {
        const expr = t.isAwaitExpression(init) ? init.argument : init
        if (t.isCallExpression(expr) && t.isMemberExpression(expr.callee)) {
          const method = expr.callee.property
          if (t.isIdentifier(method)) {
            const apiMeta = Object.values(schema.apis).find(a => a.name === method.name)
            if (apiMeta?.returnType) {
              const model = schema.models[apiMeta.returnType]
              if (model) {
                const root = new FieldNode('root')
                for (const prop of id.properties) {
                  if (t.isObjectProperty(prop) && t.isIdentifier(prop.key)) {
                    const fieldName = prop.key.name
                    if (model.fields.some(f => f.name === fieldName)) {
                      root.addChild(fieldName)
                    }
                  }
                }
                if (root.children.size > 0 && root.children.size < model.fields.length) {
                  apiTrees.set(method.name, root)
                  varToAPI.set('__destruct__' + method.name, method.name)
                }
              }
            }
          }
        }
        return
      }

      if (!t.isIdentifier(id)) return

      // Unwrap await
      const expr = t.isAwaitExpression(init) ? init.argument : init

      // Pattern: const user = await client.getUser(args)
      if (t.isCallExpression(expr) && t.isMemberExpression(expr.callee)) {
        const method = expr.callee.property
        if (t.isIdentifier(method)) {
          const apiMeta = Object.values(schema.apis).find(a => a.name === method.name)
          if (apiMeta?.returnType) {
            varToAPI.set(id.name, method.name)
            varToType.set(id.name, apiMeta.returnType)
          }
        }
      }

      // Pattern: const user = post.user (variable alias)
      if (t.isMemberExpression(expr) && t.isIdentifier(expr.object) && t.isIdentifier(expr.property)) {
        const parentVar = expr.object.name
        if (varToAPI.has(parentVar) || varToParent.has(parentVar)) {
          varToParent.set(id.name, { var: parentVar, field: expr.property.name })
          // Resolve type
          const parentType = varToType.get(parentVar)
          if (parentType) {
            const model = schema.models[parentType]
            const field = model?.fields.find(f => f.name === expr.property.name)
            if (field?.type && schema.models[field.type]) {
              varToType.set(id.name, field.type)
            }
          }
        }
      }
    },

    // Track forEach/map lambda params: arr.forEach(item => ...)
    CallExpression(path) {
      const callee = path.node.callee
      if (!t.isMemberExpression(callee)) return
      const method = callee.property
      if (!t.isIdentifier(method)) return
      if (!['forEach', 'map', 'filter', 'find', 'some', 'every', 'flatMap'].includes(method.name)) return

      // Get the array source
      const obj = callee.object
      let sourceVar: string | undefined
      let sourceField: string | undefined

      if (t.isMemberExpression(obj) && t.isIdentifier(obj.object) && t.isIdentifier(obj.property)) {
        sourceVar = obj.object.name
        sourceField = obj.property.name
      } else if (t.isIdentifier(obj)) {
        // Direct variable: comments.forEach(...)
        sourceVar = obj.name
      }

      if (!sourceVar) return
      if (!varToAPI.has(sourceVar) && !varToParent.has(sourceVar)) return

      // Get lambda param name
      const arg = path.node.arguments[0]
      if (!arg) return
      let paramName: string | undefined
      if (t.isArrowFunctionExpression(arg) || t.isFunctionExpression(arg)) {
        const firstParam = arg.params[0]
        if (t.isIdentifier(firstParam)) {
          paramName = firstParam.name
        }
      }

      if (!paramName) return

      // Link lambda param to source
      if (sourceField) {
        varToParent.set(paramName, { var: sourceVar, field: sourceField })
        // Resolve type
        const parentType = varToType.get(sourceVar)
        if (parentType) {
          const model = schema.models[parentType]
          const field = model?.fields.find(f => f.name === sourceField)
          if (field?.type && schema.models[field.type]) {
            varToType.set(paramName, field.type)
          }
        }
      } else {
        // Direct variable alias (comments = post.comments, then comments.forEach(c => ...))
        const parent = varToParent.get(sourceVar)
        if (parent) {
          varToParent.set(paramName, parent)
          const parentType = varToType.get(sourceVar)
          if (parentType) varToType.set(paramName, parentType)
        }
      }
    },
  })

  if (varToAPI.size === 0) return null

  // Phase 2: initialize trees for non-destructured APIs
  for (const apiName of varToAPI.values()) {
    if (!apiTrees.has(apiName)) {
      apiTrees.set(apiName, new FieldNode('root'))
    }
  }

  const processChain = (chain: string[]) => {
    const rootVar = chain[0]
    let apiName: string | undefined
    let fieldChain: string[]

    if (varToAPI.has(rootVar)) {
      apiName = varToAPI.get(rootVar)
      fieldChain = chain.slice(1)
    } else {
      const resolved = resolveParentChain(rootVar, varToParent)
      if (!resolved) return
      apiName = varToAPI.get(resolved.rootVar)
      fieldChain = [...resolved.fields, ...chain.slice(1)]
    }

    if (!apiName) return
    const tree = apiTrees.get(apiName)
    if (!tree) return

    const apiMeta = Object.values(schema.apis).find(a => a.name === apiName)
    if (!apiMeta?.returnType) return

    addFieldChain(tree, fieldChain, schema.models[apiMeta.returnType], schema)
  }

  traverse(ast, {
    MemberExpression(path) {
      const chain = extractChain(path.node)
      if (chain && chain.length >= 2) processChain(chain)
    },
    OptionalMemberExpression(path) {
      const chain = extractChain(path.node)
      if (chain && chain.length >= 2) processChain(chain)
    },
  })

  // Phase 3: generate $select and inject
  let modified = false
  let result = code

  for (const [apiName, tree] of apiTrees) {
    const selectStr = tree.toSelectString()
    if (!selectStr) continue

    // Check depth and warn
    const depth = tree.maxDepth()
    if (depth > MAX_NESTING_DEPTH) {
      console.warn(
        `[luxo] Warning: ${apiName} has ${depth}-level nested field selection (max recommended: ${MAX_NESTING_DEPTH}). ` +
        `Deep nesting may cause performance issues. Consider using @native or restructuring your query.`
      )
    }

    // Skip if all top-level fields are used
    const apiMeta = Object.values(schema.apis).find(a => a.name === apiName)
    if (apiMeta?.returnType) {
      const model = schema.models[apiMeta.returnType]
      if (model && tree.children.size >= model.fields.length) continue
    }

    // Find and replace the API call
    const callRegex = new RegExp(
      `(await\\s+\\w+\\.${escapeRegex(apiName)}\\()([^)]*)\\)`,
    )
    const match = callRegex.exec(result)
    if (!match) continue
    const [fullMatch, prefix, args] = match
    if (args.includes('$select')) continue

    const trimmed = args.trim()
    const replacement = trimmed === ''
      ? `${prefix}{ $select: '${selectStr}' })`
      : `${prefix}${args}, { $select: '${selectStr}' })`

    result = result.replace(fullMatch, replacement)
    modified = true
  }

  return modified ? result : null
}

// ─── FieldNode tree ─────────────────────────────────────────────────────────

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

  maxDepth(): number {
    if (this.children.size === 0) return 0
    let max = 0
    for (const child of this.children.values()) {
      max = Math.max(max, child.maxDepth())
    }
    return max + 1
  }
}

// ─── Helpers ────────────────────────────────────────────────────────────────

/** Extract property chain from MemberExpression/OptionalMemberExpression */
function extractChain(node: t.Expression): string[] | null {
  const chain: string[] = []

  let current: t.Expression = node
  while (t.isMemberExpression(current) || t.isOptionalMemberExpression(current)) {
    const prop = current.property
    if (t.isIdentifier(prop)) {
      chain.unshift(prop.name)
    } else {
      break
    }
    current = current.object

    // Skip index expressions: arr[0].field
    if ((t.isMemberExpression(current) || t.isOptionalMemberExpression(current)) && current.computed) {
      current = current.object
    }
  }

  if (t.isIdentifier(current)) {
    chain.unshift(current.name)
  }

  return chain.length >= 2 ? chain : null
}

/** Resolve variable through parent chain: c → post.comments */
function resolveParentChain(
  varName: string,
  parents: Map<string, { var: string; field: string }>,
): { rootVar: string; fields: string[] } | null {
  const fields: string[] = []
  let current = varName
  const seen = new Set<string>()

  while (parents.has(current)) {
    if (seen.has(current)) break // prevent cycles
    seen.add(current)
    const parent = parents.get(current)!
    fields.unshift(parent.field)
    current = parent.var
  }

  if (fields.length === 0) return null
  return { rootVar: current, fields }
}

/** Add a field chain to the tree, validating against schema */
function addFieldChain(
  root: FieldNode,
  chain: string[],
  model: LuxoModel | undefined,
  schema: LuxoSchema,
): void {
  let currentModel = model
  let currentNode = root

  for (const seg of chain) {
    const field = currentModel?.fields.find(f => f.name === seg)
    if (!field) break

    currentNode = currentNode.addChild(seg)
    currentModel = field.type ? schema.models[field.type] : undefined
  }
}

function escapeRegex(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}
