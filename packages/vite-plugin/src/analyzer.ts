import { parse } from '@babel/parser'
import _traverse, { type NodePath } from '@babel/traverse'
import * as t from '@babel/types'
import type { LuxoAPI, LuxoField, LuxoModel, LuxoSchema, LuxoTypeDecl } from '@luxojs/client'

const traverse = (_traverse as unknown as { default: typeof _traverse }).default ?? _traverse
const MAX_NESTING_DEPTH = 5
const ARRAY_CALLBACKS = new Set(['every', 'filter', 'find', 'flatMap', 'forEach', 'map', 'some'])
const PAGE_FIELDS = new Set(['page', 'pageSize', 'total'])

type SchemaDeclaration = LuxoModel | LuxoTypeDecl
type ValueShape = 'object' | 'list' | 'page' | 'scalar'

interface CallUsage {
  api: LuxoAPI
  call: t.CallExpression
  tree: FieldNode
  escaped: boolean
}

interface ValueBinding {
  usage: CallUsage
  typeName: string
  fields: string[]
  shape: ValueShape
}

interface TextEdit {
  start: number
  end: number
  replacement: string
}

interface AnalysisState {
  schema: LuxoSchema
  apis: Map<string, LuxoAPI>
  streamAPIs: Map<string, LuxoAPI>
  bindings: Map<t.Identifier, ValueBinding>
  usages: CallUsage[]
  usagesByCall: Map<t.CallExpression, CallUsage>
}

/**
 * Tracks statically visible result usage and injects a safe compile-time
 * selection into generated client's params object.
 */
export function analyzeAndTransform(code: string, _id: string, schema: LuxoSchema): string | null {
  const ast = parseSource(code)
  if (!ast) return null

  const state = createAnalysisState(schema)
  collectBindings(ast, state)
  collectStandaloneCalls(ast, state)
  collectFieldAccesses(ast, state)
  collectEscapes(ast, state)

  const edits = state.usages.flatMap(usage => createSelectionEdit(code, usage, state.schema))
  if (edits.length === 0) return null
  return applyTextEdits(code, edits)
}

function parseSource(code: string): t.File | null {
  try {
    return parse(code, {
      sourceType: 'module',
      plugins: ['typescript', 'jsx'],
    })
  } catch {
    return null
  }
}

function createAnalysisState(schema: LuxoSchema): AnalysisState {
  return {
    schema,
    apis: new Map(Object.values(schema.apis).filter(api => !api.stream).map(api => [api.name, api])),
    streamAPIs: new Map(Object.values(schema.apis).filter(api => api.stream).map(api => [streamMethodName(api.name), api])),
    bindings: new Map(),
    usages: [],
    usagesByCall: new Map(),
  }
}

function collectBindings(ast: t.File, state: AnalysisState): void {
  traverse(ast, {
    VariableDeclarator(path) {
      bindVariableDeclarator(path, state)
    },
    CallExpression(path) {
      bindArrayCallback(path, state)
      bindStreamCallback(path, state)
    },
    ForOfStatement(path) {
      bindForOfItem(path, state)
    },
  })
}

function collectStandaloneCalls(ast: t.File, state: AnalysisState): void {
  traverse(ast, {
    CallExpression(path) {
      const apiCall = findAPICall(path, state)
      if (!apiCall || state.usagesByCall.has(apiCall.path.node)) return
      const usage = callUsage(apiCall.api, apiCall.path.node, state)
      usage.escaped = !apiCall.path.parentPath?.isExpressionStatement()
    },
  })
}

function bindVariableDeclarator(path: NodePath<t.VariableDeclarator>, state: AnalysisState): void {
  const init = asNodePath(path.get('init'))
  if (!init?.node) return

  const apiCall = findAPICall(init, state)
  if (apiCall) {
    if (apiCall.stream) return
    const usage = callUsage(apiCall.api, apiCall.path.node, state)
    bindPattern(asNodePath(path.get('id')), rootValue(usage), state)
    return
  }

  const value = resolveValue(init, state)
  if (value) bindPattern(asNodePath(path.get('id')), value, state)
}

