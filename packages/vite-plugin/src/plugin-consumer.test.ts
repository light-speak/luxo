import { afterAll, beforeAll, expect, it } from 'vitest'
import { execFile } from 'node:child_process'
import { existsSync, mkdirSync, mkdtempSync, readFileSync, realpathSync, rmSync, symlinkSync, writeFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { dirname, join, resolve, sep } from 'node:path'
import { promisify } from 'node:util'
import type { LuxoSchema } from '@luxojs/client'

const executeFile = promisify(execFile)

async function runFile(command: string, args: string[], options: { cwd?: string; timeout: number }) {
  try {
    return await executeFile(command, args, options)
  } catch (error) {
    if (error instanceof Error && 'stdout' in error) error.message += '\n' + String(error.stdout)
    if (error instanceof Error && 'stderr' in error) error.message += '\n' + String(error.stderr)
    throw error
  }
}
const packageRoot = resolve(import.meta.dirname, '..')
const repositoryRoot = resolve(packageRoot, '../..')
const temporaryRoot = join(repositoryRoot, '.tmp')
let consumerRoot = packageRoot
const schema: LuxoSchema = {
  models: { Metric: { name: 'Metric', fields: [
    { id: 1, name: 'id', type: 'Int' },
    { id: 2, name: 'label', type: 'String' },
  ] } },
  apis: { getMetric: { id: 1, name: 'getMetric', module: 'metrics', returnType: 'Metric',
    params: [{ id: 1, name: 'id', type: 'Int' }],
  } },
}

beforeAll(async () => {
  await runFile('pnpm', ['build'], { cwd: packageRoot, timeout: 60000 })
  mkdirSync(temporaryRoot, { recursive: true })
  consumerRoot = mkdtempSync(join(temporaryRoot, 'packed-vite-plugin-'))
  await runFile('pnpm', ['pack', '--out', join(consumerRoot, 'plugin.tgz')], { cwd: packageRoot, timeout: 30000 })
  preparePackedConsumer()
  await runFile('pnpm', ['install', '--offline', '--no-frozen-lockfile', '--ignore-scripts'], { cwd: consumerRoot, timeout: 60000 })
}, 180000)

afterAll(() => {
  if (consumerRoot.startsWith(temporaryRoot + sep)) rmSync(consumerRoot, { recursive: true })
})

function preparePackedConsumer(): void {
  const require = createRequire(import.meta.url)
  const dependencies: Record<string, string> = {
    '@luxojs/vite-plugin': 'file:./plugin.tgz',
    '@luxojs/client': `link:${join(repositoryRoot, 'packages/client')}`,
  }
  // Reuse installed peers/tooling; only the plugin under test comes from this
  // archive. Client archive consumption is covered by the React package suite.
  for (const name of ['@babel/parser', '@babel/traverse', '@babel/types', 'typescript', 'vite']) {
    dependencies[name] = `link:${dirname(require.resolve(`${name}/package.json`))}`
  }
  // Bind the archive's Babel imports to the same installed versions as the
  // build. Offline registry metadata need not contain those cached releases.
  const overrides = Object.fromEntries(['@babel/parser', '@babel/traverse', '@babel/types'].map(name => [name, dependencies[name]]))
  writeFileSync(join(consumerRoot, 'package.json'), JSON.stringify({
    private: true, devDependencies: dependencies,
  }))
  writeFileSync(join(consumerRoot, 'pnpm-workspace.yaml'), JSON.stringify({ overrides }))
}

it('resolves the plugin from an installed archive, never the workspace source', () => {
  const require = createRequire(join(consumerRoot, 'package.json'))
  const entry = realpathSync(require.resolve('@luxojs/vite-plugin'))
  expect(entry.startsWith(temporaryRoot + sep)).toBe(true)
  expect(existsSync(join(dirname(entry), '../src'))).toBe(false)
})

it('publishes declarations generated from the implementation, not a duplicate public interface', () => {
  const installed = join(consumerRoot, 'node_modules/@luxojs/vite-plugin')
  const manifest = JSON.parse(readFileSync(join(installed, 'package.json'), 'utf8')) as {
    types: string; exports: { '.': { types: string } }; files: string[]
  }
  expect(manifest.types).toBe('dist/index.d.ts')
  expect(manifest.exports['.'].types).toBe('./dist/index.d.ts')
  expect(manifest.files).toEqual(['dist'])
  expect(readFileSync(join(installed, manifest.types), 'utf8')).toContain('interface LuxoPluginOptions')
  expect(existsSync(join(installed, 'src'))).toBe(false)
})

it.each(['commonjs', 'module'])('builds and executes a real Vite %s consumer from a separate project root', async moduleType => {
  mkdirSync(temporaryRoot, { recursive: true })
  const directory = mkdtempSync(join(temporaryRoot, 'vite-consumer-'))
  try {
    writeFixture(directory)
    const result = await runFile(process.execPath, ['--input-type=module', '-e', buildConsumer, directory, moduleType], {
      cwd: consumerRoot, timeout: 60000,
    })
    expect(result.stdout).toContain('selection and int64 verified')
  } finally {
    if (directory.startsWith(temporaryRoot + sep)) rmSync(directory, { recursive: true })
  }
}, 65000)

function writeFixture(directory: string): void {
  const scope = join(directory, 'node_modules/@luxojs')
  mkdirSync(scope, { recursive: true })
  symlinkSync(join(repositoryRoot, 'packages/client'), join(scope, 'client'), 'dir')
  mkdirSync(join(directory, 'schema'))
  mkdirSync(join(directory, 'src'))
  writeFileSync(join(directory, 'schema/luxo.schema.json'), JSON.stringify(schema))
  writeFileSync(join(directory, 'src/main.mts'), consumerSource)
}

it.each(['commonjs', 'module'])('exposes standalone generation through the published %s entry', async moduleType => {
  mkdirSync(temporaryRoot, { recursive: true })
  const directory = mkdtempSync(join(temporaryRoot, 'standalone-sdk-'))
  try {
    writeFixture(directory)
    const result = await runFile(process.execPath, ['--input-type=module', '-e', standaloneConsumer, directory, moduleType], {
      cwd: consumerRoot, timeout: 60000,
    })
    expect(result.stdout).toContain('standalone SDK verified')
    expect(readFileSync(join(consumerRoot, 'node_modules/@luxojs/vite-plugin/dist/index.d.ts'), 'utf8')).toContain('generateSDK(')
  } finally {
    if (directory.startsWith(temporaryRoot + sep)) rmSync(directory, { recursive: true })
  }
}, 65000)

const standaloneConsumer = `
import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
import { readFileSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
const require = createRequire(import.meta.url)
const directory = process.argv[1]
const { generateSDK } = process.argv[2] === 'commonjs' ? require('@luxojs/vite-plugin') : await import('@luxojs/vite-plugin')
const options = { schema: 'schema/luxo.schema.json', outDir: 'src/generated' }
const schema = await generateSDK(options, directory)
assert.equal(schema.models.Metric.fields[0].type, 'Int')
const output = join(directory, 'src/generated/types.ts')
const before = readFileSync(output, 'utf8')
assert.match(before, /id.*bigint/)
const client = readFileSync(join(directory, 'src/generated/client.ts'), 'utf8')
assert.ok(client.includes('this.transport.callGenerated('))
assert.ok(!client.includes('new $getMetricRequest'))
writeFileSync(join(directory, options.schema), Buffer.from([0xff]))
await assert.rejects(generateSDK(options, directory), AggregateError)
assert.equal(readFileSync(output, 'utf8'), before)
console.log('standalone SDK verified')
`

it.each(['commonjs', 'module'])('rejects corrupt network/files before replacing generated output: %s', async moduleType => {
  mkdirSync(temporaryRoot, { recursive: true })
  const directory = mkdtempSync(join(temporaryRoot, 'vite-schema-'))
  try {
    writeFixture(directory)
    const result = await runFile(process.execPath, ['--input-type=module', '-e', verifySchemaBoundary, directory, moduleType], {
      cwd: consumerRoot, timeout: 60000,
    })
    expect(result.stdout).toContain('schema byte boundaries verified')
  } finally {
    if (directory.startsWith(temporaryRoot + sep)) rmSync(directory, { recursive: true })
  }
}, 65000)

const verifySchemaBoundary = `
import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
import { createServer } from 'node:http'
import { existsSync, readFileSync, readdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'

const require = createRequire(import.meta.url)
const directory = process.argv[1]
const { luxo } = process.argv[2] === 'commonjs' ? require('@luxojs/vite-plugin') : await import('@luxojs/vite-plugin')
const schemaFile = join(directory, 'schema/luxo.schema.json')
const schema = JSON.parse(readFileSync(schemaFile, 'utf8'))
schema.models.Metric.description = '中文😀\uFEFF\uFFFD'
const valid = Buffer.from(JSON.stringify(schema))
const badUTF8 = [[0x80], [0xc0, 0xaf], [0xe2, 0x82], [0xed, 0xa0, 0x80], [0xf4, 0x90, 0x80, 0x80]].map(bytes =>
  Buffer.concat([Buffer.from('{"models":{},"apis":{},"description":"'), Buffer.from(bytes), Buffer.from('"}')]))
const invalid = [...badUTF8, Buffer.from([0xef, 0xbb]), Buffer.concat([Buffer.from([0xef, 0xbb, 0xbf, 0xef, 0xbb, 0xbf]), valid]),
  Buffer.from(JSON.stringify(schema), 'utf16le'), Buffer.from('{'), Buffer.from('null'), Buffer.from('[]')]
let payload = valid
let status = 200
const requests = []
const server = createServer((request, response) => {
  requests.push({ url: request.url, key: request.headers['x-introspection-key'] })
  response.writeHead(status, { 'Content-Type': 'application/json; charset=iso-8859-1' })
  for (let i = 0; i < payload.length; i++) response.write(payload.subarray(i, i + 1))
  response.end()
})
await new Promise((resolve, reject) => {
  server.once('error', reject)
  server.listen(0, '127.0.0.1', resolve)
})
try {
  for (const source of ['file', 'network']) {
    const outDir = join(directory, 'checked-' + source)
    const options = source === 'file' ? { schema: schemaFile } : {
      endpoint: 'http://127.0.0.1:' + server.address().port + '/luvia?tenant=a%20b&%24schema=old#ignored', introspectionKey: 'test-key',
    }
    const plugin = luxo({ ...options, outDir })
    const setPayload = bytes => { payload = bytes; writeFileSync(schemaFile, bytes) }
    setPayload(invalid[0])
    await assert.rejects(plugin.buildStart(), AggregateError)
    assert.equal(existsSync(outDir), false)
    setPayload(valid)
    await plugin.buildStart()
    const files = readdirSync(outDir).sort()
    assert.deepEqual(files, ['client.ts', 'queries.ts', 'requests.ts', 'schema.ts', 'types.ts'])
    const before = files.map(file => readFileSync(join(outDir, file), 'utf8'))
    setPayload(Buffer.concat([Buffer.from([0xef, 0xbb, 0xbf]), valid]))
    await plugin.buildStart()
    assert.deepEqual(files.map(file => readFileSync(join(outDir, file), 'utf8')), before)
    for (const bytes of invalid) {
      setPayload(bytes)
      await assert.rejects(plugin.buildStart(), AggregateError)
      assert.deepEqual(files.map(file => readFileSync(join(outDir, file), 'utf8')), before)
      assert.equal(plugin.transform('async function f() { const m = await client.getMetric({id: 1n}); console.log(m.label) }', join(directory, 'src/view.ts')), null)
    }
    setPayload(valid)
    if (source === 'network') {
      status = 503
      await assert.rejects(plugin.buildStart(), AggregateError)
      assert.deepEqual(files.map(file => readFileSync(join(outDir, file), 'utf8')), before)
      status = 200
    }
    await plugin.buildStart()
  }
  for (const request of requests) {
    const url = new URL(request.url, 'http://localhost')
    assert.equal(url.searchParams.get('tenant'), 'a b')
    assert.deepEqual(url.searchParams.getAll('$schema'), [''])
    assert.equal(request.key, 'test-key')
  }
  console.log('schema byte boundaries verified')
} finally {
  server.closeAllConnections()
  await new Promise((resolve, reject) => server.close(error => error ? reject(error) : resolve()))
}
`

const consumerSource = `
import { LuxoClient } from './luxo/client.js'
import type { Metric } from './luxo/types.js'

export async function selectMetric(client: LuxoClient): Promise<bigint> {
  const metric = (await client.getMetric({ id: 9223372036854775807n })) satisfies Metric
  return metric.id
}

export async function selectLabel(client: LuxoClient): Promise<string> {
  const metric = await client.getMetric({ id: 9223372036854775807n })
  return metric.label
}

export async function selectExplicit(client: LuxoClient): Promise<bigint> {
  const metric = await client.getMetric({ id: 9223372036854775807n, $select: 'id, label' })
  return metric.id
}

export function createClient(endpoint: string): LuxoClient {
  return LuxoClient.create(endpoint)
}
`

const buildConsumer = `
import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { pathToFileURL } from 'node:url'
import { build } from 'vite'

const require = createRequire(import.meta.url)
const directory = process.argv[1]
const commonjs = process.argv[2] === 'commonjs'
const plugin = commonjs ? require('@luxojs/vite-plugin') : await import('@luxojs/vite-plugin')
assert.equal(plugin.default, plugin.luxo)
const { luxo } = plugin
const filename = commonjs ? 'main.cjs' : 'main.mjs'
await build({
  root: directory, configFile: false, logLevel: 'silent',
  plugins: [luxo({ schema: 'schema/luxo.schema.json' })],
  build: {
    minify: false, outDir: 'out', emptyOutDir: false,
    lib: { entry: join(directory, 'src/main.mts'), formats: [commonjs ? 'cjs' : 'es'], fileName: () => filename },
  },
})
assert.match(readFileSync(join(directory, 'src/luxo/types.ts'), 'utf8'), /id.*bigint/)
const location = join(directory, 'out', filename)
const { selectMetric, selectLabel, selectExplicit, createClient } = commonjs ? require(location) : await import(pathToFileURL(location).href)
const value = 9223372036854775807n
const selectionIdentities = new Map()
for (const [call, selection, response, expected] of [
  [selectMetric, 'id', { id: value }, value],
  [selectLabel, 'label', { label: 'only label' }, 'only label'],
  [selectExplicit, 'id, label', { id: value, label: 'explicit' }, value],
  [selectMetric, 'id', { id: value }, value],
]) {
  const actual = await call({ async getMetric(params) {
    const selected = params.$select
    const text = typeof selected === 'string' ? selected : selected.text
    assert.deepEqual({ ...params, $select: text }, { id: value, $select: selection })
    if (call !== selectExplicit) {
      assert.equal(Object.isFrozen(selected), true)
      if (selectionIdentities.has(call)) assert.equal(selected, selectionIdentities.get(call))
      selectionIdentities.set(call, selected)
    }
    return response
  } })
  assert.equal(actual, expected)
}
const { createServer } = await import('node:http')
const { decodeLuxoJSON } = await import('@luxojs/client')
const selections = []
const server = createServer(async (request, response) => {
  const parts = []
  for await (const part of request) parts.push(part)
  const body = decodeLuxoJSON(Buffer.concat(parts).toString())
  selections.push(body.$select)
  response.setHeader('content-type', 'application/json')
  response.end(body.$select === 'label' ? '{"data":{"label":"network"}}' : '{"data":{"id":9223372036854775807}}')
})
await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
const client = createClient('http://127.0.0.1:' + server.address().port)
client.setMode('json')
try {
  assert.equal(await selectMetric(client), value)
  assert.equal(await selectLabel(client), 'network')
  assert.deepEqual(selections, ['id', 'label'])
} finally {
  client.close()
  server.closeAllConnections()
  await new Promise(resolve => server.close(resolve))
}
console.log('selection and int64 verified')
`
