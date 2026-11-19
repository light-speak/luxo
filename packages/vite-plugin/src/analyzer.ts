import { parse } from '@babel/parser'
import traverse, { type Binding, type NodePath } from '@babel/traverse'
import * as t from '@babel/types'
import type { LuxoAPI, LuxoField, LuxoModel, LuxoSchema, LuxoTypeDecl } from '@luxojs/client'
import { schemaEntry } from './codegen-response'

const MAX_NESTING_DEPTH = 5
const ARRAY_CALLBACKS = new Set(['every', 'filter', 'find', 'flatMap', 'forEach', 'map', 'some'])
const PAGE_FIELDS = new Set(['page', 'pageSize', 'total'])

type SchemaDeclaration = LuxoModel | LuxoTypeDecl
const sealedCatalogue = Symbol('sealed selection catalogue')
interface SelectionSchema extends LuxoSchema {
  readonly [sealedCatalogue]?: ReadonlyMap<string, SchemaDeclaration>
}
type ValueShape = 'object' | 'list' | 'page' | 'scalar'
type TransparentExpression = t.TSAsExpression | t.TSTypeAssertion | t.TSSatisfiesExpression |
  t.TSNonNullExpression | t.TypeCastExpression | t.ParenthesizedExpression
type CollectionCall = t.CallExpression | t.OptionalCallExpression
type MemberCall = t.CallExpression & { callee: t.MemberExpression | t.OptionalMemberExpression }

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

interface AggregateBinding {
  // Each static member keeps its own originating API call and field path.
  shape: 'aggregate'
  kind: 'object' | 'array'
  members: Map<string, TrackedBinding>
  element?: TrackedBinding | null
  position?: 'unknown'
}

interface ChoiceBinding {
  shape: 'choice'
  values: Array<ValueBinding | AggregateBinding>
}

type TrackedBinding = ValueBinding | AggregateBinding | ChoiceBinding

export interface TextEdit {
  start: number
  end: number
  replacement: string
}

interface AnalysisState {
  schema: SelectionSchema
  apis: Map<string, LuxoAPI>
  streamAPIs: Map<string, LuxoAPI>
  bindings: Map<t.Identifier, TrackedBinding>
  aggregateLiterals?: Map<t.Node, AggregateBinding>
  defaultSources?: Set<t.AssignmentPattern>
  readonlyBindings?: Set<t.Identifier>
  usages: CallUsage[]
  usagesByCall: Map<t.CallExpression, CallUsage>
  verifiedMembers?: ReadonlySet<number>
}

export interface SelectionCandidate {
  memberStart: number
}

/** String-only consumers share the same verified edit analysis as the Vite plugin. */
export async function analyzeVerifiedCalls(
  code: string, id: string, schema: LuxoSchema,
  verify: (calls: readonly SelectionCandidate[]) => Promise<ReadonlySet<number>>,
): Promise<string | null> {
  const edits = await analyzeVerifiedEdits(code, id, schema, verify)
  return edits ? applyTextEdits(code, edits) : null
}

/** Verify declaration identity before tracing field usage; retain original source offsets. */
export async function analyzeVerifiedEdits(
  code: string, id: string, schema: LuxoSchema,
  verify: (calls: readonly SelectionCandidate[]) => Promise<ReadonlySet<number>>,
): Promise<TextEdit[] | null> {
  const ast = parseSource(code, id)
  try {
    const state = createAnalysisState(schema)
    const candidates: SelectionCandidate[] = []
    traverse(ast, { CallExpression(path) {
      const call = findAPICall(path, state)
      if (!call || hasManualSelection(call.path.node)) return
      // findAPICall validates the member; parseSource always retains its position.
      candidates.push({ memberStart: call.path.node.callee.property.start! })
    } })
    if (candidates.length === 0) return null
    state.verifiedMembers = await verify(candidates)
    return collectTextEdits(code, ast, state, true)
  } finally {
    releaseParsedAST(ast)
  }
}

/**
 * Tracks statically visible result usage and injects a safe compile-time
 * selection into generated client's params object.
 * Isolated fixtures supply trusted snippets here; production uses analyzeVerifiedCalls.
 */
export function analyzeAndTransform(code: string, id: string, schema: LuxoSchema, preparedSelections = false): string | null {
  const ast = parseSource(code, id)
  try {
    const state = createAnalysisState(schema)
    return transformParsedSource(code, ast, state, preparedSelections)
  } finally {
    releaseParsedAST(ast)
  }
}

// Parsed trees belong to this analysis; release their Babel cache keys on every exit.
function releaseParsedAST(ast: t.File): void {
  t.traverseFast(ast, releaseCachedNode, traverse.cache)
}

function releaseCachedNode(node: t.Node, cache?: typeof traverse.cache): void {
  // releaseParsedAST always supplies the cache namespace as the walker's state.
  cache!.path.delete(node)
  cache!.scope.delete(node)
}

function transformParsedSource(code: string, ast: t.File, state: AnalysisState, preparedSelections: boolean): string | null {
  const edits = collectTextEdits(code, ast, state, preparedSelections)
  return edits ? applyTextEdits(code, edits) : null
}

function collectTextEdits(code: string, ast: t.File, state: AnalysisState, preparedSelections: boolean): TextEdit[] | null {
  collectBindings(ast, state)
  collectStandaloneCalls(ast, state)
  if (state.usages.every(usage => hasManualSelection(usage.call))) return null
  collectFieldAccesses(ast, state)
  collectEscapes(ast, state)

  let prefix = '$luxoSelection'
  while (code.includes(prefix)) prefix += '_'
  const plans: string[] = []
  let spread = false
  const expression = (selection: string, spreadArgs: boolean): string => {
    spread ||= spreadArgs
    if (!preparedSelections) return `'${selection}'`
    const name = `${prefix}${plans.length}`
    plans.push(`const ${name} = ${prefix}Prepare(${JSON.stringify(selection)});`)
    return name
  }
  const edits = state.usages.flatMap(usage => createSelectionEdit(usage, state.schema, expression, `${prefix}Args`))
  if (plans.length || spread) {
    const directives = ast.program.directives
    const point = directives.at(-1)?.end ?? ast.program.interpreter?.end ?? 0
    const imports = plans.length ? `import { prepareSelection as ${prefix}Prepare } from '@luxojs/client';\n` : ''
    const helper = spread ? spreadArgumentHelper(`${prefix}Args`) : ''
    edits.push({ start: point, end: point, replacement: `\n${imports}${plans.join('\n')}\n${helper}` })
  }
  if (edits.length === 0) return null
  return edits
}

