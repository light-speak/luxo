import { expect, it } from 'vitest'
import { execFile } from 'node:child_process'
import { createServer } from 'node:http'
import { promisify } from 'node:util'
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from 'node:fs'
import { resolve, join, sep } from 'node:path'
import { generateTypes } from './codegen'
import type { LuxoSchema } from '@luxojs/client'

const runFile = promisify(execFile)
const scalarTypes = ['String', 'Float', 'Boolean', 'Int', 'Duration', 'DateTime', 'UUID', 'Bytes', 'Decimal', 'JSON', 'Enum']

const schema: LuxoSchema = {
  enums: { State: { name: 'State', values: ['ok'] } },
  types: { TextValues: { name: 'TextValues', fields: [
    { id: 1, name: 'label', type: 'String' },
    { id: 2, name: 'enabled', type: 'Boolean' },
    { id: 3, name: 'labels', type: 'String', isList: true },
    { id: 4, name: 'flags', type: 'Boolean', isList: true },
    { id: 5, name: 'state', type: 'Enum', typeName: 'State', nullable: true },
    { id: 6, name: 'states', type: 'Enum', typeName: 'State', isList: true },
    { id: 7, name: 'child', type: 'Model', typeName: 'TextValues', nullable: true },
    { id: 8, name: 'children', type: 'Model', typeName: 'TextValues', isList: true },
  ] } },
  models: { Metric: { name: 'Metric', fields: [
    { id: 1, name: 'id', type: 'Int' },
    { id: 2, name: 'duration', type: 'Duration', nullable: true },
    { id: 3, name: 'counts', type: 'Int', isList: true },
    { id: 4, name: 'score', type: 'Float' },
  ] } },
  apis: {
    getMetric: { id: 1, name: 'getMetric', module: 'test', returnType: 'Metric', params: [{ id: 1, name: 'id', type: 'Int' }] },
    count: { id: 2, name: 'count', module: 'test', returnType: 'Int' },
    durations: { id: 3, name: 'durations', module: 'test', returnType: 'Duration', returnList: true },
    listMetrics: { id: 4, name: 'listMetrics', module: 'test', returnType: 'Metric', returnList: true, paginated: true },
    liveMetrics: { id: 5, name: 'liveMetrics', module: 'test', returnType: 'Metric', stream: true },
    echoDuration: { id: 6, name: 'echoDuration', module: 'test', returnType: 'Duration', params: [{ id: 1, name: 'value', type: 'Duration' }] },
    getTextValues: { id: 7, name: 'getTextValues', module: 'test', returnType: 'TextValues' },
    metricRows: { id: 8, name: 'metricRows', module: 'test', returnType: 'Metric', returnList: true },
    liveMetricRows: { id: 9, name: 'liveMetricRows', module: 'test', returnType: 'Metric', returnList: true, stream: true },
  },
}
for (const type of scalarTypes) {
  for (const list of [false, true]) {
    for (const stream of [false, true]) {
      const name = `${stream ? 'live' : 'get'}${type}${list ? 'List' : ''}`
      schema.apis[name] = { id: Object.keys(schema.apis).length + 1, name, module: 'test',
        returnType: type === 'Enum' ? 'State' : type, returnList: list, stream }
    }
  }
}
schema.apis.implicitCount = { id: 100, name: 'implicitCount', module: 'test' }
const responseConsumers = ['json-kind-consumer', 'json-projection-consumer', 'stream-isolation-consumer'] as const

it.each(['commonjs', 'module'])('compiles a generated %s consumer with TS 7 and executes real HTTP JSON/binary contracts', async moduleType => {
  const root = resolve(import.meta.dirname, '../../..')
  const temporaryRoot = join(root, '.tmp')
  mkdirSync(temporaryRoot, { recursive: true })
  const directory = mkdtempSync(join(temporaryRoot, 'ts-int64-consumer-'))
  let fixture: Awaited<ReturnType<typeof startHTTPFixture>> | undefined
  try {
    fixture = await startHTTPFixture()
    await generateTypes(schema, directory)
    if (process.env.LUXO_CAPTURE_JSON_PROJECTION === '1') captureProjectionBaseline(root, directory)
    prepareResponseConsumers(directory)
    const benchmarks = prepareResponseBenchmarks(root, directory)
    prepareConsumer(root, directory, fixture.endpoint, moduleType)
    const tsc = join(root, 'packages/client/node_modules/.bin/tsc')
    await runFile(tsc, ['-p', directory], { timeout: 60000 })
    for (const script of ['consumer', 'scalar-consumer', ...responseConsumers, 'codec-example']) {
      await runFile(process.execPath, [join(directory, `out/${script}.js`)], { timeout: 15000 })
    }
    for (const script of benchmarks) await runBenchmark(directory, moduleType, script)
    expect(fixture.requests).toHaveLength(14)
  } catch (error) {
    if (error instanceof Error && 'stdout' in error) error.message += '\n' + String(error.stdout)
    throw error
  } finally {
    try { await fixture?.close() } finally {
      if (directory.startsWith(temporaryRoot + sep)) rmSync(directory, { recursive: true })
    }
  }
}, 90000)

