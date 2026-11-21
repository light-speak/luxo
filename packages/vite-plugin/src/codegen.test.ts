import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { APISchema, LuxoSchema } from '@luxojs/client'
import { Decoder, ColumnarDecoder, Encoder, stringFromJSON, prepareSelection, FetchTransport, UnknownEnumValue, normalizeLuxoJSON } from '@luxojs/client'
import { stripTypeScriptTypes } from 'node:module'

const outputs = vi.hoisted(() => new Map<string, string>())

vi.mock('fs', () => ({
  existsSync: () => true,
  mkdirSync: vi.fn(),
  writeFileSync: (path: string, data: string) => outputs.set(path, data),
}))

import { generateTypes } from './codegen'

it('writes sealed scalar and ordinary payloads with equivalent static and legacy codecs', async () => {
  const fields = [
    { id: 1, name: 'number', type: 'Float' }, { id: 2, name: 'enabled', type: 'Boolean' },
    { id: 3, name: 'identifier', type: 'UUID' }, { id: 4, name: 'amount', type: 'Decimal' },
    { id: 5, name: 'bytes', type: 'Bytes' }, { id: 6, name: 'time', type: 'DateTime' },
    { id: 7, name: 'delay', type: 'Duration' }, { id: 8, name: 'document', type: 'JSON' },
    { id: 9, name: 'documents', type: 'JSON', isList: true },
    { id: 10, name: 'state', type: 'Enum', typeName: 'State' },
    { id: 11, name: 'point', type: 'Model', typeName: 'Point' },
    { id: 12, name: 'points', type: 'Model', typeName: 'Point', isList: true },
    { id: 13, name: 'legacy', type: 'Model', typeName: 'Legacy' },
  ]
  const schema: LuxoSchema = { models: { Point: { name: 'Point', fields: [{ id: 1, name: 'value', type: 'Int' }] } },
    types: { Legacy: { name: 'Legacy', fields: [{ id: 1, name: 'values', type: 'JSON', list: true }] } },
    enums: { State: { name: 'State', values: ['READY'] } },
    sealeds: { Outcome: { name: 'Outcome', variants: [{ id: 1, name: 'Payload', fields },
      { id: 2, name: 'PointOnly', fields: [{ id: 1, name: 'point', type: 'Model', typeName: 'Point' }] }] } },
    apis: { echo: { id: 1, name: 'echo', module: 'test', returnType: 'Outcome', params: [{ id: 1, name: 'value', type: 'Sealed', typeName: 'Outcome' }] } } }
  const runtime = await import('@luxojs/client')
  const make = async (staticRequests: boolean) => {
    const directory = staticRequests ? 'sealed-scalars-static' : 'sealed-scalars-legacy'
    await generateTypes(schema, directory, { staticRequests })
    const source = outputs.get(`${directory}/requests.ts`)!
    const js = stripTypeScriptTypes(source.replace(/^import .*\n/gm, '')).replace(/^export /gm, '')
    return new Function('runtime', `const {GeneratedRequest,Encoder,enumRawValue,stringFromJSON,booleanFromJSON,utf8Length,dateTimeFromJSON}=runtime; ${js};return $echoRequest`)(runtime) as
      import('@luxojs/client').RequestEncoder<{ value: unknown }> & (new(values: { value: unknown }) => import('@luxojs/client').GeneratedRequest)
  }
  const staticCodec = await make(true), Legacy = await make(false)
  const input = { $case: 'Payload', number: 1.5, enabled: true, identifier: '00000000-0000-0000-0000-000000000001', amount: '12.50',
    bytes: Uint8Array.of(1, 2), time: '2026-11-17T00:00:00Z', delay: 7n, document: { safe: [] }, documents: [null, { value: 1 }],
    state: 'READY', point: { value: 7n }, points: [{ value: 8n }], legacy: { values: [null, true] },
    toJSON() { throw Error('business case was projected') } }
  const values = { value: input }, legacy = new Legacy(values)
  const writer = new runtime.JSONRequestWriter(), oldWriter = new runtime.JSONRequestWriter()
  staticCodec.writeJSONParameters!(writer, values); legacy.writeJSONParameters!(oldWriter)
  expect(writer.toString()).toBe(oldWriter.toString())
  const json = JSON.parse('{' + writer.toString().slice(1) + '}') as { value: { Payload: Record<string, unknown> } }
  expect(json.value.Payload).toMatchObject({ bytes: 'AQI=', delay: 7, state: 'READY', point: { value: 7 }, points: [{ value: 8 }], legacy: { values: [null, true] } })
  const binary = new Encoder(), oldBinary = new Encoder()
  staticCodec.writeParameters(binary, values); legacy.writeParameters(oldBinary)
  expect(binary.bytes()).toEqual(oldBinary.bytes())
  const point = new Encoder()
  staticCodec.writeParameters(point, { value: { $case: 'PointOnly', point: { value: 7n } } })
  expect([...point.bytes()]).toEqual([1, 9, 2, 7, 0, 1, 0, 1, 14, 0, 0])
})