function createCallUsage(api: LuxoAPI, call: t.CallExpression): CallUsage {
  return { api, call, tree: new FieldNode('root'), escaped: false }
}

function callUsage(api: LuxoAPI, call: t.CallExpression, state: AnalysisState): CallUsage {
  const existing = state.usagesByCall.get(call)
  if (existing) return existing
  const usage = createCallUsage(api, call)
  state.usages.push(usage)
  state.usagesByCall.set(call, usage)
  return usage
}

function rootValue(usage: CallUsage): ValueBinding {
  let shape: ValueShape = 'object'
  if (usage.api.paginated) shape = 'page'
  else if (usage.api.returnList) shape = 'list'
  return { usage, typeName: usage.api.returnType ?? '', fields: [], shape }
}

function findAPICall(
  path: NodePath<t.Node>,
  state: AnalysisState,
): { api: LuxoAPI; path: NodePath<t.CallExpression>; stream: boolean } | null {
  const expression = unwrapExpression(path)
  if (!expression.isCallExpression()) return null
  const callee = asNodePath(expression.get('callee'))
  if (!callee || (!callee.isMemberExpression() && !callee.isOptionalMemberExpression())) return null
  const name = memberPropertyName(callee.node)
  const api = name ? state.apis.get(name) ?? state.streamAPIs.get(name) : undefined
  if (!api?.returnType || !declaration(state.schema, api.returnType)) return null
  return { api, path: expression as NodePath<t.CallExpression>, stream: Boolean(name && state.streamAPIs.has(name)) }
}

function bindPattern(path: NodePath<t.Node> | null, value: ValueBinding, state: AnalysisState): void {
  if (!path) return
  if (path.isIdentifier()) {
    bindIdentifier(path, value, state)
    return
  }
  if (path.isObjectPattern()) {
    bindObjectPattern(path, value, state)
    return
  }
  if (path.isArrayPattern()) bindArrayPattern(path, value, state)
}

function bindIdentifier(path: NodePath<t.Identifier>, value: ValueBinding, state: AnalysisState): void {
  if (value.shape === 'scalar') return
  const binding = path.scope.getBinding(path.node.name)
  if (binding) state.bindings.set(binding.identifier, value)
}

function bindObjectPattern(path: NodePath<t.ObjectPattern>, value: ValueBinding, state: AnalysisState): void {
  for (const propertyPath of path.get('properties')) {
    if (propertyPath.isRestElement()) {
      value.usage.escaped = true
      continue
    }
    if (!propertyPath.isObjectProperty()) continue
    const name = objectPropertyName(propertyPath.node)
    if (!name) {
      value.usage.escaped = true
      continue
    }
    const child = resolveProperty(value, name, state.schema)
    if (!child) continue
    addSelectedFields(child)
    bindPattern(asNodePath(propertyPath.get('value')), child, state)
  }
}

function bindArrayPattern(path: NodePath<t.ArrayPattern>, value: ValueBinding, state: AnalysisState): void {
  if (value.shape !== 'list') {
    value.usage.escaped = true
    return
  }
  const item = { ...value, shape: 'object' as const }
  for (const element of path.get('elements')) bindPattern(asNodePath(element), item, state)
}

function bindArrayCallback(path: NodePath<t.CallExpression>, state: AnalysisState): void {
  const callee = asNodePath(path.get('callee'))
  if (!callee?.isMemberExpression() && !callee?.isOptionalMemberExpression()) return
  const method = memberPropertyName(callee.node)
  if (!method || !ARRAY_CALLBACKS.has(method)) return

  const source = resolveValue(asNodePath(callee.get('object')), state)
  if (source?.shape !== 'list') return
  const callback = asNodePath(path.get('arguments')[0])
  if (!callback?.isArrowFunctionExpression() && !callback?.isFunctionExpression()) return
  bindPattern(asNodePath(callback.get('params')[0]), { ...source, shape: 'object' }, state)
}