function prepareResponseConsumers(directory: string): void {
  writeFileSync(join(directory, 'scalar-consumer.ts'), scalarConsumer())
  for (const script of responseConsumers) {
    writeFileSync(join(directory, `${script}.ts`), readFileSync(
      join(import.meta.dirname, `fixtures/${script}.ts.txt`), 'utf8'))
  }
}

function prepareResponseBenchmarks(root: string, directory: string): string[] {
  const setups: [string, string, () => void][] = [
    ['LUXO_BENCH_RESPONSE_COMPLETION', 'benchmark', () => prepareCompletionBenchmark(directory)],
    ['LUXO_BENCH_SCALAR_COMPLETION', 'scalar-benchmark', () => prepareScalarBenchmark(directory)],
    ['LUXO_BENCH_JSON_KINDS', 'json-kind-benchmark', () => prepareJSONKindBenchmark(directory)],
    ['LUXO_BENCH_JSON_PROJECTION', 'json-projection-benchmark', () => prepareJSONProjectionBenchmark(root, directory)],
  ]
  const scripts: string[] = []
  for (const [flag, script, prepare] of setups) {
    if (process.env[flag] !== '1') continue
    prepare()
    scripts.push(script)
  }
  return scripts
}

function captureProjectionBaseline(root: string, directory: string): void {
  const baseline = join(root, '.tmp/ts-json-projection-before')
  mkdirSync(baseline, { recursive: true })
  for (const file of ['types.ts', 'client.ts']) {
    const source = readFileSync(join(directory, file), 'utf8'), target = join(baseline, file)
    if (existsSync(target) && readFileSync(target, 'utf8') !== source) throw new Error('Projection baseline already exists with different content')
    writeFileSync(target, source)
  }
}

function prepareJSONProjectionBenchmark(root: string, directory: string): void {
  for (const file of ['types', 'client']) {
    const source = readFileSync(join(root, '.tmp/ts-json-projection-before', `${file}.ts`), 'utf8')
    if (source.includes('requireField') || source.includes('$item(')) throw new Error('Expected the pre-projection generator snapshot')
    writeFileSync(join(directory, `${file}.before.ts`), source.replaceAll("'./types.js'", "'./types.before.js'"))
  }
  writeFileSync(join(directory, 'json-projection-benchmark.ts'), readFileSync(
    join(import.meta.dirname, 'fixtures/json-projection.bench.ts.txt'), 'utf8'))
  copyBenchmarkMeasure(directory)
}

function prepareConsumer(root: string, directory: string, endpoint: string, moduleType: string): void {
  const scope = join(directory, 'node_modules/@luxojs')
  mkdirSync(scope, { recursive: true })
  symlinkSync(join(root, 'packages/client'), join(scope, 'client'), 'dir')
  writeFileSync(join(directory, 'consumer.ts'), `const endpoint = ${JSON.stringify(endpoint)}\n${consumer}`)
  const readme = readFileSync(join(root, 'packages/client/README.md'), 'utf8')
  const example = readme.split('### Codec Utilities')[1]?.match(/```ts\n([\s\S]*?)```/)?.[1]
  if (!example) throw new Error('Missing documented codec example')
  writeFileSync(join(directory, 'codec-example.ts'), example)
  writeFileSync(join(directory, 'package.json'), JSON.stringify({ type: moduleType }))
  writeFileSync(join(directory, 'tsconfig.json'), JSON.stringify({ compilerOptions: {
    target: 'ES2022', module: 'NodeNext', moduleResolution: 'NodeNext',
    strict: true, outDir: 'out', types: [],
  }, include: ['*.ts'] }))
}

async function runBenchmark(directory: string, moduleType: string, script: string): Promise<void> {
  for (let round = 1; round <= 3; round++) {
    const result = await runFile(process.execPath, [join(directory, `out/${script}.js`)], { timeout: 30000 })
    console.log(`${script} ${moduleType}, round ${round}\n${result.stdout}`)
  }
}

function scalarConsumer(): string {
  const checks = scalarTypes.map(type => `
    await verify('${type}', () => client.get${type}(), () => client.get${type}List(),
      onData => client.subscribeLive${type}(onData), onData => client.subscribeLive${type}List(onData))
  `).join('')
  const fixture = readFileSync(join(import.meta.dirname, 'fixtures/scalar-consumer.ts.txt'), 'utf8')
  return fixture.replace('// GENERATED_CHECKS', checks)
}

