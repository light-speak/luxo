// @vitest-environment node
import { afterAll, beforeAll, expect, it } from 'vitest'
import { execFile } from 'node:child_process'
import type { ExecFileOptions } from 'node:child_process'
import { mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs'
import { join, resolve, sep } from 'node:path'
import { promisify } from 'node:util'
import { runInNewContext } from 'node:vm'

const executeFile = promisify(execFile)
const packageRoot = resolve(import.meta.dirname, '..')
const temporaryRoot = resolve(packageRoot, '../../.tmp')
let directory = ''

beforeAll(async () => {
  mkdirSync(temporaryRoot, { recursive: true })
  directory = mkdtempSync(join(temporaryRoot, 'client-package-consumer-'))
  await execute('pnpm', ['build'], { cwd: packageRoot, timeout: 60000 })
  const archive = join(directory, 'client.tgz')
  await execute('pnpm', ['pack', '--out', archive], { cwd: packageRoot, timeout: 30000 })
  const installed = join(directory, 'node_modules/@luxojs/client')
  mkdirSync(installed, { recursive: true })
  await execute('tar', ['-xzf', archive, '-C', installed, '--strip-components', '1'])
  writeFileSync(join(directory, 'package.json'), '{"private":true}')
}, 120000)

afterAll(() => {
  if (directory.startsWith(temporaryRoot + sep)) rmSync(directory, { recursive: true })
})

it.each(['cts', 'mts'])('packed Node .%s entry shares runtime identities with the root', async extension => {
  writeFileSync(join(directory, `node.${extension}`), `
import { FetchTransport, LuxoError } from '@luxojs/client'
import { NodeHTTP2Transport } from '@luxojs/client/node'
async function check() {
  const transport = new NodeHTTP2Transport('https://localhost', { mode: 'binary' })
  if (!(transport instanceof FetchTransport)) throw new Error('duplicate transport class')
  try {
    await transport.call('missing')
    throw new Error('missing Schema accepted')
  } catch (error) {
    if (!(error instanceof LuxoError) || error.error !== 'ConfigError') throw error
  } finally { transport.close() }
}
void check()
`)
  await compile(`node.${extension}`, 'NodeNext', `node-${extension}`)
  await execute(process.execPath, [join(directory, `node-${extension}`, `node.${extension === 'cts' ? 'cjs' : 'mjs'}`)], { timeout: 15000 })
}, 60000)

it('packed browser root compiles without Node types and bundles without Node globals', async () => {
  writeFileSync(join(directory, 'browser.ts'), `
import { FetchTransport } from '@luxojs/client'
export async function check() {
  globalThis.fetch = async (_input, init) => {
    if (init?.method !== 'POST' || !init.signal) throw new Error('invalid fetch request')
    return new Response(Uint8Array.of(7, 8))
  }
  const transport = new FetchTransport('https://localhost', { mode: 'binary' })
  transport.setSchema({ echo: { id: 1, params: [] } })
  const result = await transport.call('echo')
  if (!(result instanceof Uint8Array) || result.length !== 2 || result[1] !== 8) throw new Error('invalid fetch response')
  return true
}
`)
  await compile('browser.ts', 'Bundler', 'browser-types')
  writeFileSync(join(directory, 'bundle.config.mjs'), `export default {
    entry: [${JSON.stringify(join(directory, 'browser.ts'))}], platform: 'browser',
    format: 'iife', globalName: 'PackedClient', dts: false,
    outDir: ${JSON.stringify(join(directory, 'browser-bundle'))},
    outExtensions: () => ({ js: '.js' }), deps: { alwaysBundle: ['@luxojs/client'] }
  }`)
  await execute('pnpm', ['exec', 'tsdown', '--config', join(directory, 'bundle.config.mjs')], { cwd: packageRoot, timeout: 60000 })
  const output = join(directory, 'browser-bundle')
  const scripts = readdirSync(output, { recursive: true, encoding: 'utf8' }).filter(name => name.endsWith('.js'))
  expect(scripts).toHaveLength(1)
  const source = readFileSync(join(output, scripts[0]), 'utf8')
  const result: unknown = await runInNewContext(`${source}; PackedClient.check()`, {
    Response, AbortController, DOMException, TextEncoder, TextDecoder, setTimeout, clearTimeout,
  }, { timeout: 1000 })
  expect(result).toBe(true)
}, 90000)

async function compile(file: string, resolution: 'Bundler' | 'NodeNext', output: string): Promise<void> {
  const config = join(directory, `${output}.json`)
  writeFileSync(config, JSON.stringify({ compilerOptions: {
    strict: true, skipLibCheck: false, target: 'ES2022', types: [],
    lib: ['ES2025', 'DOM', 'DOM.Iterable'], moduleResolution: resolution,
    module: resolution === 'Bundler' ? 'ESNext' : 'NodeNext', outDir: output,
  }, files: [file] }))
  await execute('pnpm', ['exec', 'tsc', '-p', config], { cwd: packageRoot, timeout: 30000 })
}

async function execute(command: string, args: string[], options: ExecFileOptions = {}): Promise<void> {
  try {
    await executeFile(command, args, { ...options, encoding: 'utf8' })
  } catch (error) {
    if (error instanceof Error && 'stdout' in error && typeof error.stdout === 'string') {
      throw new Error(`${error.message}\n${error.stdout}`, { cause: error })
    }
    throw error
  }
}