function bindStreamCallback(path: NodePath<t.CallExpression>, state: AnalysisState): void {
  const apiCall = findAPICall(path, state)
  if (!apiCall?.stream) return

  const usage = callUsage(apiCall.api, apiCall.path.node, state)
  const args = apiCall.path.get('arguments')
  const callback = asNodePath(args[args.length - 1])
  if (!callback?.isArrowFunctionExpression() && !callback?.isFunctionExpression()) {
    usage.escaped = true
    return
  }
  bindPattern(asNodePath(callback.get('params')[0]), rootValue(usage), state)
}

function bindForOfItem(path: NodePath<t.ForOfStatement>, state: AnalysisState): void {
  const source = resolveValue(asNodePath(path.get('right')), state)
  if (source?.shape !== 'list') return
  const left = asNodePath(path.get('left'))
  const target = left?.isVariableDeclaration() ? asNodePath(left.get('declarations')[0]?.get('id')) : left
  bindPattern(target, { ...source, shape: 'object' }, state)
}

function collectFieldAccesses(ast: t.File, state: AnalysisState): void {
  const visit = (path: NodePath<t.MemberExpression | t.OptionalMemberExpression>) => {
    const value = resolveValue(path, state)
    if (value && value.fields.length > 0) addSelectedFields(value)
  }
  traverse(ast, {
    MemberExpression: visit,
    OptionalMemberExpression: visit,
  })
}

function addSelectedFields(value: ValueBinding): void {
  addFieldChain(value.usage.tree, value.fields)
}

function collectEscapes(ast: t.File, state: AnalysisState): void {
  traverse(ast, {
    Identifier(path) {
      if (!path.isReferencedIdentifier()) return
      const value = lookupIdentifier(path, state)
      if (value && !isSafeReference(path)) value.usage.escaped = true
    },
  })
}

function isSafeReference(path: NodePath<t.Identifier>): boolean {
  let current: NodePath<t.Node> = path
  while (isTransparentExpression(current.parentPath)) current = current.parentPath!
  const parent = current.parentPath
  if (!parent) return false
  if (parent.isVariableDeclarator() && parent.node.init === current.node) return true
  if (parent.isForOfStatement() && parent.node.right === current.node) return true
  if (!parent.isMemberExpression() && !parent.isOptionalMemberExpression()) return false
  if (parent.node.object !== current.node) return false
  return !parent.node.computed || isStaticMemberProperty(parent.node.property)
}

function isTransparentExpression(path: NodePath<t.Node> | null): boolean {
  return Boolean(path?.isTSAsExpression() || path?.isTSNonNullExpression() || path?.isTypeCastExpression())
}

function resolveValue(path: NodePath<t.Node> | null, state: AnalysisState): ValueBinding | null {
  if (!path?.node) return null
  const expression = unwrapExpression(path)
  if (expression.isIdentifier()) return lookupIdentifier(expression, state)
  if (!expression.isMemberExpression() && !expression.isOptionalMemberExpression()) return null

  const base = resolveValue(asNodePath(expression.get('object')), state)
  if (!base) return null
  if (expression.node.computed && !isStaticMemberProperty(expression.node.property)) return null
  const name = memberPropertyName(expression.node)
  return name === null ? resolveIndexedValue(base, expression.node.property) : resolveProperty(base, name, state.schema)
}

function resolveIndexedValue(base: ValueBinding, property: t.Expression | t.PrivateName): ValueBinding | null {
  if (!t.isNumericLiteral(property) && !(t.isStringLiteral(property) && /^\d+$/.test(property.value))) return null
  if (base.shape !== 'list') return null
  return { ...base, shape: 'object' }
}

function resolveProperty(base: ValueBinding, name: string, schema: LuxoSchema): ValueBinding | null {
  if (base.shape === 'page') {
    if (name === 'items') return { ...base, shape: 'list' }
    return PAGE_FIELDS.has(name) ? { ...base, shape: 'scalar' } : null
  }
  if (base.shape === 'list') return name === 'length' ? { ...base, shape: 'scalar' } : null
  if (base.shape !== 'object') return null

  const field = declaration(schema, base.typeName)?.fields.find(candidate => candidate.name === name)
  if (!field) return null
  return fieldValue(base, field, schema)
}