function prepareCompletionBenchmark(directory: string): void {
  const source = readFileSync(join(directory, 'types.ts'), 'utf8')
  const checks = [' || dec.remaining !== 0', ' || r.offset() !== data.length']
  let unchecked = source
  for (const check of checks) {
    if (!unchecked.includes(check)) throw new Error(`Missing completion check: ${check}`)
    unchecked = unchecked.replaceAll(check, '')
  }
  // Counterfactual benchmark only: keep decoding/error checks but omit trailing-byte validation.
  writeFileSync(join(directory, 'types.unchecked.ts'), unchecked)
  writeFileSync(join(directory, 'benchmark.ts'), readFileSync(
    join(import.meta.dirname, 'fixtures/response-completion.bench.ts.txt'), 'utf8'))
  copyBenchmarkMeasure(directory)
}

function prepareScalarBenchmark(directory: string): void {
  const source = readFileSync(join(directory, 'client.ts'), 'utf8')
  // Counterfactual: isolate completion checks; keep the same decoder and callback body.
  const unchecked = source.replace(/^.*if \(dec\.error !== null \|\| dec\.remaining !== 0\).*\n/gm, '')
  if (source === unchecked) throw new Error('Missing scalar completion checks')
  writeFileSync(join(directory, 'client.unchecked.ts'), unchecked)
  writeFileSync(join(directory, 'scalar-benchmark.ts'), readFileSync(
    join(import.meta.dirname, 'fixtures/scalar-response.bench.ts.txt'), 'utf8'))
  copyBenchmarkMeasure(directory)
}

function prepareJSONKindBenchmark(directory: string): void {
  for (const file of ['client', 'types']) {
    const source = readFileSync(join(directory, `${file}.ts`), 'utf8')
    // Counterfactual: reproduce the previous unchecked casts, keeping other decoding unchanged.
    let unchecked = source.replaceAll("'./types.js'", "'./types.unchecked.js'")
    for (const type of ['string', 'boolean']) {
      const name = `${type}FromJSON`
      if (!unchecked.includes(`${name}(`)) throw new Error(`Missing ${name} checks`)
      unchecked = unchecked.replaceAll(`${name}(`, `unchecked${type}(`)
      unchecked += `\nfunction unchecked${type}(value: unknown): ${type} { return value as ${type} }\n`
    }
    writeFileSync(join(directory, `${file}.unchecked.ts`), unchecked)
  }
  writeFileSync(join(directory, 'json-kind-benchmark.ts'), readFileSync(
    join(import.meta.dirname, 'fixtures/json-kind-response.bench.ts.txt'), 'utf8'))
  copyBenchmarkMeasure(directory)
}

function copyBenchmarkMeasure(directory: string): void {
  writeFileSync(join(directory, 'measure.ts'), readFileSync(
    join(import.meta.dirname, 'fixtures/measure.bench.ts.txt'), 'utf8'))
}

async function startHTTPFixture() {
  const requests: Buffer[] = []
  const server = createServer((request, response) => {
    const chunks: Buffer[] = []
    request.on('data', (chunk: Buffer) => chunks.push(chunk))
    request.on('end', () => {
      const body = Buffer.concat(chunks)
      requests.push(body)
      if (request.headers['content-type'] === 'application/x-luxo') {
        if (!body.subarray(0, 3).equals(Buffer.from([6, 0, 1])) || body.at(-1) !== 0) {
          response.writeHead(400).end()
          return
        }
        response.writeHead(200, { 'Content-Type': 'application/x-luxo' }).end(body.subarray(3, -1))
        return
      }
      const value = /"value":(-?\d+)(?:,|})/.exec(body.toString())?.[1]
      if (!value) response.writeHead(400).end()
      else response.writeHead(200, { 'Content-Type': 'application/json' }).end(`{"data":${value}}`)
    })
  })
  await new Promise<void>((resolve, reject) => {
    server.once('error', reject)
    server.listen(0, '127.0.0.1', resolve)
  })
  const address = server.address()
  if (!address || typeof address === 'string') throw new Error('expected TCP fixture address')
  return { requests, endpoint: `http://127.0.0.1:${address.port}/api`,
    close: () => new Promise<void>((resolve, reject) => {
      server.closeAllConnections()
      server.close(error => error ? reject(error) : resolve())
    }),
  }
}