it('generates sealed stream, DTO-list and input-only legacy shapes with strict public rejection', async () => {
  const { readFileSync } = await vi.importActual<typeof import('node:fs')>('node:fs')
  const schema = JSON.parse(readFileSync(new URL('fixtures/sealed-generation-shapes.json', import.meta.url), 'utf8')) as LuxoSchema
  await generateTypes(schema, 'sealed-shapes', { staticRequests: false })
  const types = outputs.get('sealed-shapes/types.ts')!, client = outputs.get('sealed-shapes/client.ts')!, requests = outputs.get('sealed-shapes/requests.ts')!
  expect(types).toContain('export type InputOnly =')
  expect(types).not.toContain('decodeInputOnly(')
  expect(types).toContain('decodeSealedListOutcome$value(new Decoder(_choices[i]!), 1 + 1, true)')
  expect(client).toContain('subscribeWatch<Select extends string | PreparedSelection | undefined = undefined>')
  expect(client).toContain('onData: (data: Outcome<Select extends string | PreparedSelection ? true : false> | null) => void')
  expect(client).toContain("this.transport.subscribe('watch', new $watchRequest(params)")
  expect(requests).toContain('writeJSONParameters(writer: JSONRequestWriter)')
  const js = stripTypeScriptTypes(types.replace(/^import .*\n/gm, '')).replace(/^export /gm, '')
  const runtime = await import('@luxojs/client')
  const decode = new Function('runtime', `const {Decoder, ColumnarDecoder, int64FromJSON, stringFromJSON}=runtime; ${js}; return decodeJSONBatch`)(runtime) as (value: unknown) => unknown
  expect(decode({ choices: [{ Ready: {} }] })).toEqual({ choices: [{ $case: 'Ready' }] })
  expect(decode({ choices: null })).toEqual({ choices: null })
  const scripts = ['types', 'schema', 'requests', 'client'].map(name => stripTypeScriptTypes(outputs.get(`sealed-shapes/${name}.ts`)!))
  const imported = new Set(scripts.flatMap(source => [...source.matchAll(/^import \{([^}]+)\} from ['"]@luxojs\/client['"]/gm)].flatMap(match => match[1].split(',').map(name => name.trim()))))
  const body = scripts.map(source => source.replace(/^import .*\n/gm, '').replace(/^export /gm, '')).join('\n')
  const Client = new Function('runtime', `const {${[...imported].join(',')}}=runtime; ${body}; return LuxoClient`)(runtime) as
    new (transport: object) => { subscribeWatch(params: object, callback: (value: unknown) => void): Promise<() => void> }
  let cancelled = false
  const transport = { setSchema() {},
    async subscribe(_api: string, request: import('@luxojs/client').GeneratedRequest, read: (value: unknown) => unknown, onData: (value: unknown) => void) {
      expect(request.toJSONParameters()).toEqual({})
      for (const raw of [null, { Ready: {} }, Uint8Array.of(0), Uint8Array.of(1, 1, 2, 0, 0)]) onData(read(raw))
      return () => { cancelled = true }
    } }
  const seen: unknown[] = []
  const cancel = await new Client(transport).subscribeWatch({}, value => { seen.push(value) })
  expect(seen).toEqual([null, { $case: 'Ready' }, null, { $case: 'Ready' }])
  cancel(); expect(cancelled).toBe(true)
  const invalid: LuxoSchema[] = [
    { ...schema, models: { Outcome: { name: 'Outcome', fields: [] } } },
    { ...schema, sealeds: { Outcome: { name: 'Outcome', variants: [{ id: 1, name: 'Ready', fields: [] }, { id: 2, name: 'Ready', fields: [] }] } } },
    { models: {}, apis: { read: { id: 1, name: 'read', module: 'test', returnType: 'Missing' } } },
    { models: {}, types: { Batch: { name: 'Batch', fields: [{ id: 1, name: 'value', type: 'Sealed', typeName: 'Missing' }] } }, apis: {} },
  ]
  for (const bad of invalid) {
    const before = new Map(outputs)
    await expect(generateTypes(bad, 'sealed-shapes')).rejects.toThrow()
    expect(outputs).toEqual(before)
  }
})

it('covers sealed public generation cold scalar and rejection controls', async () => {
  const schema: LuxoSchema = { models: { InputRecord: { name: 'InputRecord', fields: [{ id: 1, name: 'choice', type: 'Sealed', typeName: 'Outcome' }] } },
    types: { Batch: { name: 'Batch', fields: [{ id: 1, name: 'value', type: 'Int' }] } },
    sealeds: { Outcome: { name: 'Outcome', variants: [{ id: 1, name: 'Ready', fields: [] }] }, Unused: { name: 'Unused', variants: [] } },
    enums: { State: { name: 'State', values: ['READY'] } },
    apis: { write: { id: 1, name: 'write', module: 'test', returnType: 'Int', params: [{ id: 1, name: 'record', type: 'Model', typeName: 'InputRecord' }] },
      sealedWrite: { id: 5, name: 'sealedWrite', module: 'test', returnType: 'Int', params: [{ id: 1, name: 'choice', type: 'Sealed', typeName: 'Outcome' }] },
      state: { id: 2, name: 'state', module: 'test', returnType: 'State' },
      ticks: { id: 3, name: 'ticks', module: 'test', stream: true, returnType: 'Int' },
      readPage: { id: 4, name: 'readPage', module: 'test', returnType: 'Batch', paginated: true, params: [{ id: 1, name: 'page', type: 'Int' }, { id: 2, name: 'pageSize', type: 'Int' }, { id: 3, name: 'choice', type: 'Sealed', typeName: 'Outcome' }] } } }
  await generateTypes(schema, 'sealed-cold-generation')
  const runtime = await import('@luxojs/client')
  const requests = outputs.get('sealed-cold-generation/requests.ts')!
  const js = stripTypeScriptTypes(requests.replace(/^import .*\n/gm, '')).replace(/^export /gm, '')
  const request = new Function('runtime', `const {GeneratedRequest,Encoder,enumRawValue,stringFromJSON,booleanFromJSON,utf8Length,dateTimeFromJSON}=runtime;${js};return $readPageRequest`)(runtime) as import('@luxojs/client').RequestEncoder<{choice:unknown}>
  const writer = new runtime.JSONRequestWriter()
  request.writeJSONParameters!(writer, {choice:{$case:'Ready'}})
  expect(writer.toString()).toBe(',"choice":{"Ready":{}}')
  expect(outputs.get('sealed-cold-generation/client.ts')).toContain('subscribeTicks(onData: (data: bigint) => void')
  expect(outputs.get('sealed-cold-generation/schema.ts')).toContain("'write': { id: 1, response: { type: 'Int' }, types: LUXO_SELECTION_TYPES")
  const invalid: LuxoSchema[] = [
    JSON.parse('{"models":{"Broken":{"name":7,"fields":[]}},"apis":{}}') as LuxoSchema,
    { models: { Broken: { name: 'Broken', fields: [{ id: 1, name: 'field', type: 'Missing' }] } }, apis: {} },
    { models: { Broken: { name: 'Broken', fields: [{ id: 1, name: 'field', type: 'Sealed' }] } }, apis: {} },
    { models: {}, apis: { read: { id: 1, name: 'read', module: 'test', returnType: 'Int', paginated: true, params: [{ id: 1, name: 'page', type: 'Int' }, { id: 2, name: 'pageSize', type: 'Int' }], query: { filters: [{ field: 'bad-field', type: 'Int', operators: ['eq'] }] } } } },
  ]
  for (const value of invalid) {
    const before = new Map(outputs)
    await expect(generateTypes(value, 'sealed-cold-generation')).rejects.toThrow()
    expect(outputs).toEqual(before)
  }
})

it('generates closed sealed cases and canonical static payload codecs', async () => {
  const { readFileSync } = await vi.importActual<typeof import('node:fs')>('node:fs')
  const fixture = JSON.parse(readFileSync(new URL('../../../pkg/lux/codec/testdata/sealed-v1.json', import.meta.url), 'utf8')) as { schema: LuxoSchema }
  await generateTypes(fixture.schema, 'sealed-canonical')
  const source = outputs.get('sealed-canonical/types.ts') ?? ''
  expect(source).toContain("readonly $case: 'Success'")
  expect(source).toContain('export type Outcome<')
  expect(source).toContain('export type EmptyOutcome<Selection extends boolean = false> = never')
  const js = stripTypeScriptTypes(source.replace(/^import .*\n/gm, '')).replace(/^export /gm, '')
  const runtime = await import('@luxojs/client')
  const load = new Function('runtime', `const { Decoder, ColumnarDecoder, int64FromJSON, stringFromJSON } = runtime; ${js}\nreturn { binary:decodeOutcome, json:decodeJSONOutcome, list:decodeSealedListOutcome, empty:decodeSealedListEmptyOutcome }`) as
    (runtime: typeof import('@luxojs/client')) => {
      binary(value: Decoder): unknown
      json(value: Record<string, unknown>): unknown
      list(value: Uint8Array): unknown[]
      empty(value: Uint8Array): unknown[]
    }
  const codecs = load(runtime)
  expect(codecs.binary(new Decoder(Uint8Array.of(1, 4, 0, 1, 14, 0)))).toEqual({ $case:'Success', value:7n })
  expect(codecs.binary(new Decoder(Uint8Array.of(1, 2, 0, 0)))).toEqual({ $case:'Success', value:undefined })
  expect(codecs.binary(new Decoder(Uint8Array.of(3, 2, 0, 0)))).toEqual({ $case:'Pending' })
  expect(codecs.json({ Pending:{} })).toEqual({ $case:'Pending' })
  expect(codecs.empty(Uint8Array.of(0))).toEqual([])
  const requests = outputs.get('sealed-canonical/requests.ts') ?? ''
  const requestJS = stripTypeScriptTypes(requests.replace(/^import .*\n/gm, '')).replace(/^export /gm, '')
  const request = new Function('runtime', `const { GeneratedRequest, Encoder, enumRawValue, stringFromJSON, booleanFromJSON, utf8Length, dateTimeFromJSON } = runtime; ${requestJS}\nreturn $echoRequest`)(runtime) as import('@luxojs/client').RequestEncoder<{value:unknown}>
  const input = { $case:'Success', value:7n, toJSON() { throw Error('input converted') } }
  const encoded = new Encoder()
  request.writeParameters(encoded, { value:input })
  expect(Array.from(encoded.bytes())).toEqual([1,6,1,4,0,1,14,0])
  const writer = new runtime.JSONRequestWriter()
  request.writeJSONParameters!(writer, { value:input })
  expect(writer.toString()).toBe(',"value":{"Success":{"value":7}}')
  const omitted = new runtime.JSONRequestWriter()
  request.writeJSONParameters!(omitted, { value:{$case:'Failure'} })
  expect(omitted.toString()).toBe(',"value":{"Failure":{}}')
  const explicit = new runtime.JSONRequestWriter()
  request.writeJSONParameters!(explicit, { value:{$case:'Nested',child:null,history:[]} })
  expect(explicit.toString()).toBe(',"value":{"Nested":{"child":null,"history":[]}}')
  expect(() => request.writeParameters(new Encoder(),{value:{$case:'Success'}})).toThrow(/required/)
  expect(() => request.writeJSONParameters!(new runtime.JSONRequestWriter(),{value:{$case:'Success'}})).toThrow(/required/)

  const nestedInput = (count: number): unknown => {
    let value: unknown = { $case: 'Pending' }
    for (let i = 1; i < count; i++) value = { $case: 'Nested', child: value }
    return value
  }
  const nestedOutput = (count: number): Record<string, unknown> => {
    let value: Record<string, unknown> = { Pending: {} }
    for (let i = 1; i < count; i++) value = { Nested: { child: value } }
    return value
  }
  const nestedWire = (count: number): Uint8Array => {
    const value = new Encoder()
    value.writeVarint(count === 1 ? 3 : 4)
    const payload = new Encoder()
    payload.writeVarint(0)
    if (count !== 1) { payload.writeVarint(1); payload.writeBool(true); payload.writeBytes(nestedWire(count - 1)) }
    payload.writeEnd(); value.writeBytes(payload.bytes())
    return value.bytes()
  }
  expect(() => codecs.json(nestedOutput(32))).not.toThrow()
  expect(() => codecs.binary(new Decoder(nestedWire(32)))).not.toThrow()
  expect(() => request.writeParameters(new Encoder(), { value: nestedInput(32) })).not.toThrow()
  expect(() => request.writeJSONParameters!(new runtime.JSONRequestWriter(), { value: nestedInput(32) })).not.toThrow()
  expect(() => codecs.json(nestedOutput(33))).toThrow(/64/)
  expect(() => codecs.binary(new Decoder(nestedWire(33)))).toThrow(/64/)
  expect(() => request.writeParameters(new Encoder(), { value: nestedInput(33) })).toThrow(/64/)
  expect(() => request.writeJSONParameters!(new runtime.JSONRequestWriter(), { value: nestedInput(33) })).toThrow(/64/)
  const oversizedList = new Encoder(); oversizedList.writeVarint(1_000_001)
  expect(() => codecs.list(oversizedList.bytes())).toThrow(/array count/)
  expect(() => codecs.binary(new Decoder(Uint8Array.of(1,4,0,1)))).toThrow()

  for (const value of [{}, { Future:{} }, { Success:{}, Pending:{} }, { Pending:{ extra:1 } }]) {
    expect(() => codecs.json(value)).toThrow()
  }
  for (const raw of [[0,2,0,0], [99,2,0,0], [3,2,0,0,0]]) {
    expect(() => codecs.binary(new Decoder(Uint8Array.from(raw)))).toThrow()
  }
})

it.each(['field','declaration'])('rejects raw sealed private namespace collisions before writes (%s)', async target => {
  const schema:LuxoSchema = {models:{},sealeds:{Outcome:{name:'Outcome',variants:[
    {id:1,name:'Success',fields:[{id:1,name:target==='field'?'$case':'value',type:'Int'}]},
  ]}},apis:{read:{id:1,name:'read',module:'unit',returnType:'Outcome'}}}
  if(target==='declaration') schema.types = {'Outcome$Success$1':{name:'Outcome$Success$1',usage:'output',fields:[]}}
  for(const file of ['types.ts','schema.ts','requests.ts','client.ts','queries.ts']) outputs.set(`sealed-names/${file}`,'existing')
  await expect(generateTypes(schema,'sealed-names')).rejects.toThrow(/identifier/)
  for(const file of ['types.ts','schema.ts','requests.ts','client.ts','queries.ts']) expect(outputs.get(`sealed-names/${file}`)).toBe('existing')
})

it('emits explicit response shape for models, lists, pages, scalars and empty declarations', async () => {
  const schema: LuxoSchema = { models: { Node: { name: 'Node', fields: [] } }, apis: {
    single: { id: 1, name: 'single', module: 'test', returnType: 'Node' },
    list: { id: 2, name: 'list', module: 'test', returnType: 'JSON', returnList: true },
    page: { id: 3, name: 'page', module: 'test', returnType: 'Node', paginated: true, params: [
      { id: 1, name: 'page', type: 'Int' }, { id: 2, name: 'pageSize', type: 'Int' },
    ] },
    scalar: { id: 4, name: 'scalar', module: 'test', returnType: 'String' },
  } }
  await generateTypes(schema, 'response-shape')
  const source = outputs.get('response-shape/schema.ts') ?? ''
  const js = stripTypeScriptTypes(source.replace(/^import .*\n/gm, '')).replace(/^export /gm, '')
  const generated = new Function(`${js}\nreturn LUXO_SCHEMA`)() as Record<string, APISchema>
  expect(generated.single.response).toEqual({ type: 'Model', typeName: 'Node' })
  expect(generated.single.types).toEqual({ Node: {} })
  expect(generated.list.response).toEqual({ type: 'JSON', isList: true })
  expect(generated.page.response).toEqual({ type: 'Model', typeName: 'Node', paginated: true })
  expect(generated.scalar.response).toEqual({ type: 'String' })
})

it.each(['model', 'type', 'enum', 'api', 'field', 'param'])('rejects empty schema names for %s before writes', async target => {
  const field = { id: 1, name: 'value', type: 'Int' }
  const schema: LuxoSchema = { models: {}, apis: {} }
  if (target === 'model') schema.models[''] = { name: '', fields: [field] }
  if (target === 'type') schema.types = { '': { name: '', fields: [field] } }
  if (target === 'enum') schema.enums = { '': { name: '', values: ['READY'] } }
  if (target === 'api') schema.apis[''] = { id: 1, name: '', module: 'test', returnType: 'Int' }
  if (target === 'field') {
    schema.models.Payload = { name: 'Payload', fields: [{ ...field, name: '' }] }
    schema.apis.read = { id: 1, name: 'read', module: 'test', returnType: 'Payload' }
  }
  if (target === 'param') schema.apis.read = { id: 1, name: 'read', module: 'test', returnType: 'Int', params: [{ ...field, name: '' }] }
  const files = ['types.ts', 'schema.ts', 'requests.ts', 'client.ts', 'queries.ts']
  for (const file of files) outputs.set(`empty-names/${file}`, `existing ${file}`)
  await expect(generateTypes(schema, 'empty-names')).rejects.toThrow(/name.*empty/)
  for (const file of files) expect(outputs.get(`empty-names/${file}`)).toBe(`existing ${file}`)
})

it.each(['models', 'types', 'params', 'unused'].flatMap(target =>
  ['', 'Missing', 'Int64'].flatMap(type => [false, true].map(list => ({ target, type, list }))),
))('rejects undeclared member types target=$target type=$type list=$list before writes', async ({ target, type, list }) => {
  const field = { id: 1, name: 'value', type, isList: list }
  const schema: LuxoSchema = { models: {}, types: {}, apis: {} }
  if (target === 'params') schema.apis.read = { id: 1, name: 'read', module: 'test', returnType: 'Int', params: [field] }
  else {
    const declaration = { name: 'Payload', fields: [field], ...(target === 'unused' ? { usage: 'unused' as const } : {}) }
    if (target === 'models') schema.models.Payload = declaration
    else schema.types = { Payload: declaration }
    schema.apis.read = { id: 1, name: 'read', module: 'test', returnType: target === 'unused' ? 'Int' : 'Payload' }
  }
  const files = ['types.ts', 'schema.ts', 'requests.ts', 'client.ts', 'queries.ts']
  for (const file of files) outputs.set(`unknown-types/${file}`, `existing ${file}`)
  await expect(generateTypes(schema, 'unknown-types')).rejects.toThrow(/type/)
  for (const file of files) expect(outputs.get(`unknown-types/${file}`)).toBe(`existing ${file}`)
})

it.each([undefined, null, false, true, 0, 7, [], {}])('rejects non-string enum values value=%s before writes', async value => {
  const values = ['READY']
  Object.assign(values, { 0: value })
  const schema: LuxoSchema = { models: {}, enums: { State: { name: 'State', values } },
    apis: { read: { id: 1, name: 'read', module: 'test', returnType: 'State' } } }
  const files = ['types.ts', 'schema.ts', 'requests.ts', 'client.ts', 'queries.ts']
  for (const file of files) outputs.set(`enum-values/${file}`, `existing ${file}`)
  await expect(generateTypes(schema, 'enum-values')).rejects.toThrow(/enum State.*invalid/)
  for (const file of files) expect(outputs.get(`enum-values/${file}`)).toBe(`existing ${file}`)
})

it.each([undefined, null, false, 1, {}, 'READY'])('rejects non-array enum values value=%s', async values => {
  const declaration = { name: 'State', values: ['READY'] }
  Object.assign(declaration, { values })
  const schema: LuxoSchema = { models: {}, enums: { State: declaration },
    apis: { read: { id: 1, name: 'read', module: 'test', returnType: 'State' } } }
  await expect(generateTypes(schema, 'enum-shape')).rejects.toThrow(/enum State.*invalid/)
})

it.each(['model', 'type', 'param'].flatMap(target =>
  [undefined, null, false, true, 0, 7, [], {}].map(value => ({ target, value }))))(
  'rejects non-string member names target=$target value=$value before writes', async ({ target, value }) => {
    const field = { id: 1, name: 'value', type: 'String' }
    const schema: LuxoSchema = {
      models: { Payload: { name: 'Payload', fields: [{ ...field }] } },
      apis: { read: { id: 1, name: 'read', module: 'test', returnType: 'Payload' } },
    }
    Object.assign(field, { name: value })
    if (target === 'model') schema.models.Payload.fields = [field]
    else if (target === 'type') schema.types = { Detail: { name: 'Detail', usage: 'output', fields: [field] } }
    else schema.apis.read.params = [field]
    const files = ['types.ts', 'schema.ts', 'requests.ts', 'client.ts', 'queries.ts']
    for (const file of files) outputs.set(`member-names/${file}`, `existing ${file}`)
    await expect(generateTypes(schema, 'member-names')).rejects.toThrow(/name.*string/)
    for (const file of files) expect(outputs.get(`member-names/${file}`)).toBe(`existing ${file}`)
  },
)

const schemaFlagCases = [
  ...['internal', 'returnList', 'returnNullable', 'paginated', 'stream'].map(flag => ({ target: 'apis', flag })),
  ...['models', 'types'].flatMap(target => ['nullable', 'isList', 'list', 'relation'].map(flag => ({ target, flag }))),
  ...['nullable', 'isList', 'hasDefault'].map(flag => ({ target: 'params', flag })),
]

it.each(['model', 'type', 'param'].flatMap(target => [
  ...[undefined, null, false, true, 0, 7, [], {}].map(value => ({ target, member: 'type', value })),
  ...[false, true, 0, 7, [], {}].map(value => ({ target, member: 'typeName', value })),
]))('rejects non-string type metadata target=$target member=$member value=$value before writes', async ({ target, member, value }) => {
  const field = { id: 1, name: 'value', type: 'String' }
  const schema: LuxoSchema = {
    models: { Payload: { name: 'Payload', fields: [{ ...field }] } },
    apis: { read: { id: 1, name: 'read', module: 'test', returnType: 'Payload' } },
  }
  Object.assign(field, { [member]: value })
  if (target === 'model') schema.models.Payload.fields = [field]
  else if (target === 'type') schema.types = { Detail: { name: 'Detail', usage: 'output', fields: [field] } }
  else schema.apis.read.params = [field]
  const files = ['types.ts', 'schema.ts', 'requests.ts', 'client.ts', 'queries.ts']
  for (const file of files) outputs.set(`member-types/${file}`, `existing ${file}`)
  await expect(generateTypes(schema, 'member-types')).rejects.toThrow(new RegExp(`${member}.*string`))
  for (const file of files) expect(outputs.get(`member-types/${file}`)).toBe(`existing ${file}`)
})

it.each(schemaFlagCases.flatMap(entry => ['true', 'false', 0, 1, [], {}].map(value => ({ ...entry, value }))))(
  'rejects non-boolean metadata target=$target flag=$flag value=$value before writes', async ({ target, flag, value }) => {
    const field = { id: 1, name: 'value', type: 'String' }
    const schema: LuxoSchema = {
      models: { Payload: { name: 'Payload', fields: [{ ...field }] } },
      apis: { read: { id: 1, name: 'read', module: 'test', returnType: 'Payload' } },
    }
    if (target === 'apis') {
      Object.assign(schema.apis.read, { [flag]: value })
      if (flag === 'paginated') schema.apis.read.params = [
        { id: 1, name: 'page', type: 'Int' }, { id: 2, name: 'pageSize', type: 'Int' },
      ]
    }
    else {
      Object.assign(field, { [flag]: value })
      if (target === 'params') schema.apis.read.params = [field]
      else if (target === 'models') schema.models.Payload = { name: 'Payload', fields: [field] }
      else schema.types = { Detail: { name: 'Detail', fields: [field] } }
    }
    const files = ['types.ts', 'schema.ts', 'requests.ts', 'client.ts', 'queries.ts']
    for (const file of files) outputs.set(`flags/${file}`, `existing ${file}`)
    await expect(generateTypes(schema, 'flags')).rejects.toThrow(/boolean/)
    for (const file of files) expect(outputs.get(`flags/${file}`)).toBe(`existing ${file}`)
  },
)

it.each([undefined, null, ''])('keeps absent optional type names value=%s', async value => {
  const field = { id: 1, name: 'value', type: 'String' }
  Object.assign(field, { typeName: value })
  const schema: LuxoSchema = {
    models: { Payload: { name: 'Payload', fields: [field] } },
    types: { Input: { name: 'Input', usage: 'input', fields: [field] } },
    apis: { read: { id: 1, name: 'read', module: 'test', returnType: 'Payload', params: [field] } },
  }
  await expect(generateTypes(schema, 'optional-types')).resolves.toBeUndefined()
  expect(outputs.get('optional-types/types.ts')).toContain('value: string')
})

it('preserves case-distinct known enum members in generated decoders', async () => {
  await generateTypes({ models: {}, apis: {}, enums: {
    State: { name: 'State', values: ['Open', 'OPEN'] },
  } }, 'enum-case')
  const source = outputs.get('enum-case/types.ts') ?? ''
  const js = stripTypeScriptTypes(source.replace(/^import .*\n/gm, '')).replace(/^export /gm, '')
  const decode = new Function('$UnknownEnumValue', `${js}\nreturn decodeEnum$State`)(UnknownEnumValue) as (raw: string) => unknown
  expect(decode('Open')).toBe('Open')
  expect(decode('OPEN')).toBe('OPEN')
  const unknown = decode('open')
  expect(unknown).toBeInstanceOf(UnknownEnumValue)
})

it.each(['model-type', 'model-enum', 'type-enum'])('rejects cross-namespace type collisions: %s', async pair => {
  const declaration = { name: 'Payload', fields: [{ id: 1, name: 'id', type: 'Int' }] }
  const schema: LuxoSchema = { models: {}, apis: {} }
  if (pair.includes('model')) schema.models.Payload = declaration
  if (pair.includes('type')) schema.types = { Payload: declaration }
  if (pair.includes('enum')) schema.enums = { Payload: { name: 'Payload', values: ['OPEN'] } }
  outputs.set('collision/types.ts', 'existing client')
  await expect(generateTypes(schema, 'collision')).rejects.toThrow(/duplicate type/)
  expect(outputs.get('collision/types.ts')).toBe('existing client')
})

it.each(['models', 'types', 'params'].flatMap(target =>
  ['Int', 'String'].map(type => ({ target, type })),
))('rejects duplicate $target names with distinct IDs ($type)', async ({ target, type }) => {
  const fields = [{ id: 1, name: 'value', type: 'Int' }, { id: 2, name: 'value', type }]
  const schema: LuxoSchema = { models: {}, apis: {} }
  if (target === 'params') schema.apis.read = { id: 1, name: 'read', module: 'test', returnType: 'Int', params: fields }
  else if (target === 'models') schema.models.Payload = { name: 'Payload', fields }
  else schema.types = { Payload: { name: 'Payload', fields } }
  outputs.set('duplicates/types.ts', 'existing client')
  await expect(generateTypes(schema, 'duplicates')).rejects.toThrow('duplicate name value')
  expect(outputs.get('duplicates/types.ts')).toBe('existing client')
})

it.each(['models', 'types', 'apis'])('rejects mismatched %s declaration names before writes', async target => {
  const schema: LuxoSchema = { models: {}, apis: {} }
  if (target === 'apis') schema.apis.read = { id: 1, name: 'other', module: 'test', returnType: 'Int' }
  else {
    const declaration = { name: 'Other', fields: [{ id: 1, name: 'id', type: 'Int' }] }
    if (target === 'models') schema.models.Payload = declaration
    else schema.types = { Payload: declaration }
    schema.apis.read = { id: 1, name: 'read', module: 'test', returnType: 'Payload' }
  }
  outputs.set('names/types.ts', 'existing client')
  await expect(generateTypes(schema, 'names')).rejects.toThrow(/name.*match/)
  expect(outputs.get('names/types.ts')).toBe('existing client')
})

it.each([false, true])('rejects omitted API return before writes: list=%s', async list => {
  outputs.set('omitted/types.ts', 'existing client')
  await expect(generateTypes({ models: {}, apis: {
    read: { id: 1, name: 'read', module: 'test', returnList: list },
  } }, 'omitted')).rejects.toThrow(/return type/)
  expect(outputs.get('omitted/types.ts')).toBe('existing client')
})

it.each(['Missing', '', 'constructor', 'Enum', 'Void'].flatMap(type =>
  [false, true].map(list => ({ type, list })),
))('rejects undeclared API return before writes: $type/$list', async ({ type, list }) => {
  outputs.set('return/types.ts', 'existing client')
  await expect(generateTypes({ models: {}, apis: {
    read: { id: 1, name: 'read', module: 'test', returnType: type, returnList: list },
  } }, 'return')).rejects.toThrow(/return type/)
  expect(outputs.get('return/types.ts')).toBe('existing client')
})

it.each(['Model', 'Enum'].flatMap(kind => ['models', 'types', 'params', 'unused'].flatMap(target =>
  [undefined, '', 'Missing', 'constructor'].flatMap(name =>
    [false, true].map(list => ({ kind, target, name, list })),
  ),
)))('rejects missing $kind references before writes: $target/$name/$list', async ({ kind, target, name, list }) => {
  const field = { id: 1, name: 'child', type: kind, typeName: name, isList: list, nullable: list }
  const schema: LuxoSchema = { models: {}, types: {}, apis: {} }
  if (target === 'params') {
    schema.apis.read = { id: 1, name: 'read', module: 'test', returnType: 'Int', params: [field] }
  } else {
    const model = { name: 'Payload', fields: [field], ...(target === 'unused' ? { usage: 'unused' as const } : {}) }
    if (target === 'models') schema.models.Payload = model
    else schema.types = { Payload: model }
    if (target !== 'unused') schema.apis.read = { id: 1, name: 'read', module: 'test', returnType: 'Payload' }
  }
  const files = ['types.ts', 'schema.ts', 'requests.ts', 'client.ts', 'queries.ts']
  for (const file of files) outputs.set(`preserved/${file}`, `previous ${file}`)
  await expect(generateTypes(schema, 'preserved')).rejects.toThrow(/missing .*type/)
  for (const file of files) expect(outputs.get(`preserved/${file}`)).toBe(`previous ${file}`)
})

it('generates empty enums as unknown-only values and preserves raw text', async () => {
  await generateTypes({ models: {}, apis: {}, enums: { Empty: { name: 'Empty', values: [] } } }, 'empty-enum')
  const source = outputs.get('empty-enum/types.ts') ?? ''
  expect(source).toContain("export type Empty = $UnknownEnumValue<'Empty'>")
  expect(source).toContain('export const EmptyValues = [] as const')
  const js = stripTypeScriptTypes(source.replace(/^import .*\n/gm, '')).replace(/^export /gm, '')
  const decode = new Function('$UnknownEnumValue', `${js}; return decodeEnum$Empty`)(UnknownEnumValue) as (raw: string) => UnknownEnumValue<'Empty'>
  for (const raw of ['', 'FUTURE', '中文', 'é', 'e\u0301']) {
    const value = decode(raw)
    expect(value).toBeInstanceOf(UnknownEnumValue)
    expect(value.rawValue).toBe(raw)
  }
})

it('accepts internal service route names while preserving public identifier validation', async () => {
  const name = 'svc:batchLoad:QueryProbe'
  const schema: LuxoSchema = { models: {}, apis: {
    [name]: { id: 1, name, module: 'svc', returnType: 'Int', internal: true },
  } }
  await generateTypes(schema, 'internal-route')
  expect(schema.apis[name].name).toBe(name)
  expect(outputs.get('internal-route/client.ts')).not.toContain(name)
  schema.apis[name].internal = false
  await expect(generateTypes(schema, 'public-route')).rejects.toThrow(/invalid declaration identifier/)
})

it.each([
  { name: 'Other', values: ['OPEN'] },
  { name: 'Status', values: ['OPEN', 'OPEN'] },
])('rejects invalid enum declaration before writes: $name/$values', async declaration => {
  outputs.set('enum/types.ts', 'existing client')
  await expect(generateTypes({ models: {}, apis: {}, enums: { Status: declaration } }, 'enum'))
    .rejects.toThrow(/enum .*declaration/)
  expect(outputs.get('enum/types.ts')).toBe('existing client')
})

it('preserves all existing artifacts when input declaration generation fails', async () => {
  const files = ['types.ts', 'schema.ts', 'requests.ts', 'client.ts', 'queries.ts']
  for (const file of files) outputs.set(`existing/${file}`, `previous ${file}`)
  await expect(generateTypes({ models: {}, apis: {
    save: { id: 1, name: 'save', module: 'test', returnType: 'Int',
      params: [{ id: 1, name: 'value', type: 'Model', typeName: 'Missing' }] },
  } }, 'existing')).rejects.toThrow('missing input type Missing')
  for (const file of files) expect(outputs.get(`existing/${file}`)).toBe(`previous ${file}`)
})

it.each(['api', 'type', 'field'])('retains __proto__ as an own Schema %s key through selection encoding', async target => {
  const api = target === 'api' ? '__proto__' : 'read'
  const type = target === 'type' ? '__proto__' : 'Payload'
  const field = target === 'field' ? '__proto__' : 'name'
  await generateTypes({
    models: {
      [type]: { name: type, fields: [{ id: 7, name: field, type: 'String' }] },
      ...(target === 'type' ? { Envelope: { name: 'Envelope', fields: [
        { id: 3, name: 'child', type: 'Model', typeName: type },
      ] } } : {}),
    },
    apis: { [api]: { id: 1, name: api, module: 'test', returnType: target === 'type' ? 'Envelope' : type } },
  }, 'prototype-schema')
  const source = outputs.get('prototype-schema/schema.ts') ?? ''
  const js = stripTypeScriptTypes(source.replace(/^import .*\n/gm, '')).replace(/^export /gm, '')
  const generated = new Function(`${js}\nreturn LUXO_SCHEMA`)() as Record<string, APISchema>
  expect(Object.hasOwn(generated, api)).toBe(true)
  const meta = generated[api]
  expect(Object.hasOwn(meta.fields ?? {}, target === 'type' ? 'child' : field)).toBe(true)
  expect(Object.hasOwn(meta.types ?? {}, type)).toBe(true)
  const codec = { apiID: 1, initialCapacity: () => 64, writeParameters: () => {} }
  const selection = target === 'type' ? `child{${field}}` : field
  const expected = target === 'type' ? [1, 6, 1, 4, 3, 2, 1, 64, 0] : [1, 2, 1, 64, 0]
  const fetch = vi.spyOn(globalThis, 'fetch').mockImplementation(async (_input, init) => {
    expect(init?.body).toEqual(new Uint8Array(expected))
    return new Response(new Uint8Array([84]))
  })
  try {
    const transport = new FetchTransport('https://example.invalid', { mode: 'binary' })
    transport.setSchema(generated)
    for (const $select of [selection, prepareSelection(selection)]) {
      expect(await transport.callGenerated(api, codec, { $select })).toEqual(new Uint8Array([84]))
    }
    expect(fetch).toHaveBeenCalledTimes(2)
  } finally { fetch.mockRestore() }
})

function nestedModelValue(layers: number, list: boolean, json?: unknown): Record<string, unknown> {
  let value: Record<string, unknown> = { next: list ? [] : null }
  if (json !== undefined) value.json = json
  for (let i = 1; i < layers; i++) value = { next: list ? [value] : value }
  return value
}

it.each(['model', 'type'])('retains direct codecs when all %s declarations have no structured fields', async target => {
  const payload = { name: 'Payload', usage: 'inputOutput' as const, fields: [
    { id: 1, name: 'value', type: 'JSON' }, { id: 2, name: 'values', type: 'JSON', isList: true },
  ] }
  await generateTypes({ models: target === 'model' ? { Payload: payload } : {},
    types: target === 'type' ? { Payload: payload } : {}, apis: {
      echo: { id: 1, name: 'echo', module: 'test', returnType: 'Payload',
        params: [{ id: 1, name: 'input', type: 'Model', typeName: 'Payload' }] },
    } }, 'flat-depth')
  // JSON container depth is independent of the single Schema object layer.
  const source = outputs.get('flat-depth/types.ts') ?? ''
  expect(source).not.toContain('model nesting exceeds 64 levels')
  expect(outputs.get('flat-depth/requests.ts')).not.toContain('model nesting exceeds 64 levels')
  const js = stripTypeScriptTypes(source.replace(/^import .*\n/gm, '')).replace(/^export /gm, '')
  const decode = new Function('normalizeLuxoJSON', `${js}\nreturn decodeJSONPayload`)(normalizeLuxoJSON) as
    (data: Record<string, unknown>) => Record<string, unknown>
  let json: unknown = {}
  for (let i = 1; i < 128; i++) json = { child: json }
  expect(decode({ value: json, values: [json] })).toEqual({ value: json, values: [json] })
  expect(() => decode({ value: { child: json } })).toThrow(/JSON nesting exceeds 128 levels/)
})

function nestedModelWire(layers: number, list: boolean, column = false, page = false, json?: unknown): Uint8Array {
  const enc = new Encoder()
  if (column) enc.writeVarint(1)
  enc.writeVarint(0)
  if (layers > 1) {
    enc.writeVarint(1)
    enc.writeBool(true)
    enc.writeBytes(nestedModelWire(layers - 1, list, list, false, json))
  } else {
    enc.writeVarint(1)
    enc.writeBool(list)
    if (list) enc.writeBytes(Uint8Array.of(0, 0, 0))
    if (json !== undefined) {
      enc.writeVarint(2)
      if (column) {
        const value = new Encoder()
        value.writeJSON(json)
        // Column cells hold JSON text without the row's byte-length prefix.
        enc.writeBytes(new Decoder(value.bytes()).readBytes())
      } else enc.writeJSON(json)
    }
  }
  enc.writeEnd()
  if (page) for (const value of [1n, 1n, 20n]) enc.writeSvarint(value)
  return enc.bytes()
}

function retiredColumnPayload(kind: string, overlimit: boolean): Uint8Array {
  if (kind.startsWith('model')) return nestedModelWire(overlimit ? 64 : 63, false, kind === 'model-list')
  const enc = new Encoder()
  if (kind === 'scalar-list') enc.writeVarint(overlimit ? 1_000_001 : 0)
  else {
    const value = new TextEncoder().encode('['.repeat(overlimit ? 129 : 128) + '0' + ']'.repeat(overlimit ? 129 : 128))
    if (kind === 'json-list') { enc.writeArrayHeader(1); enc.writeBytes(value) }
    else return value
  }
  return enc.bytes()
}

function overwrittenColumn(payload: Uint8Array, nullable: boolean, overwrite: boolean, page: boolean): Uint8Array {
  const enc = new Encoder()
  enc.writeVarint(1)
  enc.writeVarint(0)
  enc.writeVarint(1)
  if (nullable) enc.writeBool(true)
  enc.writeBytes(payload)
  if (overwrite) {
    enc.writeVarint(1)
    if (nullable) enc.writeBool(false)
    else enc.writeBytes(Uint8Array.of(48))
  }
  enc.writeEnd()
  if (page) for (const value of [1n, 1n, 20n]) enc.writeSvarint(value)
  return enc.bytes()
}

it.each([false, true])('validates retired JSON cells without native parsing or value trees (list=%s)', async list => {
  await generateTypes({ models: { Payload: { name: 'Payload', fields: [
    { id: 1, name: 'value', type: 'JSON', isList: list, nullable: list },
  ] } }, apis: {} }, 'retired-json-allocation')
  const source = outputs.get('retired-json-allocation/types.ts') ?? ''
  const js = stripTypeScriptTypes(source.replace(/^import .*\n/gm, '')).replace(/^export /gm, '')
  const runtime = await import('@luxojs/client')
  const decode = new Function('runtime', `const { Decoder, ColumnarDecoder, decodeJSONValue, decodeScalarArray,
    validateJSONValue: $validateJSONValue, validateJSONArray: $validateJSONArray } = runtime; ${js}\nreturn decodeColumnarPayload`)(runtime) as
    (data: Uint8Array) => { value: unknown }[]
  const raw = new TextEncoder().encode('['.repeat(128) + '0' + ']'.repeat(128))
  const old = new Encoder()
  old.writeArrayHeader(2)
  old.writeBytes(raw)
  old.writeBytes(raw)
  const wire = overwrittenColumn(list ? old.bytes() : raw, list, true, false)
  const parse = vi.spyOn(JSON, 'parse')
  try {
    expect(decode(wire)[0].value).toBe(list ? null : 0)
    expect(parse).toHaveBeenCalledTimes(list ? 0 : 1)
  } finally { parse.mockRestore() }
})

it.each(['model', 'type'].flatMap(target => [false, true].flatMap(page =>
  ['model', 'model-list', 'json', 'json-null', 'json-list', 'scalar-list'].map(kind => ({ target, page, kind })),
)))('checks retired response columns before overwriting ($target, $page, $kind)', async ({ target, page, kind }) => {
  const field = { id: 1, name: 'value', type: kind.startsWith('model') ? 'Model' : kind === 'scalar-list' ? 'Int' : 'JSON',
    typeName: kind.startsWith('model') ? 'Node' : undefined, nullable: kind !== 'json', isList: kind.endsWith('-list') }
  const root = { name: 'Root', fields: [field] }
  const node = { name: 'Node', fields: [{ id: 1, name: 'next', type: 'Model', typeName: 'Node', nullable: true }] }
  await generateTypes({ models: target === 'model' ? { Root: root, Node: node } : {},
    types: target === 'type' ? { Root: root, Node: node } : {}, apis: {} }, 'retired-columns')
  const source = outputs.get('retired-columns/types.ts') ?? ''
  const js = stripTypeScriptTypes(source.replace(/^import .*\n/gm, '')).replace(/^export /gm, '')
  const runtime = await import('@luxojs/client')
  const decode = new Function('Decoder', 'ColumnarDecoder', 'decodeJSONValue', 'decodeScalarArray', '$validateJSONValue', '$validateJSONArray',
    `${js}\nreturn ${page ? 'decodePaginatedRoot' : 'decodeColumnarRoot'}`)(Decoder, ColumnarDecoder, runtime.decodeJSONValue, runtime.decodeScalarArray, runtime.validateJSONValue, runtime.validateJSONArray) as
    (data: Uint8Array) => { value: unknown }[] | { items: { value: unknown }[] }
  const invalid = retiredColumnPayload(kind, true)
  const message = kind.startsWith('model') ? /model nesting exceeds 64 levels/ : kind === 'scalar-list' ? /array/ : /JSON nesting exceeds 128 levels/
  for (const overwrite of [false, true]) {
    expect(() => decode(overwrittenColumn(invalid, field.nullable, overwrite, page))).toThrow(message)
  }
  const valid = decode(overwrittenColumn(retiredColumnPayload(kind, false), field.nullable, true, page))
  expect(('items' in valid ? valid.items : valid)[0].value).toBe(field.nullable ? null : 0)
})

it.each(['model', 'type'].flatMap(target => [false, true].flatMap(list =>
  ['json', 'json-list', 'row', 'column', 'page'].map(mode => ({ target, list, mode })),
)))('bounds generated response model depth at 64 ($target, $list, $mode)', async ({ target, list, mode }) => {
  const node = { name: 'Node', fields: [
    { id: 1, name: 'next', type: 'Model', typeName: 'Node', isList: list, nullable: true, relation: target === 'model' },
    { id: 2, name: 'json', type: 'JSON' },
  ] }
  const schema: LuxoSchema = { models: target === 'model' ? { Node: node } : {},
    types: target === 'type' ? { Node: node } : {}, apis: {} }
  await generateTypes(schema, 'model-depth')
  const source = outputs.get('model-depth/types.ts') ?? ''
  const js = stripTypeScriptTypes(source.replace(/^import .*\n/gm, '')).replace(/^export /gm, '')
  const load = new Function('Decoder', 'ColumnarDecoder', 'normalizeLuxoJSON', 'decodeJSONValue',
    `${js}\nreturn { row: decodeNode, json: decodeJSONNode, list: decodeJSONNode$list, column: decodeColumnarNode, page: decodePaginatedNode }`) as
    (d: typeof Decoder, c: typeof ColumnarDecoder, j: typeof normalizeLuxoJSON, b: typeof import('@luxojs/client').decodeJSONValue) => {
      row(dec: Decoder): unknown
      json(data: Record<string, unknown>): unknown
      list(data: unknown): unknown[]
      column(data: Uint8Array): unknown[]
      page(data: Uint8Array): { items: unknown[] }
    }
  const { decodeJSONValue } = await import('@luxojs/client')
  const decode = load(Decoder, ColumnarDecoder, normalizeLuxoJSON, decodeJSONValue)
  let json: unknown = {}
  for (let i = 1; i < 128; i++) json = { child: json }
  const run = (layers: number): unknown => {
    if (mode === 'json') return decode.json(nestedModelValue(layers, list, json))
    if (mode === 'json-list') return decode.list([nestedModelValue(layers, list, json)])[0]
    const wire = nestedModelWire(layers, list, mode !== 'row', mode === 'page', json)
    return mode === 'row' ? decode.row(new Decoder(wire)) : mode === 'column' ? decode.column(wire)[0] : decode.page(wire).items[0]
  }
  expect(run(64)).toEqual(nestedModelValue(64, list, json))
  expect(() => run(65)).toThrow(/model nesting exceeds 64 levels/)
  expect(run(1)).toEqual(nestedModelValue(1, list, json))
  if (mode.startsWith('json')) {
    const cyclic: Record<string, unknown> = {}
    cyclic.next = list ? [cyclic] : cyclic
    expect(() => mode === 'json' ? decode.json(cyclic) : decode.list([cyclic])).toThrow(/model nesting exceeds 64 levels/)
    expect(() => decode.json({ json: { child: json } })).toThrow(/JSON nesting exceeds 128 levels/)
  }
  // Public unary decoders remain safe as Array.map callbacks.
  expect(Array.from({ length: 66 }, () => ({ json })).map(decode.json)).toHaveLength(66)
  expect(Array.from({ length: 66 }, () => new Decoder(nestedModelWire(1, list))).map(decode.row)).toHaveLength(66)
  expect(Array.from({ length: 66 }, () => []).map(decode.list)).toHaveLength(66)
  expect(Array.from({ length: 66 }, () => nestedModelWire(1, list, true)).map(decode.column)).toHaveLength(66)
})

it.each(['json', 'row', 'column', 'page'].flatMap(mode =>
  ['kept', null, undefined].map(value => [mode, value] as const),
))('preserves prototype-named fields in %s responses (%s)', async (mode, value) => {
  const names = [...Object.getOwnPropertyNames(Object.prototype), 'prototype']
  const fields = names.map((name, index) => ({ id: index + 1, name, type: 'String', nullable: name === '__proto__' }))
  await generateTypes({ models: { Payload: { name: 'Payload', fields } }, apis: {} }, 'prototype-fields')
  const source = outputs.get('prototype-fields/types.ts') ?? ''
  const js = stripTypeScriptTypes(source.replace(/^import .*\n/gm, '')).replace(/^export /gm, '')
  const load = new Function('Decoder', 'ColumnarDecoder', 'stringFromJSON',
    `${js}\nreturn { row: decodePayload, json: decodeJSONPayload, column: decodeColumnarPayload, page: decodePaginatedPayload }`) as
    (d: typeof Decoder, c: typeof ColumnarDecoder, s: typeof stringFromJSON) => {
      row(dec: Decoder): Record<string, unknown>
      json(data: Record<string, unknown>): Record<string, unknown>
      column(data: Uint8Array): Record<string, unknown>[]
      page(data: Uint8Array): { items: Record<string, unknown>[] }
    }
  const decode = load(Decoder, ColumnarDecoder, stringFromJSON)
  const expected = Object.fromEntries(names.filter(name => name !== '__proto__' || value !== undefined)
    .map(name => [name, name === '__proto__' ? value : `value:${name}`]))
  const encoder = new Encoder()
  if (mode === 'column' || mode === 'page') encoder.writeVarint(1)
  encoder.writeVarint(0)
  for (const field of fields) {
    if (field.name !== '__proto__') { encoder.writeFieldString(field.id, `value:${field.name}`); continue }
    if (value === undefined) continue
    encoder.writeVarint(field.id)
    encoder.writeBool(value !== null)
    if (value !== null) encoder.writeString(value)
  }
  encoder.writeEnd()
  if (mode === 'page') for (const value of [1n, 1n, 20n]) encoder.writeSvarint(value)
  const actual = mode === 'json' ? decode.json(expected)
    : mode === 'row' ? decode.row(new Decoder(encoder.bytes()))
      : mode === 'column' ? decode.column(encoder.bytes())[0] : decode.page(encoder.bytes()).items[0]
  expect(Object.getPrototypeOf(actual)).toBe(Object.prototype)
  for (const name of names) {
    const absent = name === '__proto__' && value === undefined
    expect(Object.hasOwn(actual, name), name).toBe(true)
    expect(actual[name], name).toBe(absent ? undefined : expected[name])
  }
  if (mode === 'json' || mode === 'row') {
    const empty = mode === 'json' ? decode.json({}) : decode.row(new Decoder(new Uint8Array([0, 0])))
    for (const name of names) expect(empty[name], `unselected ${name}`).toBeUndefined()
  }
})

it('uses static encoders by default for unary and stream transport entries', async () => {
  const params = [{ id: 3, name: 'id', type: 'Int' }]
  await generateTypes({ models: {}, apis: {
    getUser: { id: 7, name: 'getUser', module: 'test', returnType: 'Int', params },
    watchUser: { id: 8, name: 'watchUser', module: 'test', returnType: 'Int', stream: true, params },
    ping: { id: 9, name: 'ping', module: 'test', returnType: 'Int' },
  } }, 'static-candidate')
  const client = outputs.get('static-candidate/client.ts') ?? ''
  expect(client).toContain('GeneratedTransport as Transport')
  expect(client).toContain("this.transport.callGenerated('getUser', $getUserRequest, params, options)")
  expect(client).toContain("this.transport.callGenerated('ping', $pingRequest, undefined, options)")
  expect(client).toContain("this.transport.subscribeGenerated('watchUser', $watchUserRequest, params,")
  expect(client).not.toContain('new $')
  expect(outputs.get('static-candidate/requests.ts')).not.toContain('constructor(')
})

it('generates dedicated requests for unary and streams instead of passing parameter records', async () => {
  const params = [{ id: 3, name: 'id', type: 'Int' }]
  await generateTypes({ models: {}, apis: {
    getUser: { id: 7, name: 'getUser', module: 'test', returnType: 'Int', params },
    watchUser: { id: 8, name: 'watchUser', module: 'test', returnType: 'Int', stream: true, params },
  } }, 'static-requests', { staticRequests: false })
  const client = outputs.get('static-requests/client.ts') ?? ''
  const requests = outputs.get('static-requests/requests.ts') ?? ''
  expect(client).toContain("this.transport.call('getUser', new $getUserRequest(params), options)")
  expect(client).toContain("this.transport.subscribe('watchUser', new $watchUserRequest(params)")
  expect(requests).toContain('extends GeneratedRequest')
  expect(requests).toContain('enc.writeVarint(3)')
  expect(requests).toContain('enc.writeSvarint(value)')
  expect(requests).not.toContain('encodeParam(')
  expect(requests).not.toContain('Object.entries(')
})

it('excludes explicit internal routes independently of their names', async () => {
  const apis = {
    ping: { id: 1, name: 'ping', module: 'test', returnType: 'Int', internal: false },
    privateOperation: { id: 2, name: 'privateOperation', module: 'test', returnType: 'Int', internal: true },
  }
  await generateTypes({ models: {}, apis }, 'internal-routes')
  for (const name of ['client.ts', 'schema.ts']) {
    const source = outputs.get(`internal-routes/${name}`) ?? ''
    expect(source).toContain('ping')
    expect(source).not.toContain('privateOperation')
  }
})

const schema: LuxoSchema = {
  models: {
    Node: {
      name: 'Node',
      usage: 'output',
      fields: [{ id: 1, name: 'id', type: 'Int' }],
    },
  },
  enums: {},
  types: {
    MetricPoint: {
      name: 'MetricPoint',
      usage: 'output',
      fields: [
        { id: 1, name: 'totalCount', type: 'Int' },
        { id: 2, name: 'labels', type: 'String', isList: true },
      ],
    },
    MetricTimeSeries: {
      name: 'MetricTimeSeries',
      usage: 'output',
      fields: [
        { id: 1, name: 'apiName', type: 'String' },
        { id: 2, name: 'points', type: 'Model', typeName: 'MetricPoint', isList: true, relation: true },
      ],
    },
		CreateNodeInput: {
			name: 'CreateNodeInput',
			usage: 'input',
			fields: [{ id: 1, name: 'name', type: 'String' }],
		},
  },
  apis: {
    listNodes: {
      id: 2,
      name: 'listNodes',
      module: 'project',
      returnType: 'Node',
      returnList: true,
      paginated: true,
      params: [
        { id: 7, name: 'page', type: 'Int', hasDefault: true },
        { id: 9, name: 'pageSize', type: 'Int', hasDefault: true },
      ],
    },
    getMetricTimeSeries: {
      id: 1,
      name: 'getMetricTimeSeries',
      module: 'monitoring',
      returnType: 'MetricTimeSeries',
      returnList: true,
      params: [
        { id: 1, name: 'projectId', type: 'Int' },
        { id: 2, name: 'apiName', type: 'String', nullable: true },
        { id: 3, name: 'tags', type: 'String', isList: true, hasDefault: true },
				{ id: 4, name: 'input', type: 'Model', typeName: 'CreateNodeInput' },
      ],
    },
    searchNodes: {
      id: 3,
      name: 'searchNodes',
      module: 'project',
      returnType: 'Node',
      returnList: true,
      paginated: true,
      params: [
        { id: 7, name: 'page', type: 'Int', hasDefault: true },
        { id: 9, name: 'pageSize', type: 'Int', hasDefault: true },
      ],
    },
    createSnapshot: {
      id: 4,
      name: 'createSnapshot',
      module: 'project',
      returnType: 'Node',
    },
  },
}

beforeEach(() => outputs.clear())

describe('stable Schema numbering', () => {
  for (const target of ['model', 'type', 'api', 'param'] as const) {
    const limit = target === 'api' ? 2147483647 : target === 'param' ? 2147483645 : 65535
    for (const id of [0, -1, 1.5, NaN, Infinity, limit + 1, Number.MAX_SAFE_INTEGER]) {
      it(`rejects ${target} ID ${id} before writing files`, async () => {
        const fixture = structuredClone(schema)
        const entry = target === 'model' ? fixture.models.Node.fields[0]
          : target === 'type' ? fixture.types!.MetricPoint.fields[0]
          : target === 'api' ? fixture.apis.getMetricTimeSeries
          : fixture.apis.getMetricTimeSeries.params![0]
        entry.id = id
        outputs.set('invalid/types.ts', 'existing client')
        await expect(generateTypes(fixture, 'invalid')).rejects.toThrow(/ID/)
        expect([...outputs]).toEqual([['invalid/types.ts', 'existing client']])
      })
    }
    it(`rejects missing ${target} ID`, async () => {
      const fixture = structuredClone(schema)
      const entry = target === 'model' ? fixture.models.Node.fields[0]
        : target === 'type' ? fixture.types!.MetricPoint.fields[0]
        : target === 'api' ? fixture.apis.getMetricTimeSeries
        : fixture.apis.getMetricTimeSeries.params![0]
      delete (entry as { id?: number }).id
      await expect(generateTypes(fixture, 'invalid')).rejects.toThrow(/ID/)
      expect(outputs.size).toBe(0)
    })
  }

  it('rejects duplicate numbers in each namespace', async () => {
    for (const target of ['model', 'type', 'api', 'param'] as const) {
      const fixture = structuredClone(schema)
      if (target === 'model') fixture.models.Node.fields.push({ id: 1, name: 'other', type: 'Int' })
      if (target === 'type') fixture.types!.MetricPoint.fields[1].id = 1
      if (target === 'api') fixture.apis.listNodes.id = fixture.apis.getMetricTimeSeries.id
      if (target === 'param') fixture.apis.getMetricTimeSeries.params![1].id = 1
      await expect(generateTypes(fixture, 'invalid')).rejects.toThrow(/duplicate.*ID/)
      expect(outputs.size).toBe(0)
    }
  })

  it('preserves legal sparse maxima and independent parameter namespaces', async () => {
    const fixture = structuredClone(schema)
    fixture.models.Node.fields[0].id = 65535
    fixture.types!.MetricPoint.fields[0].id = 65535
    fixture.apis.getMetricTimeSeries.id = 2147483647
    fixture.apis.getMetricTimeSeries.params![0].id = 2147483645
    fixture.apis.listNodes.params!.push({ id: 2147483645, name: 'cursor', type: 'Int' })
    await generateTypes(fixture, 'valid')
    expect(outputs.get('valid/schema.ts')).toContain('2147483647')
    expect(outputs.get('valid/schema.ts')).toContain('2147483645')
    expect(outputs.get('valid/schema.ts')).toContain('65535')
  })

  it('validates internal routes before excluding them from public code', async () => {
    const fixture = structuredClone(schema)
    fixture.apis.getMetricTimeSeries.internal = true
    fixture.apis.getMetricTimeSeries.id = 0
    await expect(generateTypes(fixture, 'invalid')).rejects.toThrow(/ID/)
    expect(outputs.size).toBe(0)
  })
})

describe('generateTypes', () => {
	it('keeps nullable array markers outside columnar array cells', async () => {
		await generateTypes({ models: { ArrayRecord: {
			name: 'ArrayRecord', usage: 'output',
			fields: [{ id: 1, name: 'values', type: 'Int', isList: true, nullable: true }],
		} }, apis: {} }, 'generated')
		const types = outputs.get('generated/types.ts') ?? ''
		expect(types).toContain("dec.readBool() ? dec.readIntArray() : null")
		expect(types).toContain('let _values: (Uint8Array | null)[] | undefined')
		expect(types).toContain('_values = r.readColumnBytesPtr()')
		expect(types).toContain("_values[i] === null ? null : decodeScalarArray(_values[i]!, 'Int') as bigint[]")
	})
	it('keeps default scalar fields strict and explicit selections tri-state', async () => {
		await generateTypes(schema, 'generated')

		const types = outputs.get('generated/types.ts') ?? ''
		const client = outputs.get('generated/client.ts') ?? ''
		expect(types).toContain('export interface Node<Selection extends boolean = false>')
		expect(types).toContain('id: true extends Selection ? Selected<bigint> : bigint')
		expect(client).toContain('createSnapshot<Select extends string | PreparedSelection | undefined = undefined>')
		expect(client).toContain('params?: { $select?: Select }')
		expect(client).toContain('Promise<Node<Select extends string | PreparedSelection ? true : false>>')
		expect(client).toContain('Promise<Page<Node<Select extends string | PreparedSelection ? true : false>>>')
	})

	it('keeps unloaded model relations optional in the default projection', async () => {
		await generateTypes({
			models: {
				Parent: {
					name: 'Parent',
					usage: 'output',
					fields: [
						{ id: 1, name: 'id', type: 'Int' },
						{ id: 2, name: 'children', type: 'Model', typeName: 'Child', isList: true, relation: true },
					],
				},
				Child: {
					name: 'Child',
					usage: 'output',
					fields: [{ id: 1, name: 'name', type: 'String' }],
				},
			},
			apis: {},
		}, 'generated')

		const types = outputs.get('generated/types.ts') ?? ''
		expect(types).toContain('id: true extends Selection ? Selected<bigint> : bigint')
		expect(types).toContain('children: Selected<Child<Selection>[]>')
	})

  it('generates optional parameters and binary array metadata', async () => {
    await generateTypes(schema, 'generated')

    const client = outputs.get('generated/client.ts') ?? ''
    const apiSchema = outputs.get('generated/schema.ts') ?? ''
		expect(client).toContain('apiName: string | null')
    expect(client).toContain('tags?: string[]')
		expect(client).toContain('input: CreateNodeInput')
		expect(client).toMatch(/import type \{[^\n]*CreateNodeInput[^\n]*\} from '\.\/types\.js'/)
    expect(client).toContain('TransportOptions, CallOptions, Page, Filter, Sorter')
    expect(client).toContain('$filters?: Filter[]; $sorters?: Sorter[]')
    expect(client).toContain('}, options?: CallOptions): globalThis.Promise<MetricTimeSeries<Select extends string | PreparedSelection ? true : false>[]>')
    expect(client).toContain('params?: { page?: bigint; pageSize?: bigint; $select?: Select; $filters?: Filter[]; $sorters?: Sorter[] }, options?: CallOptions')
    expect(client).toContain('searchNodes<Select extends string | PreparedSelection | undefined = undefined>(params?: { page?: bigint; pageSize?: bigint; $select?: Select; $filters?: Filter[]; $sorters?: Sorter[] }')
    expect(client).toContain('createSnapshot<Select extends string | PreparedSelection | undefined = undefined>(params?: { $select?: Select }, options?: CallOptions)')
    expect(client).not.toContain('createSnapshot(input:')
    expect(client).toContain('projectId: bigint; apiName: string | null; tags?: string[]; input: CreateNodeInput; $select?: Select')
    expect(client).toContain("this.transport.callGenerated('getMetricTimeSeries', $getMetricTimeSeriesRequest, params, options)")
    expect(apiSchema).toContain("name: 'tags', type: 'String', isList: true")
		expect(apiSchema).toContain("name: 'apiName', type: 'String', nullable: true")
		expect(apiSchema).toContain("name: 'input', type: 'Model', typeName: 'CreateNodeInput'")
		expect(apiSchema).toContain("'MetricTimeSeries': { 'apiName': { fieldID: 1, type: 'String' }, 'points': { fieldID: 2, type: 'Model', typeName: 'MetricPoint', isList: true } }")
		expect(apiSchema).toContain("'CreateNodeInput': { 'name': { fieldID: 1, type: 'String' } }")
		expect(apiSchema).toContain("fields: LUXO_SELECTION_TYPES['MetricTimeSeries'], types: LUXO_SELECTION_TYPES")
	})

	it('keeps absent and explicit null distinct for nullable patch params', async () => {
		const patchSchema: LuxoSchema = {
			models: schema.models,
			apis: {
				updateNode: {
					id: 3,
					name: 'updateNode',
					module: 'node',
					returnType: 'Node',
					params: [
						{ id: 1, name: 'id', type: 'Int' },
						{ id: 2, name: 'nickname', type: 'String', nullable: true, hasDefault: true },
					],
				},
			},
		}
		await generateTypes(patchSchema, 'generated')
		const client = outputs.get('generated/client.ts') ?? ''
		expect(client).toContain('nickname?: string | null')
	})

  it('decodes nested list columns from blob cells', async () => {
    await generateTypes(schema, 'generated')

    const types = outputs.get('generated/types.ts') ?? ''
    expect(types).toContain('let _points: Uint8Array[] | undefined')
    expect(types).toContain('points: _points ? decodeColumnarMetricPoint$value(_points[i], depth + 1) : undefined')
    expect(types).toContain('points: _points ? decodeColumnarMetricPoint$value(_points[i], 1 + 1) : undefined')
  })

  it('decodes UUID columns from their fixed-width binary representation', async () => {
    await generateTypes({
      models: {
        Project: {
          name: 'Project',
          fields: [
            { id: 1, name: 'publicId', type: 'UUID' },
            { id: 2, name: 'parentId', type: 'UUID', nullable: true },
          ],
        },
      },
      apis: {},
    }, 'generated')

    const types = outputs.get('generated/types.ts') ?? ''
    expect(types).toContain("case 1: _publicId = r.readColumnUUID(); break")
    expect(types).toContain("case 2: _parentId = r.readColumnUUIDPtr(); break")
  })

  it('decodes scalar list fields in row mode', async () => {
    await generateTypes(schema, 'generated')

    const types = outputs.get('generated/types.ts') ?? ''
    expect(types).toContain("case 2: obj['labels'] = dec.readStringArray(); break")
  })

  it('does not fabricate a value for an unselected required enum field', async () => {
    await generateTypes({
      models: {
        User: {
          name: 'User',
          fields: [{ id: 1, name: 'role', type: 'Enum', typeName: 'Role' }],
        },
      },
      enums: {
        Role: { name: 'Role', values: ['ADMIN', 'VIEWER'] },
      },
      apis: {},
    }, 'generated')

    const types = outputs.get('generated/types.ts') ?? ''
    expect(types).toContain('role: true extends Selection ? Selected<Role> : Role')
    expect(types).toContain("Object.prototype.hasOwnProperty.call(data, 'role')")
    expect(types).not.toContain("'' as Role")
    expect(types).toContain("UnknownEnumValue<'Role'>")
    expect(types).toContain('export function decodeEnum$Role')
    expect(types).toContain('decodeEnum$Role(stringFromJSON(')
    expect(types).toContain('decodeEnum$Role(dec.readString())')
    expect(types).toContain('decodeEnum$Role(_role[i]')
  })

  it('does not import an unused row decoder for list returns', async () => {
    await generateTypes(schema, 'generated')

    const client = outputs.get('generated/client.ts') ?? ''
    expect(client).toContain('decodeColumnarMetricTimeSeries')
    expect(client).not.toMatch(/import \{[^\n]*decodeMetricTimeSeries[, }]/)
  })

  it('keeps generated list helpers disjoint from model names', async () => {
    await generateTypes({
      models: Object.fromEntries(['Metric', 'MetricItem', 'ItemMetric'].map(name => [name, {
        name, fields: [{ id: 1, name: 'id', type: 'Int' }],
      }])),
      apis: { rows: { id: 1, name: 'rows', module: 'test', returnType: 'Metric', returnList: true } },
    }, 'generated')
    const source = outputs.get('generated/types.ts') ?? ''
    const names = [...source.matchAll(/(?:export )?function ([\w$]+)\(/g)].map(match => match[1])
    expect(new Set(names).size).toBe(names.length)
  })

  it('only imports runtime decode helpers used by the generated schema', async () => {
    await generateTypes({
      models: {
        Node: { name: 'Node', fields: [{ id: 1, name: 'id', type: 'Int' }] },
      },
      apis: {
        listNodes: {
          id: 1,
          name: 'listNodes',
          module: 'node',
          returnType: 'Node',
          returnList: true,
        },
      },
    }, 'generated')

    const types = outputs.get('generated/types.ts') ?? ''
    const client = outputs.get('generated/client.ts') ?? ''
    expect(types).toContain("import { Decoder, ColumnarDecoder, int64FromJSON } from '@luxojs/client'")
    expect(types).not.toContain('decodeBase64')
    expect(types).not.toContain('decodeJSONValue')
    expect(types).not.toContain('decodeScalarArray')
    expect(types).not.toContain('unixSecondsToISO')
    expect(client).toContain("import { FetchTransport, WsTransport } from '@luxojs/client'")
    expect(client).not.toContain('Decoder')
    expect(client).not.toContain('decodeBase64')
    expect(client).not.toContain('decodeJSONValue')
    expect(client).not.toContain('unixSecondsToISO')
  })

  it('preserves selected JSON null fields and rejects undefined or fieldless list items', async () => {
    await generateTypes({ models: { Payload: { name: 'Payload', fields: [
      { id: 1, name: 'payload', type: 'JSON' }, { id: 2, name: 'items', type: 'JSON', isList: true },
      { id: 3, name: 'title', type: 'String' },
    ] } }, apis: { read: { id: 1, name: 'read', module: 'test', returnType: 'Payload' } } }, 'generated')
    const source = outputs.get('generated/types.ts') ?? ''
    const js = stripTypeScriptTypes(source.replace(/^import .*\n/gm, '')).replace(/^export /gm, '')
    const load = new Function('normalizeLuxoJSON', 'stringFromJSON',
      `${js}\nreturn { one: decodeJSONPayload, list: decodeJSONPayload$list }`) as
      (json: typeof normalizeLuxoJSON, text: typeof stringFromJSON) => {
        one(value: Record<string, unknown>): Record<string, unknown>
        list(value: unknown): Record<string, unknown>[]
      }
    const decode = load(normalizeLuxoJSON, stringFromJSON)
    expect(decode.one({ payload: null })).toEqual({ payload: null })
    expect(Object.hasOwn(decode.one({ payload: null }), 'payload')).toBe(true)
    expect(decode.one({})).toEqual({})
    expect(decode.list([{ payload: null }])).toEqual([{ payload: null }])
    expect(decode.one({ items: [null] })).toEqual({ items: [null] })
    for (const value of [{ payload: undefined }, { items: null }, { title: null }]) {
      expect(() => decode.one(value)).toThrow(TypeError)
    }
    for (const value of [[{}], [{}, { payload: null }], [{ payload: null }, {}]]) {
      expect(() => decode.list(value)).toThrow(TypeError)
    }
  })

  it('keeps JSON and binary model decoding type-equivalent', async () => {
    await generateTypes({
      models: {
        Payload: {
          name: 'Payload',
          fields: [
            { id: 1, name: 'blob', type: 'Bytes' },
            { id: 2, name: 'metadata', type: 'JSON' },
          ],
        },
      },
      apis: {
        listPayloads: {
          id: 3,
          name: 'listPayloads',
          module: 'file',
          returnType: 'Payload',
          returnList: true,
        },
      },
    }, 'generated')

    const types = outputs.get('generated/types.ts') ?? ''
    const client = outputs.get('generated/client.ts') ?? ''
    expect(types).toContain('blob: true extends Selection ? Selected<Uint8Array> : Uint8Array')
    expect(types).toContain('metadata: true extends Selection ? Selected<unknown> : unknown')
    expect(types).toContain("Object.prototype.hasOwnProperty.call(data, 'blob')")
    expect(types).not.toContain('new Uint8Array(0)')
    expect(types).toContain('decodeJSONValue(_metadata![i]!)')
    expect(client).toContain('decodeJSONPayload')
  })

  it('separates strict input DTOs from field-selected output models', async () => {
    await generateTypes({
      models: {},
      types: {
        Profile: {
          name: 'Profile',
          usage: 'unused',
          fields: [
            { id: 1, name: 'name', type: 'String' },
            { id: 2, name: 'bio', type: 'String', nullable: true },
          ],
        },
      },
      apis: {
        updateProfile: {
          id: 1,
          name: 'updateProfile',
          module: 'profile',
          returnType: 'Profile',
          params: [{ id: 1, name: 'profile', type: 'Model', typeName: 'Profile' }],
        },
      },
    }, 'generated')

    const types = outputs.get('generated/types.ts') ?? ''
    const client = outputs.get('generated/client.ts') ?? ''
    expect(types).toContain('export interface Profile<Selection extends boolean = false> {')
    expect(types).toContain('name: true extends Selection ? Selected<string> : string')
    expect(types).toContain('bio: true extends Selection ? Selected<string | null> : string | null')
    expect(types).toContain('export interface ProfileInput {')
    expect(types).toContain('name: string')
    expect(types).toContain('bio: string | null')
    expect(client).toContain('profile: ProfileInput')
    expect(client).toContain('Promise<Profile<Select extends string | PreparedSelection ? true : false>>')
  })
})

describe('stream client generation', () => {
  it('generates callback subscriptions instead of one-shot calls', async () => {
    await generateTypes({
      models: {
        Alert: { name: 'Alert', fields: [{ id: 1, name: 'id', type: 'Int' }] },
      },
      apis: {
        liveAlerts: {
          id: 9,
          name: 'liveAlerts',
          module: 'alert',
          returnType: 'Alert',
          stream: true,
          params: [{ id: 1, name: 'projectId', type: 'Int' }],
        },
        systemReady: {
          id: 10,
          name: 'systemReady',
          module: 'alert',
          returnType: 'Alert',
          stream: true,
        },
      },
    }, 'generated')

    const client = outputs.get('generated/client.ts') ?? ''
    expect(client).toContain('subscribeLiveAlerts<Select extends string | PreparedSelection | undefined = undefined>(params: { projectId: bigint; $select?: Select }, onData: (data: Alert<Select extends string | PreparedSelection ? true : false>) => void, observer?: StreamObserver): globalThis.Promise<() => void>')
    expect(client).toContain('subscribeSystemReady<Select extends string | PreparedSelection | undefined = undefined>(params: { $select?: Select }, onData: (data: Alert<Select extends string | PreparedSelection ? true : false>) => void, observer?: StreamObserver): globalThis.Promise<() => void>')
    expect(client).toContain('}, onData, observer)')
    expect(client).not.toContain('return onData(')
    expect(client).toContain("this.transport.subscribeGenerated('liveAlerts', $liveAlertsRequest, params,")
    expect(client).not.toContain('async liveAlerts(')
  })
})