function fieldValue(base: ValueBinding, field: LuxoField, schema: LuxoSchema): ValueBinding {
  const typeName = field.typeName || field.type
  const nested = Boolean(declaration(schema, typeName))
  let shape: ValueShape = nested ? 'object' : 'scalar'
  if (field.isList || field.list) shape = 'list'
  return { ...base, typeName, fields: [...base.fields, field.name], shape }
}

function lookupIdentifier(path: NodePath<t.Identifier>, state: AnalysisState): ValueBinding | null {
  const binding = path.scope.getBinding(path.node.name)
  return binding ? state.bindings.get(binding.identifier) ?? null : null
}

function declaration(schema: LuxoSchema, name: string): SchemaDeclaration | undefined {
  return schema.models[name] ?? schema.types?.[name]
}

function unwrapExpression(path: NodePath<t.Node>): NodePath<t.Node> {
  let current = path
  while (true) {
    if (current.isAwaitExpression()) {
      current = asNodePath(current.get('argument')) ?? current
      continue
    }
    if (current.isTSAsExpression() || current.isTSNonNullExpression() || current.isTypeCastExpression()) {
      current = asNodePath(current.get('expression')) ?? current
      continue
    }
    return current
  }
}

function createSelectionEdit(code: string, usage: CallUsage, schema: LuxoSchema): TextEdit[] {
  if (hasManualSelection(usage.call)) return []
  if (usage.escaped) addSafeProjection(usage.tree, usage.api.returnType ?? '', schema)
  completeStructuredLeaves(usage.tree, usage.api.returnType ?? '', schema)
  if (usage.tree.children.size === 0) addMinimalProjection(usage.tree, usage.api.returnType ?? '', schema)
  const selection = usage.tree.toSelectString()
  if (!selection) return []
  warnDeepSelection(usage.api.name, usage.tree)

  const first = usage.call.arguments[0]
  if (!first) return insertEmptyParams(usage.call, selection)
  if (t.isObjectExpression(first)) return mergeObjectParams(first, selection)
  return wrapDynamicParams(code, first, selection)
}

function addSafeProjection(
  root: FieldNode,
  typeName: string,
  schema: LuxoSchema,
  ancestors: ReadonlySet<string> = new Set(),
): void {
  const type = declaration(schema, typeName)
  if (!type) return
  const recursive = ancestors.has(typeName)
  const nextAncestors = new Set(ancestors).add(typeName)
  const model = schema.models[typeName]

  for (const field of type.fields) {
    const nestedType = field.typeName || field.type
    const nested = declaration(schema, nestedType)
    if (!nested) {
      root.addChild(field.name)
      continue
    }
    if (recursive) continue
    if (model && field.relation) continue
    const child = root.addChild(field.name)
    addSafeProjection(child, nestedType, schema, nextAncestors)
    if (child.children.size === 0) root.children.delete(field.name)
  }
}

function completeStructuredLeaves(
  root: FieldNode,
  typeName: string,
  schema: LuxoSchema,
  ancestors: ReadonlySet<string> = new Set(),
): void {
  const type = declaration(schema, typeName)
  if (!type || ancestors.has(typeName)) return
  const nextAncestors = new Set(ancestors).add(typeName)

  for (const child of root.children.values()) {
    const field = type.fields.find(candidate => candidate.name === child.name)
    if (!field) continue
    const nestedType = field.typeName || field.type
    if (!declaration(schema, nestedType)) continue
    if (child.children.size === 0) addSafeProjection(child, nestedType, schema, nextAncestors)
    else completeStructuredLeaves(child, nestedType, schema, nextAncestors)
  }
}

function addMinimalProjection(
  root: FieldNode,
  typeName: string,
  schema: LuxoSchema,
  ancestors: ReadonlySet<string> = new Set(),
): boolean {
  const type = declaration(schema, typeName)
  if (!type) return false
  const model = schema.models[typeName]
  const scalar = type.fields.find(field => !declaration(schema, field.typeName || field.type))
  if (scalar) {
    root.addChild(scalar.name)
    return true
  }
  if (ancestors.has(typeName)) return false

  const nextAncestors = new Set(ancestors).add(typeName)
  for (const field of type.fields) {
    if (model && field.relation) continue
    const nestedType = field.typeName || field.type
    if (!declaration(schema, nestedType)) continue
    const child = root.addChild(field.name)
    if (addMinimalProjection(child, nestedType, schema, nextAncestors)) return true
    root.children.delete(field.name)
  }
  return false
}