function parseSource(code: string, id: string): t.File {
  const sourceID = id.split('?', 1)[0]!
  try {
    return parse(code, {
      sourceType: 'module',
      sourceFilename: id,
      createParenthesizedExpressions: true,
      plugins: [
        ['typescript', { disallowAmbiguousJSXLike: /\.[mc]ts$/.test(sourceID) }],
        ...(sourceID.endsWith('.tsx') ? ['jsx' as const] : []),
        'decorators', 'decoratorAutoAccessors',
      ],
    })
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error)
    throw new SyntaxError(`[luxo] Cannot analyze ${id}: ${message}`, { cause: error })
  }
}

function createAnalysisState(schema: LuxoSchema): AnalysisState {
  let selectionSchema: SelectionSchema = schema
  if (Object.keys(schema.sealeds ?? {}).length) {
    const catalogue = new Map<string, SchemaDeclaration>()
    for (const owner of Object.values(schema.sealeds ?? {})) {
      catalogue.set(owner.name, { name: owner.name, fields: owner.variants.map(variant => ({
        id: variant.id, name: variant.name, type: 'Model', typeName: `${owner.name}.${variant.name}`,
      })) })
      for (const variant of owner.variants) catalogue.set(`${owner.name}.${variant.name}`, {
        name: `${owner.name}.${variant.name}`, fields: variant.fields,
      })
    }
    selectionSchema = { ...schema, [sealedCatalogue]: catalogue }
  }
  return {
    schema: selectionSchema,
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
    OptionalCallExpression(path) {
      bindArrayCallback(path, state)
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

function readOnlyAggregateTarget(path: NodePath<t.Node>, value: TrackedBinding, schema: LuxoSchema): Binding | null | false {
  // Mutable or opaque containers must not acquire a stale member identity.
  let current = path
  while (isTransparentExpression(current.parentPath)) current = current.parentPath!
  const start = current
  let source: TrackedBinding | null = value
  while (current.parentPath?.isMemberExpression() || current.parentPath?.isOptionalMemberExpression()) {
    const parent = current.parentPath
    if (parent.node.object !== current.node || (parent.node.computed && !isStaticMemberProperty(parent.node.property))) return false
    if (isKnownArrayCall(parent, source)) return null
    const name = memberPropertyName(parent.node)
    source = source ? (parent.node.computed ? resolveIndexedValue(source, parent.node.property) : null) ??
      (name === null ? null : resolveProperty(source, name, schema)) : null
    current = parent
    while (isTransparentExpression(current.parentPath)) current = current.parentPath!
  }
  const parent = current.parentPath
  if (current === start && parent?.isVariableDeclarator() && parent.node.init === current.node) {
    const id = parent.get('id')
    // Parsed variable declarations have an identifier or a binding pattern.
    if (!id.isIdentifier()) return null
    const binding = id.scope.getBinding(id.node.name)
    return binding?.constant ? binding : false
  }
  if (current === start) return false
  const safe = !(parent?.isAssignmentExpression() && parent.node.left === current.node) &&
    !parent?.isUpdateExpression() && !(parent?.isUnaryExpression() && parent.node.operator === 'delete') &&
    !((parent?.isCallExpression() || parent?.isOptionalCallExpression()) && parent.node.callee === current.node)
  return safe ? null : false
}

function isReadOnlyAggregateBinding(binding: Binding, value: TrackedBinding, state: AnalysisState): boolean {
  if (state.readonlyBindings?.has(binding.identifier)) return true
  const pending = [binding]
  const visited: Binding[] = []
  // A constant alias has one initializer. Reverse alias edges reachable from
  // a literal root form a DAG; use a worklist and memoize every verified alias.
  while (pending.length) {
    const current = pending.pop()!
    if (!current.constant) return false
    visited.push(current)
    for (const reference of current.referencePaths) {
      const target = readOnlyAggregateTarget(reference, value, state.schema)
      if (target === false) return false
      if (target) pending.push(target)
    }
  }
  const cache = state.readonlyBindings ??= new Set()
  for (const current of visited) cache.add(current.identifier)
  return true
}

function markAggregateEscape(value: TrackedBinding): void {
  if (value.shape === 'aggregate') {
    for (const member of value.members.values()) markAggregateEscape(member)
  } else if (value.shape === 'choice') {
    for (const member of value.values) markAggregateEscape(member)
  } else markValueEscape(value)
}

function markValueEscape(value: ValueBinding): void {
  if (value.shape === 'scalar') return
  if (value.fields.length === 0) value.usage.escaped = true
  else addFieldChain(value.usage.tree, value.fields).escaped = true
}

function resolveAggregateLiteral(
  path: NodePath<t.ObjectExpression | t.ArrayExpression>, state: AnalysisState,
): AggregateBinding | null {
  let members: Map<string, TrackedBinding> | undefined
  const reject = (): null => {
    if (members) for (const value of members.values()) markAggregateEscape(value)
    return null
  }
  const add = (name: string, item: NodePath<t.Node> | null) => {
    if (!item?.node) return
    const call = findAPICall(item, state)
    const value = call && !call.stream ? rootValue(callUsage(call.api, call.path.node, state)) : resolveValue(item, state)
    if (value && value.shape !== 'scalar') (members ??= new Map()).set(name, value)
    else members?.delete(name)
  }
  if (path.isObjectExpression()) {
    for (const property of path.get('properties')) {
      if (!property.isObjectProperty()) return reject()
      const name = objectPropertyName(property.node) ?? (t.isNumericLiteral(property.node.key) ? String(property.node.key.value) : null)
      if (name === null || name === '__proto__') return reject()
      add(name, asNodePath(property.get('value')))
    }
  } else if (path.isArrayExpression()) {
    for (const [index, item] of path.get('elements').entries()) {
      if (item.isSpreadElement()) return reject()
      add(String(index), asNodePath(item))
    }
  }
  if (!members?.size) return null
  const value: AggregateBinding = { shape: 'aggregate', kind: path.isArrayExpression() ? 'array' : 'object', members }
  ;(state.aggregateLiterals ??= new Map()).set(path.node, value)
  return value
}

function mergeTracked(left: TrackedBinding | null, right: TrackedBinding | null): TrackedBinding | null {
  if (!left) return right
  if (!right || sameTrackedSource(left, right)) return left
  const values = left.shape === 'choice' ? [...left.values] : [left]
  const append = (value: ValueBinding | AggregateBinding) => {
    if (!values.some(existing => sameTrackedSource(existing, value))) values.push(value)
  }
  if (right.shape === 'choice') right.values.forEach(append)
  else append(right)
  return { shape: 'choice', values }
}

function sameTrackedSource(left: TrackedBinding, right: TrackedBinding): boolean {
  if (left === right) return true
  if (left.shape === 'aggregate' || left.shape === 'choice' || right.shape === 'aggregate' || right.shape === 'choice') return false
  return left.usage === right.usage && left.shape === right.shape && left.typeName === right.typeName &&
    left.fields.length === right.fields.length && left.fields.every((field, index) => field === right.fields[index])
}

function mapChoices(value: ChoiceBinding, project: (member: ValueBinding | AggregateBinding) => TrackedBinding | null): TrackedBinding | null {
  let result: TrackedBinding | null = null
  for (const member of value.values) result = mergeTracked(result, project(member))
  return result
}

function objectRest(value: TrackedBinding, excluded: ReadonlySet<string>): TrackedBinding | null {
  if (value.shape === 'choice') return mapChoices(value, member => objectRest(member, excluded))
  if (value.shape === 'aggregate') return { shape: 'aggregate', kind: 'object', members: new Map([...value.members].filter(([name]) => !excluded.has(name))) }
  // Preserve the existing opaque model-rest policy; literal containers remain exact.
  markValueEscape(value)
  return value
}

function arrayRest(value: TrackedBinding, start: number): TrackedBinding | null {
  if (value.shape === 'choice') return mapChoices(value, member => arrayRest(member, start))
  if (value.shape === 'aggregate') {
    if (value.position === 'unknown') return value
    const members = new Map<string, TrackedBinding>()
    for (const [name, member] of value.members) {
      const index = Number(name)
      if (Number.isInteger(index) && index >= start) members.set(String(index - start), member)
    }
    return { shape: 'aggregate', kind: 'array', members }
  }
  if (value.shape === 'list') return value
  markValueEscape(value)
  return null
}

function arrayItem(value: TrackedBinding, index: number): TrackedBinding | null {
  if (value.shape === 'choice') return mapChoices(value, member => arrayItem(member, index))
  if (value.shape === 'aggregate') return value.position === 'unknown' ? arrayElement(value) : value.members.get(String(index)) ?? null
  if (value.shape === 'list') return { ...value, shape: 'object' }
  markValueEscape(value)
  return null
}

function listProjection(value: TrackedBinding | null): TrackedBinding | null {
  if (value?.shape === 'choice') return mapChoices(value, member => listProjection(member))
  return value?.shape === 'list' || (value?.shape === 'aggregate' && value.kind === 'array') ? value : null
}

function arrayElement(value: TrackedBinding): TrackedBinding | null {
  if (value.shape === 'choice') return mapChoices(value, member => arrayElement(member))
  if (value.shape !== 'aggregate') return arrayItem(value, 0)
  if (value.element !== undefined) return value.element
  let result: TrackedBinding | null = null
  for (const member of value.members.values()) result = mergeTracked(result, member)
  value.element = result
  return result
}

function filteredList(value: TrackedBinding): TrackedBinding | null {
  if (value.shape === 'choice') return mapChoices(value, member => filteredList(member))
  return value.shape === 'aggregate' ? { ...value, position: 'unknown', element: arrayElement(value) } : value
}

function allArraySources(value: TrackedBinding | null): boolean {
  if (value?.shape === 'choice') return value.values.every(allArraySources)
  return value?.shape === 'list' || (value?.shape === 'aggregate' && value.kind === 'array')
}

function isKnownArrayCall(path: NodePath<t.MemberExpression | t.OptionalMemberExpression>, value: TrackedBinding | null): boolean {
  const method = memberPropertyName(path.node)
  let callee: NodePath<t.Node> = path
  while (isTransparentExpression(callee.parentPath)) callee = callee.parentPath!
  const parent = callee.parentPath
  return Boolean(method && ARRAY_CALLBACKS.has(method) && allArraySources(value) &&
    (parent?.isCallExpression() || parent?.isOptionalCallExpression()) && parent.node.callee === callee.node)
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
): { api: LuxoAPI; path: NodePath<MemberCall>; stream: boolean } | null {
  const expression = unwrapExpression(path)
  if (!expression.isCallExpression()) return null
  const callee = asNodePath(expression.get('callee'))
  if (!callee || (!callee.isMemberExpression() && !callee.isOptionalMemberExpression())) return null
  if (state.verifiedMembers && !state.verifiedMembers.has(callee.node.property.start ?? -1)) return null
  const name = memberPropertyName(callee.node)
  const api = name ? state.apis.get(name) ?? state.streamAPIs.get(name) : undefined
  if (!api?.returnType || !declaration(state.schema, api.returnType)) return null
  if (schemaEntry(state.schema.sealeds, api.returnType)?.variants.length === 0) return null
  return { api, path: expression as NodePath<MemberCall>, stream: Boolean(name && state.streamAPIs.has(name)) }
}

function bindPattern(path: NodePath<t.Node> | null, value: TrackedBinding | null, state: AnalysisState): void {
  if (!path) return
  if (path.isAssignmentPattern()) {
    const right = asNodePath(path.get('right'))!
    const api = findAPICall(right, state)
    const fallback = api && !api.stream ? rootValue(callUsage(api.api, api.path.node, state)) : resolveValue(right, state)
    const choices = mergeTracked(value, fallback)
    if (choices) {
      ;(state.defaultSources ??= new Set()).add(path.node)
      bindPattern(asNodePath(path.get('left')), choices, state)
    }
    return
  }
  if (!value) return
  if (path.isRestElement()) {
    bindPattern(asNodePath(path.get('argument')), value, state)
    return
  }
  if (path.isIdentifier()) {
    bindIdentifier(path, value, state)
    return
  }
  if (path.isObjectPattern()) {
    bindObjectPattern(path, value, state)
    return
  }
  if (path.isArrayPattern()) bindArrayPattern(path, value, state)
  else markAggregateEscape(value)
}

function bindIdentifier(path: NodePath<t.Identifier>, value: TrackedBinding, state: AnalysisState): void {
  if (value.shape === 'scalar') return
  const binding = path.scope.getBinding(path.node.name)
  if (!binding) {
    markAggregateEscape(value)
    return
  }
  const aggregate = value.shape === 'aggregate' || (value.shape === 'choice' && value.values.some(member => member.shape === 'aggregate'))
  if (aggregate && !isReadOnlyAggregateBinding(binding, value, state)) {
    markAggregateEscape(value)
    return
  }
  state.bindings.set(binding.identifier, value)
}

function bindObjectPattern(path: NodePath<t.ObjectPattern>, value: TrackedBinding, state: AnalysisState): void {
  const properties = path.get('properties')
  for (const [index, propertyPath] of properties.entries()) {
    if (propertyPath.isRestElement()) {
      const excluded = new Set<string>()
      for (const previous of properties.slice(0, index)) {
        if (previous.isObjectProperty()) {
          const name = objectPropertyName(previous.node)
          if (name !== null) excluded.add(name)
        }
      }
      bindPattern(asNodePath(propertyPath.get('argument')), objectRest(value, excluded), state)
      continue
    }
    if (!propertyPath.isObjectProperty()) continue
    const name = objectPropertyName(propertyPath.node)
    if (!name) {
      markAggregateEscape(value)
      continue
    }
    const child = resolveProperty(value, name, state.schema)
    if (child) addSelectedFields(child)
    bindPattern(asNodePath(propertyPath.get('value')), child, state)
  }
}

function bindArrayPattern(path: NodePath<t.ArrayPattern>, value: TrackedBinding, state: AnalysisState): void {
  for (const [index, element] of path.get('elements').entries()) {
    if (element.isRestElement()) bindPattern(asNodePath(element.get('argument')), arrayRest(value, index), state)
    else bindPattern(asNodePath(element), arrayItem(value, index), state)
  }
}

function bindArrayCallback(path: NodePath<CollectionCall>, state: AnalysisState): void {
  const callee = unwrapExpression(path.get('callee'))
  if (!callee?.isMemberExpression() && !callee?.isOptionalMemberExpression()) return
  const method = memberPropertyName(callee.node)
  if (!method || !ARRAY_CALLBACKS.has(method)) return

  const rawSource = resolveValue(asNodePath(callee.get('object')), state)
  const source = listProjection(rawSource)
  if (rawSource && !source) markAggregateEscape(rawSource)
  if (!source) return
  if ((method === 'filter' || method === 'find') && !isSafeReference(path)) markAggregateEscape(source)
  const argument = asNodePath(path.get('arguments')[0])
  const callback = argument ? unwrapExpression(argument) : null
  if (!callback?.isArrowFunctionExpression() && !callback?.isFunctionExpression()) {
    markAggregateEscape(source)
    return
  }
  bindCallbackParameters(callback.get('params'), arrayElement(source), source, state)
}

function bindCallbackParameters(
  params: readonly NodePath<t.Node>[], item: TrackedBinding | null,
  list: TrackedBinding | null, state: AnalysisState,
): void {
  for (const [index, parameter] of params.entries()) {
    if (parameter.isRestElement()) {
      const members = new Map<string, TrackedBinding>()
      if (index === 0 && item) members.set('0', item)
      if (index <= 2 && list) members.set(String(2 - index), list)
      bindPattern(parameter, { shape: 'aggregate', kind: 'array', members }, state)
    } else bindPattern(parameter, index === 0 ? item : index === 2 ? list : null, state)
  }
}

function bindStreamCallback(path: NodePath<t.CallExpression>, state: AnalysisState): void {
  const apiCall = findAPICall(path, state)
  if (!apiCall?.stream) return

  const usage = callUsage(apiCall.api, apiCall.path.node, state)
  const args = apiCall.path.get('arguments')
  // Structured streams always have params before onData; observer is optional.
  // Only onData is callable in the generated signature. With a spread prefix,
  // require a unique visible function instead of guessing its argument offset.
  const argument = t.isSpreadElement(args[0]?.node) ? spreadDataCallback(args) : asNodePath(args[1])
  const callback = argument ? unwrapExpression(argument) : null
  if (!callback?.isArrowFunctionExpression() && !callback?.isFunctionExpression()) {
    usage.escaped = true
    return
  }
  bindCallbackParameters(callback.get('params'), rootValue(usage), null, state)
}

function spreadDataCallback(args: readonly NodePath<t.Node>[]): NodePath<t.Node> | null {
  let found: NodePath<t.Node> | null = null
  for (const argument of args) {
    let current: NodePath<t.Node> | null = argument
    while (isTransparentExpression(current)) {
      const node = current.node
      // An opaque assertion can turn a function into an observer object.
      if ((t.isTSAsExpression(node) || t.isTSTypeAssertion(node) || t.isTSSatisfiesExpression(node)) &&
          !t.isTSFunctionType(node.typeAnnotation)) {
        current = null
        break
      }
      current = asNodePath(current.get('expression'))
    }
    if (!current?.isArrowFunctionExpression() && !current?.isFunctionExpression()) continue
    if (found || current.node.params.length === 0) return null
    found = current
  }
  return found
}

function bindForOfItem(path: NodePath<t.ForOfStatement>, state: AnalysisState): void {
  const source = listProjection(resolveValue(asNodePath(path.get('right')), state))
  if (!source) return
  const left = asNodePath(path.get('left'))
  const target = left?.isVariableDeclaration() ? asNodePath(left.get('declarations')[0]?.get('id')) : left
  bindPattern(target, arrayElement(source), state)
}

function collectFieldAccesses(ast: t.File, state: AnalysisState): void {
  const visit = (path: NodePath<t.MemberExpression | t.OptionalMemberExpression>) => {
    const value = resolveValue(path, state)
    if (!value) {
      const source = resolveValue(asNodePath(path.get('object')), state)
      if (hasSealedSource(source, state.schema) && source) markAggregateEscape(source)
      return
    }
    addSelectedFields(value)
    if (value.shape !== 'scalar' &&
        (!(isSafeReference(path) || isSafeAggregateSlot(path, state) || isSafeSealedChoiceReference(path, state) || sealedPresenceCheck(path, value, state.schema)) || isOpaqueTrackedCall(path, state, value))) {
      // Field tracing already has this value; escape flags commute with later
      // field reads and are expanded only after all projections are collected.
      markAggregateEscape(value)
    }
  }
  traverse(ast, {
    MemberExpression: visit,
    OptionalMemberExpression: visit,
  })
}

function addSelectedFields(value: TrackedBinding): void {
  if (value.shape === 'aggregate') {
    for (const member of value.members.values()) addSelectedFields(member)
  } else if (value.shape === 'choice') {
    value.values.forEach(addSelectedFields)
  } else addFieldChain(value.usage.tree, value.fields)
}

function collectEscapes(ast: t.File, state: AnalysisState): void {
  const visitValue = (path: NodePath<t.Node>) => {
    if ((isSafeReference(path) || isSafeAggregateSlot(path, state) || isSafeSealedChoiceReference(path, state)) && !isOpaqueTrackedCall(path, state)) return
    const value = resolveValue(path, state)
    if (value && !sealedPresenceCheck(path, value, state.schema)) markAggregateEscape(value)
  }
  traverse(ast, {
    Identifier(path) {
      if (!path.isReferencedIdentifier()) return
      visitValue(path)
    },
    ObjectExpression: visitValue,
    ArrayExpression: visitValue,
  })
}

function isOpaqueTrackedCall(path: NodePath<t.Node>, state: AnalysisState, known?: TrackedBinding): boolean {
  let current = path
  while (isTransparentExpression(current.parentPath)) current = current.parentPath!
  const member = current.parentPath
  if ((!member?.isMemberExpression() && !member?.isOptionalMemberExpression()) || member.node.object !== current.node) return false
  let callee: NodePath<t.Node> = member
  while (isTransparentExpression(callee.parentPath)) callee = callee.parentPath!
  const call = callee.parentPath
  if ((!call?.isCallExpression() && !call?.isOptionalCallExpression()) || call.node.callee !== callee.node) return false
  const value = known ?? resolveValue(path, state)
  const structured = value?.shape === 'aggregate' || value?.shape === 'list' ||
    (value?.shape === 'choice' && value.values.some(source => source.shape === 'aggregate' || source.shape === 'list'))
  return Boolean(structured && !isKnownArrayCall(member, value))
}

function isSafeAggregateSlot(path: NodePath<t.Node>, state: AnalysisState): boolean {
  let current = path
  while (isTransparentExpression(current.parentPath)) current = current.parentPath!
  const parent = current.parentPath
  if (parent?.isAssignmentPattern() && parent.node.right === current.node && state.defaultSources?.has(parent.node)) return true
  if (parent?.isObjectProperty() && parent.node.value === current.node) {
    return Boolean(state.aggregateLiterals?.has(parent.parentPath.node))
  }
  return Boolean(parent?.isArrayExpression() && state.aggregateLiterals?.has(parent.node))
}

function isSafeReference(path: NodePath<t.Node>): boolean {
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

function isSafeSealedChoiceReference(path: NodePath<t.Node>, state: AnalysisState): boolean {
  let current = path
  while (isTransparentExpression(current.parentPath)) current = current.parentPath!
  const parent = current.parentPath
  if (!parent?.isConditionalExpression() || parent.node.test === current.node) return false
  if (!isSafeReference(parent) && !isSafeAggregateSlot(parent, state) && !isSafeSealedChoiceReference(parent, state)) return false
  return hasSealedSource(resolveValue(parent, state), state.schema)
}

function isTransparentExpression(path: NodePath<t.Node> | null): path is NodePath<TransparentExpression> {
  return Boolean(path?.isTSAsExpression() || path?.isTSTypeAssertion() || path?.isTSSatisfiesExpression() || path?.isTSNonNullExpression() ||
    path?.isTypeCastExpression() || path?.isParenthesizedExpression())
}

function resolveValue(path: NodePath<t.Node> | null, state: AnalysisState): TrackedBinding | null {
  if (!path?.node) return null
  const expression = unwrapExpression(path)
  if (expression.isIdentifier()) return narrowSealedValue(lookupIdentifier(expression, state), expression, state)
  if (expression.isConditionalExpression()) return resolveSealedChoice(expression, state)
  if (expression.isObjectExpression() || expression.isArrayExpression()) {
    return state.aggregateLiterals?.get(expression.node) ?? resolveAggregateLiteral(expression, state)
  }
  if (!expression.isMemberExpression() && !expression.isOptionalMemberExpression()) {
    return expression.isCallExpression() || expression.isOptionalCallExpression() ? resolveCollectionResult(expression, state) : null
  }

  const base = resolveValue(asNodePath(expression.get('object')), state)
  if (!base) return null
  if (expression.node.computed && !isStaticMemberProperty(expression.node.property)) return null
  const indexed = expression.node.computed ? resolveIndexedValue(base, expression.node.property) : null
  if (indexed) return indexed
  const name = memberPropertyName(expression.node)
  return name === null ? null : narrowSealedValue(resolveProperty(base, name, state.schema), expression, state)
}

function resolveSealedChoice(path: NodePath<t.ConditionalExpression>, state: AnalysisState): TrackedBinding | null {
  const operand = (branch: NodePath<t.Node>) => {
    const api = findAPICall(branch, state)
    return api && !api.stream ? rootValue(callUsage(api.api, api.path.node, state)) : resolveValue(branch, state)
  }
  const left = operand(path.get('consequent')), right = operand(path.get('alternate'))
  if (hasSealedSource(left, state.schema) && hasSealedSource(right, state.schema)) return mergeTracked(left, right)
  if (left) markAggregateEscape(left)
  if (right) markAggregateEscape(right)
  return null
}

function choiceValues(values: ValueBinding[]): TrackedBinding | null {
  if (!values.length) return null
  return values.length === 1 ? values[0] : { shape: 'choice', values }
}

function mapTracked(value: TrackedBinding, project: (value: ValueBinding) => ValueBinding): TrackedBinding | null {
  if (value.shape === 'choice') return mapChoices(value, member => mapTracked(member, project))
  return value.shape === 'aggregate' ? null : project(value)
}

function sealedPayloadOwner(schema: LuxoSchema, name: string): { owner: string; variant: string } | null {
  const dot = name.lastIndexOf('.')
  if (dot < 0) return null
  const owner = name.slice(0, dot), variant = name.slice(dot + 1)
  return schemaEntry(schema.sealeds, owner)?.variants.some(item => item.name === variant) ? { owner, variant } : null
}

function hasSealedSource(value: TrackedBinding | null, schema: LuxoSchema): boolean {
  if (!value) return false
  if (value.shape === 'choice') return value.values.some(member => hasSealedSource(member, schema))
  return value.shape === 'object' && Boolean(schemaEntry(schema.sealeds, value.typeName) || sealedPayloadOwner(schema, value.typeName))
}

function sealedPresenceCheck(path: NodePath<t.Node>, value: TrackedBinding, schema: LuxoSchema): boolean {
  let current = path
  while (isTransparentExpression(current.parentPath)) current = current.parentPath!
  const parent = current.parentPath
  if (!parent?.isBinaryExpression() || !['===', '!==', '==', '!='].includes(parent.node.operator)) return false
  if (!t.isNullLiteral(parent.node.left) && !t.isNullLiteral(parent.node.right)) return false
  if (value.shape === 'choice') return value.values.every(item => sealedPresenceCheck(path, item, schema))
  return value.shape !== 'aggregate' && Boolean(schemaEntry(schema.sealeds, value.typeName) || sealedPayloadOwner(schema, value.typeName))
}

function narrowSealedValue(value: TrackedBinding | null, path: NodePath<t.Node>, state: AnalysisState): TrackedBinding | null {
  if (!hasSealedSource(value, state.schema)) return value
  if (value?.shape === 'choice') return mapChoices(value, member => narrowSealedValue(member, path, state))
  if (!value || value.shape !== 'object') return value
  let result: TrackedBinding = value
  let current = path
  while (current.parentPath) {
    const parent = current.parentPath
    if (parent.isIfStatement() && (parent.node.consequent === current.node || parent.node.alternate === current.node)) {
      result = refineSealedCondition(result, parent.get('test'), parent.node.consequent === current.node, state) ?? result
    }
    if (current.isSwitchCase() && parent.isSwitchStatement()) {
      const names = switchCaseNames(current, parent)
      if (names) result = refineSealedCase(result, asNodePath(parent.get('discriminant')), names.cases, state, names.exclude) ?? result
    }
    current = parent
  }
  return result
}

function switchCaseNames(current: NodePath<t.SwitchCase>, owner: NodePath<t.SwitchStatement>): { cases: Set<string>; exclude: boolean } | null {
  const names = new Set<string>()
  const cases = owner.get('cases')
  const index = cases.findIndex(item => item.node === current.node)
  let fallback = false
  for (let at = index; at >= 0; at--) {
    const clause = cases[at].node
    if (clause.test === null) fallback = true
    else if (t.isStringLiteral(clause.test)) names.add(clause.test.value)
    else return null
    if (at === 0) break
    const previous = cases[at - 1].node.consequent.at(-1)
    if (terminatesSwitchCase(previous)) break
  }
  if (!fallback) return { cases: names, exclude: false }
  const excluded = new Set<string>()
  for (const clause of cases) {
    const test = clause.node.test
    if (t.isStringLiteral(test) && !names.has(test.value)) excluded.add(test.value)
    else if (test !== null && !t.isStringLiteral(test)) return null
  }
  return { cases: excluded, exclude: true }
}

function terminatesSwitchCase(node: t.Statement | undefined): boolean {
  if (t.isBlockStatement(node)) return terminatesSwitchCase(node.body.at(-1))
  if (t.isIfStatement(node)) return terminatesSwitchCase(node.consequent) && Boolean(node.alternate && terminatesSwitchCase(node.alternate))
  return t.isBreakStatement(node) || t.isReturnStatement(node) || t.isThrowStatement(node)
}

function refineSealedCondition(value: TrackedBinding | null, condition: NodePath<t.Node>, positive: boolean, state: AnalysisState): TrackedBinding | null {
  const expression = unwrapExpression(condition)
  if (expression.isUnaryExpression({ operator: '!' })) return refineSealedCondition(value, expression.get('argument'), !positive, state)
  if (expression.isLogicalExpression() && expression.node.operator !== '??') {
    const intersection = (expression.node.operator === '&&') === positive
    const left = refineSealedCondition(value, expression.get('left'), positive, state)
    const right = refineSealedCondition(intersection ? left ?? value : value, expression.get('right'), positive, state)
    return intersection ? right ?? left : left && right ? mergeTracked(left, right) : null
  }
  if (!expression.isBinaryExpression() || !['===', '==', '!==', '!='].includes(expression.node.operator)) return null
  const left = expression.get('left'), right = expression.get('right')
  const literal = left.isStringLiteral() ? left.node.value : right.isStringLiteral() ? right.node.value : null
  const member = left.isStringLiteral() ? asNodePath(right) : asNodePath(left)
  if (literal === null || !member) return null
  const equal = expression.node.operator === '===' || expression.node.operator === '=='
  return refineSealedCase(value, member, new Set([literal]), state, positive !== equal)
}

function sealedOrigin(value: ValueBinding, schema: LuxoSchema): { owner: string; fields: string[]; variant?: string } | null {
  const payload = sealedPayloadOwner(schema, value.typeName)
  if (payload) return { ...payload, fields: value.fields.slice(0, -1) }
  return schemaEntry(schema.sealeds, value.typeName) ? { owner: value.typeName, fields: value.fields } : null
}

function refineSealedCase(value: TrackedBinding | null, member: NodePath<t.Node> | null, cases: ReadonlySet<string>, state: AnalysisState, exclude = false): TrackedBinding | null {
  if (!value || !member) return null
  if (value.shape === 'choice') return mapChoices(value, item => refineSealedCase(item, member, cases, state, exclude))
  if (value.shape !== 'object') return null
  const expression = unwrapExpression(member)
  if ((!expression.isMemberExpression() && !expression.isOptionalMemberExpression()) || memberPropertyName(expression.node) !== '$case') return null
  const receiver = asNodePath(expression.get('object'))
  if (receiver?.isIdentifier() && !receiver.scope.getBinding(receiver.node.name)?.constant) return null
  const compared = resolveValue(receiver, state)
  if (!compared) return null
  const origin = sealedOrigin(value, state.schema)
  if (!origin) return null
  const matches = (compared.shape === 'choice' ? compared.values : [compared]).flatMap(item => {
    if (item.shape !== 'object') return []
    const other = sealedOrigin(item, state.schema)
    return other && origin.owner === other.owner && value.usage === item.usage &&
      origin.fields.length === other.fields.length && origin.fields.every((name, index) => name === other.fields[index]) ? [other] : []
  })
  if (!matches.length) return null
  const owner = schemaEntry(state.schema.sealeds, origin.owner)!
  const variants = owner.variants.filter(item => cases.has(item.name) !== exclude && (!origin.variant || origin.variant === item.name) &&
    matches.some(other => !other.variant || other.variant === item.name))
  return choiceValues(variants.map(variant => ({ ...value, typeName: `${owner.name}.${variant.name}`,
    fields: [...origin.fields, variant.name] })))
}

function resolveCollectionResult(path: NodePath<CollectionCall>, state: AnalysisState): TrackedBinding | null {
  const callee = unwrapExpression(path.get('callee'))
  if (!callee?.isMemberExpression() && !callee?.isOptionalMemberExpression()) return null
  // Filter preserves the list; find preserves one source element. Both retain
  // provenance, while map may change the element type and field path.
  const method = memberPropertyName(callee.node)
  if (method !== 'filter' && method !== 'find') return null
  const source = listProjection(resolveValue(asNodePath(callee.get('object')), state))
  if (!source) return null
  const argument = asNodePath(path.get('arguments')[0])
  const callback = argument && unwrapExpression(argument)
  if ((callback?.isArrowFunctionExpression() || callback?.isFunctionExpression()) && hasSealedSource(arrayElement(source), state.schema)) {
    bindCallbackParameters(callback.get('params'), arrayElement(source), source, state)
    let predicate = asNodePath(callback.get('body'))
    if (predicate?.isBlockStatement()) {
      const body = predicate.get('body')
      predicate = body.length === 1 && body[0].isReturnStatement() ? asNodePath(body[0].get('argument')) : null
    }
    const element = predicate && refineSealedCondition(arrayElement(source), predicate, true, state)
    if (element) return method === 'find' ? element : mapTracked(element, value => ({ ...value, shape: 'list' }))
  }
  return method === 'find' ? arrayElement(source) : filteredList(source)
}

function resolveIndexedValue(base: TrackedBinding, property: t.Expression | t.PrivateName): TrackedBinding | null {
  if (base.shape === 'choice') return mapChoices(base, member => resolveIndexedValue(member, property))
  if (!t.isNumericLiteral(property) && !(t.isStringLiteral(property) && /^\d+$/.test(property.value))) return null
  if (base.shape === 'aggregate') return base.position === 'unknown' ? arrayElement(base) :
    base.members.get(t.isNumericLiteral(property) ? String(property.value) : property.value) ?? null
  if (base.shape !== 'list') return null
  return { ...base, shape: 'object' }
}

function resolveProperty(base: TrackedBinding, name: string, schema: LuxoSchema): TrackedBinding | null {
  if (base.shape === 'choice') return mapChoices(base, member => resolveProperty(member, name, schema))
  if (base.shape === 'aggregate') return base.members.get(name) ?? null
  if (base.shape === 'page') {
    if (name === 'items') return { ...base, shape: 'list' }
    return PAGE_FIELDS.has(name) ? { ...base, shape: 'scalar' } : null
  }
  if (base.shape === 'list') return name === 'length' ? { ...base, shape: 'scalar' } : null
  if (base.shape !== 'object') return null

  const owner = schemaEntry(schema.sealeds, base.typeName)
  if (owner) {
    if (name === '$case') return choiceValues(owner.variants.map(variant => ({
      ...base, typeName: 'String', fields: [...base.fields, variant.name], shape: 'scalar',
    })))
    if (!owner.variants.every(variant => variant.fields.some(field => field.name === name))) return null
    return choiceValues(owner.variants.map(variant => fieldValue({ ...base,
      typeName: `${owner.name}.${variant.name}`, fields: [...base.fields, variant.name],
    }, variant.fields.find(field => field.name === name)!, schema)))
  }
  if (name === '$case' && sealedPayloadOwner(schema, base.typeName)) return { ...base, typeName: 'String', shape: 'scalar' }

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

function lookupIdentifier(path: NodePath<t.Identifier>, state: AnalysisState): TrackedBinding | null {
  const binding = path.scope.getBinding(path.node.name)
  return binding ? state.bindings.get(binding.identifier) ?? null : null
}

function declaration(schema: SelectionSchema, name: string): SchemaDeclaration | undefined {
  return schemaEntry(schema.models, name) ?? schemaEntry(schema.types, name) ?? schema[sealedCatalogue]?.get(name)
}

function unwrapExpression(path: NodePath<t.Node>): NodePath<t.Node> {
  let current = path
  while (true) {
    if (current.isAwaitExpression()) {
      current = asNodePath(current.get('argument')) ?? current
      continue
    }
    if (isTransparentExpression(current)) {
      current = asNodePath(current.get('expression')) ?? current
      continue
    }
    return current
  }
}

function createSelectionEdit(
  usage: CallUsage, schema: LuxoSchema, expression: (text: string, spreadArgs: boolean) => string, spreadHelper: string,
): TextEdit[] {
  if (hasManualSelection(usage.call)) return []
  if (usage.escaped) addSafeProjection(usage.tree, usage.api.returnType ?? '', schema)
  completeStructuredLeaves(usage.tree, usage.api.returnType ?? '', schema)
  if (usage.tree.children.size === 0) addMinimalProjection(usage.tree, usage.api.returnType ?? '', schema)
  const selection = usage.tree.toSelectString()
  if (!selection) return []
  warnDeepSelection(usage.api.name, usage.tree)

  const first = usage.call.arguments[0]
  const value = expression(selection, t.isSpreadElement(first))
  if (!first) return insertEmptyParams(usage.call, value)
  if (t.isObjectExpression(first)) return mergeObjectParams(first, value)
  if (t.isSpreadElement(first)) return wrapSpreadArguments(usage.call, first, value, spreadHelper)
  return wrapDynamicParams(first, value)
}

// Collect the whole argument list once; an initial empty spread must not shift
// a later params argument into the options or callback position.
function wrapSpreadArguments(call: t.CallExpression, first: t.SpreadElement, selection: string, helper: string): TextEdit[] {
  // Both positions come from parseSource, never from caller-supplied AST nodes.
  const start = first.start!
  const end = call.end! - 1
  return [
    { start, end: start, replacement: `...${helper}(${selection}, ` },
    { start: end, end, replacement: ')' },
  ]
}

function spreadArgumentHelper(name: string): string {
  return `function ${name}<S, P extends object, R extends unknown[]>(selection: S, ...args: [P?, ...R]) {
  const params = { $select: selection, ...(args[0] as P) };
  args[0] = params;
  return args as [typeof params, ...R];
}\n`
}

function addSafeProjection(
  root: FieldNode,
  typeName: string,
  schema: LuxoSchema,
  ancestors: ReadonlySet<string> = new Set(),
): void {
  const owner = schemaEntry(schema.sealeds, typeName)
  if (owner) {
    if (ancestors.has(typeName)) throw new Error(`Cannot infer complete recursive sealed selection for ${typeName}; provide an explicit selection`)
    const next = new Set(ancestors).add(typeName)
    for (const variant of owner.variants) addSafeProjection(root.addChild(variant.name), `${typeName}.${variant.name}`, schema, next)
    return
  }
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
  // Call discovery and nested-field checks prove this declaration exists;
  // retain the defensive guard if those callers later admit an unknown type.
  if (!type) return
  const nextAncestors = new Set(ancestors).add(typeName)

  for (const child of root.children.values()) {
    const field = type.fields.find(candidate => candidate.name === child.name)
    if (!field) continue
    const nestedType = field.typeName || field.type
    if (!declaration(schema, nestedType)) continue
    if (schemaEntry(schema.sealeds, typeName) && !child.escaped && child.children.size === 0) continue
    if (schemaEntry(schema.sealeds, nestedType) && !child.escaped && child.children.size === 0) {
      addMinimalProjection(child, nestedType, schema, nextAncestors)
      continue
    }
    if (child.escaped || child.children.size === 0) addSafeProjection(child, nestedType, schema, nextAncestors)
    // The explicit tree is finite. Ancestors bound implicit expansion inside
    // addSafeProjection, while deeper explicit escape flags still need visiting.
    completeStructuredLeaves(child, nestedType, schema, nextAncestors)
  }
}

function addMinimalProjection(
  root: FieldNode,
  typeName: string,
  schema: LuxoSchema,
  ancestors: ReadonlySet<string> = new Set(),
): boolean {
  const owner = schemaEntry(schema.sealeds, typeName)
  if (owner) {
    for (const variant of owner.variants) root.addChild(variant.name)
    return owner.variants.length !== 0
  }
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
  return [{ start: point, end: point, replacement: `{ $select: ${selection} }` }]
}

function mergeObjectParams(params: t.ObjectExpression, selection: string): TextEdit[] {
  if (params.start === null || params.start === undefined || params.end === null || params.end === undefined) return []
  if (params.properties.length === 0) {
    return [{ start: params.start + 1, end: params.start + 1, replacement: ` $select: ${selection} ` }]
  }
  return [{ start: params.start + 1, end: params.start + 1, replacement: ` $select: ${selection},` }]
}

function wrapDynamicParams(
  params: t.Expression | t.SpreadElement | t.JSXNamespacedName | t.ArgumentPlaceholder,
  selection: string,
): TextEdit[] {
  if (params.start === null || params.start === undefined || params.end === null || params.end === undefined) return []
  return [
    { start: params.start, end: params.start, replacement: `{ $select: ${selection}, ...(` },
    { start: params.end, end: params.end, replacement: ') }' },
  ]
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
  const ordered = [...edits].sort((a, b) => b.start - a.start)
  // One prepared dynamic call needs at most three edits; avoid an extra parts array.
  if (ordered.length <= 3) {
    let result = code
    for (const edit of ordered) {
      result = result.slice(0, edit.start) + edit.replacement + result.slice(edit.end)
    }
    return result
  }
  const parts: string[] = []
  let end = code.length
  for (const edit of ordered) {
    parts.push(code.slice(edit.end, end), edit.replacement)
    end = edit.start
  }
  parts.push(code.slice(0, end))
  return parts.reverse().join('')
}

class FieldNode {
  children = new Map<string, FieldNode>()
  declare escaped?: boolean

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

function addFieldChain(root: FieldNode, fields: string[]): FieldNode {
  let current = root
  for (const field of fields) current = current.addChild(field)
  return current
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

function asNodePath(value: NodePath<t.Node | null> | NodePath<t.Node | null>[] | null | undefined): NodePath<t.Node> | null {
  if (!value || Array.isArray(value) || value.type === null) return null
  return value
}
