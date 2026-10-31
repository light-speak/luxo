// @vitest-environment node
import { afterAll, beforeAll, expect, it } from 'vitest'
import { execFile } from 'node:child_process'
import { existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { dirname, join, resolve, sep } from 'node:path'
import { promisify } from 'node:util'
import type { LuxoSchema } from '@luxojs/client'
import { generateTypes } from '../../vite-plugin/src/codegen'

const executeFile = promisify(execFile)
const require = createRequire(import.meta.url)
const packageRoot = resolve(import.meta.dirname, '..')
const repositoryRoot = resolve(packageRoot, '../..')
const temporaryRoot = join(repositoryRoot, '.tmp')
let directory = ''

beforeAll(async () => {
  mkdirSync(temporaryRoot, { recursive: true })
  directory = mkdtempSync(join(temporaryRoot, 'react-package-consumer-'))
  for (const name of ['client', 'react']) {
    const cwd = join(repositoryRoot, 'packages', name)
    await runFile('pnpm', ['build'], { cwd, timeout: 60000 })
    await runFile('pnpm', ['pack', '--out', join(directory, `${name}.tgz`)], { cwd, timeout: 30000 })
  }
  prepareConsumer()
  await runFile('pnpm', ['install', '--offline', '--no-frozen-lockfile', '--ignore-scripts'], { cwd: directory, timeout: 60000 })
}, 180000)

afterAll(() => {
  if (directory.startsWith(temporaryRoot + sep)) rmSync(directory, { recursive: true })
})

it('publishes compiler-emitted declarations without a handwritten source fallback', () => {
  const installed = join(directory, 'node_modules/@luxojs/react')
  const manifest = JSON.parse(readFileSync(join(installed, 'package.json'), 'utf8')) as {
    types: string
    exports: { '.': { types: string } }
  }
  expect(manifest.types).toBe('dist/index.d.ts')
  expect(manifest.exports['.'].types).toBe('./dist/index.d.ts')
  expect(existsSync(join(installed, 'src'))).toBe(false)
  expect(readFileSync(join(installed, 'dist/index.d.ts'), 'utf8')).toContain('QueryClient')
})

it('publishes client runtime sources without tests or benchmark baselines', () => {
  const installed = join(directory, 'node_modules/@luxojs/client')
  const sources = readdirSync(join(installed, 'src'), { recursive: true, encoding: 'utf8' })
  expect(sources).toContain('index.ts')
  expect(sources).toContain('codec.ts')
  expect(sources.filter(name => /\.(test|bench)\.ts$/.test(name))).toEqual([])
})

it.each(['cts', 'mts'])('type-checks and executes a packed .%s consumer with TS 7', async extension => {
  const source = `consumer.${extension}`
  writeFileSync(join(directory, source), consumerSource)
  writeFileSync(join(directory, 'tsconfig.json'), JSON.stringify({
    compilerOptions: {
      strict: true, skipLibCheck: false, target: 'ES2022',
      module: 'NodeNext', moduleResolution: 'NodeNext', types: ['react'], outDir: 'out',
    }, files: [source],
  }))
  await runFile(join(directory, 'node_modules/.bin/tsc'), ['-p', directory], { timeout: 60000 })
  const result = await runFile(process.execPath, [join(directory, 'out', `consumer.${extension === 'cts' ? 'cjs' : 'mjs'}`)], {
    timeout: 15000,
  })
  expect(result.stdout).toContain('packed React int64 contract verified')
}, 80000)

it('compiles and loads documented React examples against the generated client', async () => {
  const schema: LuxoSchema = {
    models: { User: { name: 'User', fields: [
      { id: 1, name: 'id', type: 'Int' }, { id: 2, name: 'name', type: 'String' },
    ] } },
    apis: {
      getUser: { id: 1, name: 'getUser', module: 'fixture', returnType: 'User', params: [{ id: 1, name: 'id', type: 'Int' }] },
      listUsers: { id: 2, name: 'listUsers', module: 'fixture', returnType: 'User', returnList: true, paginated: true },
    },
  }
  await generateTypes(schema, join(directory, 'luxo'))
  const readme = readFileSync(join(packageRoot, 'README.md'), 'utf8')
  const files = ['Why Luxo + React?', 'Quick Start'].map((heading, index) => {
    const example = readme.split(`## ${heading}\n`)[1]?.match(/```tsx\n([\s\S]*?)```/)?.[1]
    if (!example) throw new Error(`Missing React README example: ${heading}`)
    const file = `readme-${index}.tsx`
    writeFileSync(join(directory, file), example)
    return file
  })
  writeFileSync(join(directory, 'tsconfig.json'), JSON.stringify({
    compilerOptions: {
      strict: true, skipLibCheck: false, target: 'ES2022', jsx: 'react-jsx',
      module: 'NodeNext', moduleResolution: 'NodeNext', types: ['react'], outDir: 'out',
    }, files,
  }))
  await runFile(join(directory, 'node_modules/.bin/tsc'), ['-p', directory], { timeout: 60000 })
  for (const file of files) {
    await runFile(process.execPath, [join(directory, 'out', file.replace('.tsx', '.js'))], { timeout: 15000 })
  }
}, 90000)

async function runFile(command: string, args: string[], options: { cwd?: string; timeout: number }) {
  try {
    return await executeFile(command, args, options)
  } catch (error) {
    if (error instanceof Error && 'stdout' in error) error.message += '\n' + String(error.stdout)
    throw error
  }
}

function prepareConsumer(): void {
  const dependencies: Record<string, string> = {
    '@luxojs/client': 'file:./client.tgz', '@luxojs/react': 'file:./react.tgz',
  }
  // Reuse installed third-party tooling, never Luxo source or workspace links.
  for (const name of ['react', '@types/react', 'typescript']) {
    dependencies[name] = `link:${dirname(require.resolve(`${name}/package.json`))}`
  }
  const compiler = JSON.parse(readFileSync(require.resolve('typescript/package.json'), 'utf8')) as { version: string }
  expect(compiler.version).toMatch(/^7\./)
  writeFileSync(join(directory, 'package.json'), JSON.stringify({ private: true, devDependencies: dependencies }))
}

const consumerSource = `
import { createElement, isValidElement } from 'react'
import { FetchTransport, int64FromJSON, decodeLuxoJSON } from '@luxojs/client'
import {
  QueryClient, QueryClientProvider, LuxoProvider, useLuxoQuery, useLuxoMutation,
  useQueryClient, useLuxoClient,
} from '@luxojs/react'
import type { QueryFunction, QueryKey, QueryState, UseLuxoQueryResult, UseLuxoMutationResult } from '@luxojs/react'
import type { Page, Selected, Transport } from '@luxojs/client'

type Metric = { id: bigint; nullable: bigint | null; unloaded: Selected<bigint> }
const maximum = 9223372036854775807n
const minimum = -9223372036854775808n
const queryKey: QueryKey = ['metric', maximum]
const metric: Metric = { id: maximum, nullable: null, unloaded: undefined }
const queryFn: QueryFunction<Metric> = async ({ signal }) => {
  if (signal.aborted) throw new Error('unexpected abort')
  return metric
}

function equal(actual: unknown, expected: unknown): void {
  if (actual !== expected) throw new Error(String(actual) + ' != ' + String(expected))
}

function Component() {
  const query: UseLuxoQueryResult<Metric> = useLuxoQuery(queryFn, [], { queryKey })
  const mutation: UseLuxoMutationResult<bigint, bigint> = useLuxoMutation(async (id: bigint) => id)
  const client: QueryClient = useQueryClient()
  const transport: Transport = useLuxoClient()
  if (query.data) {
    const id: bigint = query.data.id
    // @ts-expect-error Luxo integers never become number through hook inference.
    const lossy: number = query.data.id
    void [id, lossy]
  }
  // @ts-expect-error Mutation variables preserve the bigint input contract.
  void mutation.mutate(1)
  void [client, transport]
  return createElement('span', null, query.data?.id.toString())
}

async function main(): Promise<void> {
  const client = new QueryClient({ staleTime: Infinity, gcTime: Infinity })
  equal((await client.fetchQuery({ queryKey, queryFn })).id, maximum)
  client.setQueryData(['metric', maximum - 1n], { ...metric, id: maximum - 1n })
  equal(client.getQueryData<Metric>(queryKey)?.id, maximum)
  equal(client.getQueryData<Metric>(['metric', maximum - 1n])?.id, maximum - 1n)
  client.setQueryData<Metric>(queryKey, previous => ({ ...metric, id: previous?.id ?? minimum, nullable: minimum }))
  const state: QueryState<Metric> = client.getQueryState<Metric>(queryKey)
  equal(state.data?.nullable, minimum)
  equal(state.data?.unloaded, undefined)
  const page: Page<Metric> = { items: [metric], total: maximum, page: 1n, pageSize: 20n }
  client.setQueryData(['page'], page)
  equal(client.getQueryData<Page<Metric>>(['page'])?.total, maximum)
  equal(int64FromJSON(decodeLuxoJSON('9223372036854775807', true)), maximum)
  const transport = new FetchTransport('http://127.0.0.1:1/not-called')
  equal(isValidElement(createElement(QueryClientProvider, { client, children: null })), true)
  equal(isValidElement(createElement(LuxoProvider, { transport, queryClient: client, children: createElement(Component) })), true)
  client.clear()
  console.log('packed React int64 contract verified')
}
void main().catch(error => { throw error })
`