function hasManualSelection(call: t.CallExpression): boolean {
  const first = call.arguments[0]
  if (!t.isObjectExpression(first)) return false
  return first.properties.some(property => {
    if (!t.isObjectProperty(property) && !t.isObjectMethod(property)) return false
    return staticPropertyName(property.key, property.computed) === '$select'
  })
}

function insertEmptyParams(call: t.CallExpression, selection: string): TextEdit[] {
  if (call.end === null || call.end === undefined) return []
  const point = call.end - 1
  return [{ start: point, end: point, replacement: `{ $select: '${selection}' }` }]
}

function mergeObjectParams(params: t.ObjectExpression, selection: string): TextEdit[] {
  if (params.start === null || params.start === undefined || params.end === null || params.end === undefined) return []
  if (params.properties.length === 0) {
    return [{ start: params.start, end: params.end, replacement: `{ $select: '${selection}' }` }]
  }
  return [{ start: params.start + 1, end: params.start + 1, replacement: ` $select: '${selection}',` }]
}

function wrapDynamicParams(
  code: string,
  params: t.Expression | t.SpreadElement | t.JSXNamespacedName | t.ArgumentPlaceholder,
  selection: string,
): TextEdit[] {
  if (params.start === null || params.start === undefined || params.end === null || params.end === undefined) return []
  const source = code.slice(params.start, params.end)
  return [{
    start: params.start,
    end: params.end,
    replacement: `{ $select: '${selection}', ...(${source}) }`,
  }]
}

function warnDeepSelection(apiName: string, tree: FieldNode): void {
  const depth = tree.maxDepth()
  if (depth <= MAX_NESTING_DEPTH) return
  console.warn(
    `[luxo] Warning: ${apiName} has ${depth}-level nested field selection (max recommended: ${MAX_NESTING_DEPTH}). ` +
    'Deep nesting may cause performance issues. Consider using @native or restructuring your query.'
  )
}

function applyTextEdits(code: string, edits: TextEdit[]): string {
  let result = code
  for (const edit of [...edits].sort((a, b) => b.start - a.start)) {
    result = result.slice(0, edit.start) + edit.replacement + result.slice(edit.end)
  }
  return result
}

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
    const parts: string[] = []
    for (const child of this.children.values()) {
      const nested = child.toSelectString()
      parts.push(nested ? `${child.name}{${nested}}` : child.name)
    }
    return parts.join(',')
  }

  maxDepth(): number {
    let max = 0
    for (const child of this.children.values()) max = Math.max(max, child.maxDepth())
    return this.children.size === 0 ? 0 : max + 1
  }
}

function addFieldChain(root: FieldNode, fields: string[]): void {
  let current = root
  for (const field of fields) current = current.addChild(field)
}

function memberPropertyName(node: t.MemberExpression | t.OptionalMemberExpression): string | null {
  if (!node.computed && t.isIdentifier(node.property)) return node.property.name
  return staticPropertyName(node.property, node.computed)
}

function streamMethodName(apiName: string): string {
  return `subscribe${apiName.charAt(0).toUpperCase()}${apiName.slice(1)}`
}

function objectPropertyName(node: t.ObjectProperty): string | null {
  return staticPropertyName(node.key, node.computed)
}

function staticPropertyName(property: t.Expression | t.PrivateName, computed: boolean): string | null {
  if (!computed && t.isIdentifier(property)) return property.name
  if (t.isStringLiteral(property)) return property.value
  return null
}

function isStaticMemberProperty(property: t.Expression | t.PrivateName): boolean {
  return t.isNumericLiteral(property) || t.isStringLiteral(property)
}

function asNodePath(value: NodePath<t.Node> | NodePath<t.Node>[] | null | undefined): NodePath<t.Node> | null {
  if (!value || Array.isArray(value)) return null
  return value
}