const consumer = `
import { LuxoClient } from './client.js'
import { decodeJSONMetric, decodeMetric, decodeColumnarMetric, decodePaginatedMetric } from './types.js'
import { Encoder, Decoder, decodeLuxoJSON, int64FromJSON } from '@luxojs/client'
import type { Transport, LuxoTypeDecl } from '@luxojs/client'

const declaration: LuxoTypeDecl = { name: 'MetricInput', fields: [{ id: 1, name: 'value', type: 'Int' }] }

function equal(actual: unknown, expected: unknown): void {
  if (actual !== expected) throw new Error(String(actual) + ' != ' + String(expected))
}
function rejects(fn: () => unknown): void {
  try { fn() } catch { return }
  throw new Error('expected rejection')
}
async function rejectsAsync(fn: () => Promise<unknown>): Promise<void> {
  try { await fn() } catch (error) {
    if (error instanceof TypeError || error instanceof RangeError) return
    throw error
  }
  throw new Error('expected local integer rejection')
}
async function main(): Promise<void> {
  equal(declaration.fields[0].type, 'Int')
  const value = 9223372036854775807n
  const row = decodeJSONMetric(decodeLuxoJSON('{"id":9223372036854775807,"duration":null,"counts":[-9223372036854775808],"score":1.0}', true) as Record<string, unknown>)
  equal(row.id, value)
  equal(row.duration, null)
  equal(row.counts?.[0], -9223372036854775808n)
  equal(row.score, 1)
  const enc = new Encoder()
  enc.writeVarint(0)
  enc.writeFieldInt(1, value)
  enc.writeEnd()
  equal(decodeMetric(new Decoder(enc.bytes())).id, value)
  enc.reset()
  enc.writeVarint(1)
  enc.writeVarint(0)
  enc.writeVarint(1)
  enc.writeSvarint(value)
  enc.writeEnd()
  for (const metadata of [value, 1n, 20n]) enc.writeSvarint(metadata)
  const page = decodePaginatedMetric(enc.bytes())
  equal(page.total, value)
  equal(page.items[0].id, value)
  rejects(() => decodePaginatedMetric(enc.bytes().subarray(0, enc.bytes().length - 1)))
  // Verify generated consumers propagate codec errors before materializing rows.
  for (const count of [10_000_001, 4294967296]) {
    const invalid = new Encoder()
    invalid.writeVarint(count)
    invalid.writeVarint(0)
    invalid.writeEnd()
    rejects(() => decodeColumnarMetric(invalid.bytes()))
    rejects(() => decodePaginatedMetric(invalid.bytes()))
  }
  for (const arena of [67_108_865, 4294967296]) {
    const invalid = new Encoder()
    invalid.writeVarint(arena)
    invalid.writeEnd()
    rejects(() => decodeMetric(new Decoder(invalid.bytes())))
  }
  for (const wire of [
    [1, 0, 0], // Nonempty list with no column.
    [1, 0, 0, 0, 0], // Padding cannot supply a missing column.
    [1, 0, 0x80, 0, 0], // Multi-byte zero is still an end marker.
    [0x80, 0x89, 0x7a, 0, 0], // One million rows with no column payload.
    [1, 0, 4], // Missing fixed-width Float cell.
    [1, 0, 3, 2, 0], // Truncated blob containing an Int array.
    [1, 0, 3, 1, 2], // Int array declares two items without a payload.
  ]) {
    rejects(() => decodeColumnarMetric(Uint8Array.from(wire)))
    rejects(() => decodePaginatedMetric(Uint8Array.from(wire)))
  }
  rejects(() => int64FromJSON(decodeLuxoJSON('1e0', true)))
  let response: unknown = value
  const transport: Transport = {
    async call() { return response }, setMode() {}, setToken() {}, setSchema() {},
    async subscribe(_api, _params, decode, onData) { onData(decode({ id: value })); return () => {} },
  }
  const client = new LuxoClient(transport)
  equal(await client.count(), value)
  response = { items: [{ id: value }], total: value, page: 1, pageSize: 20 }
  equal((await client.listMetrics({ page: 1n, pageSize: 20n })).total, value)
  const unsubscribe = await client.subscribeLiveMetrics({}, data => equal(data.id, value))
  unsubscribe()
  response = new Uint8Array([128])
  let failed = false
  try { await client.count() } catch { failed = true }
  equal(failed, true)
  const live = LuxoClient.create(endpoint)
  for (const mode of ['json', 'binary'] as const) {
    live.setMode(mode)
    for (const value of [0n, 1n, -1n, 9007199254740993n, -9007199254740993n, -(1n << 63n), (1n << 63n) - 1n]) {
      equal(await live.echoDuration({ value }), value)
    }
    for (const value of [1n << 63n, -(1n << 63n) - 1n, 9007199254740992 as unknown as bigint]) {
      await rejectsAsync(() => live.echoDuration({ value }))
    }
  }
  live.close()
}
void main().catch(error => { throw error })
`
