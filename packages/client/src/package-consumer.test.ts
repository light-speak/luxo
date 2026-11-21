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

it.each(['cjs', 'mjs'])('packed %s calls release encoded inputs before receiving a response', async extension => {
  const script = join(directory, `input-lifetime.${extension}`)
  const imports = extension === 'cjs' ? "const sdk = require('@luxojs/client')" : "import * as sdk from '@luxojs/client'"
  writeFileSync(script, imports + inputLifetimeConsumer)
  const output = await execute(process.execPath, ['--expose-gc', script], { timeout: 15000 })
  expect(output.trim()).toBe('input-lifetime-ok:24')
}, 60000)

const inputLifetimeConsumer = `
const immediate = () => new Promise(resolve => setImmediate(resolve))
let socket, finishHTTP
class Socket {
  static OPEN = 1
  readyState = 0
  constructor() { socket = this; queueMicrotask(() => { this.readyState = 1; this.onopen?.() }) }
  send() {}
  close() { this.readyState = 3; this.onclose?.() }
}
class HTTP extends sdk.FetchTransport {
  executeRequest() { return new Promise(resolve => { finishHTTP = resolve }) }
}
class Legacy extends sdk.GeneratedRequest {
  apiID = 7
  constructor(blob) { super(); this.blob = blob }
  writeParameters(encoder) { encoder.writeVarint(1); encoder.writeBytes(this.blob) }
  toJSONParameters() { return { blob: this.blob } }
}
async function begin(channel, mode, entry, observed) {
  const options = { mode, observer: observed ? () => {} : undefined }
  const transport = channel === 'ws' ? new sdk.WsTransport('ws://example.invalid', options) : new HTTP('https://example.invalid', options)
  transport.setSchema({ live: { id: 7, params: [{ fieldID: 1, name: 'blob', type: 'Bytes' }] } })
  const blob = new Uint8Array(256 * 1024), params = entry === 'legacy' ? new Legacy(blob) : { blob }
  const codec = { apiID: 7, initialCapacity: () => 1024,
    writeParameters(encoder, values) { encoder.writeVarint(1); encoder.writeBytes(values.blob) } }
  const refs = [new WeakRef(params), new WeakRef(blob)]
  if (entry === 'static') refs.push(new WeakRef(codec))
  const pending = entry === 'static' ? transport.callGenerated('live', codec, params) : transport.call('live', params)
  await immediate()
  return { transport, pending, refs, strong: new Uint8Array(32) }
}
async function collect() {
  for (let i = 0; i < 8; i++) { await immediate(); globalThis.gc(); await immediate() }
}
async function main() {
  globalThis.WebSocket = Socket
  const failures = []
  let count = 0
  for (const observed of [false, true]) for (const channel of ['ws', 'http'])
  for (const mode of ['json', 'binary']) for (const entry of ['record', 'static', 'legacy']) {
    const state = await begin(channel, mode, entry, observed)
    const control = new WeakRef(state.strong)
    try {
      await collect()
      if (state.refs.some(ref => ref.deref() !== undefined)) failures.push([channel, mode, entry, observed].join('/'))
      if (control.deref() !== state.strong) throw new Error('strong reference control failed')
    } finally {
      if (channel === 'ws') socket.onmessage?.({ data: mode === 'json' ? '{"$id":1,"data":42}' : Uint8Array.of(2, 1, 84).buffer })
      else {
        const bytes = mode === 'json' ? new TextEncoder().encode('{"data":42}') : Uint8Array.of(84)
        finishHTTP({ status: 200, arrayBuffer: async () => bytes.buffer })
      }
      try {
        const value = await state.pending
        if (mode === 'json' ? value !== 42 : !(value instanceof Uint8Array) || value[0] !== 84) throw new Error('response changed')
      } finally { state.transport.close?.() }
    }
    count++
  }
  if (failures.length) throw new Error('retained encoded inputs: ' + failures.join(', '))
  console.log('input-lifetime-ok:' + count)
}
main().catch(error => { console.error(error); process.exitCode = 1 })
`

it('packed Binary subscriptions release unused JSON metadata while remaining active', async () => {
  const script = join(directory, 'binary-lifecycle.cjs')
  writeFileSync(script, `
const assert = require('node:assert/strict')
const { setImmediate: immediate } = require('node:timers/promises')
const { WsTransport } = require('@luxojs/client')
class Socket {
  static OPEN = 1
  readyState = 0
  constructor() { queueMicrotask(() => { this.readyState = 1; this.onopen?.() }) }
  send(message) {
    if (new Uint8Array(message)[0] === 0x10) this.onmessage?.({ data: Uint8Array.of(0x13, 1).buffer })
  }
  close() { this.readyState = 3; this.onclose?.() }
}
global.WebSocket = Socket
async function active() {
  const transport = new WsTransport('ws://example.invalid', { mode: 'binary' })
  transport.setSchema({ read: { id: 1, response: { type: 'Model', typeName: 'Node' },
    types: { Node: { next: { fieldID: 1, type: 'Model', typeName: 'Node' } } } } })
  const plan = new WeakRef(transport.jsonParams.response('read'))
  const stop = await transport.subscribe('read', {}, x => x, () => {})
  transport.setSchema({})
  return { transport, plan, stop }
}
async function main() {
  const state = await active()
  try {
    let released = false
    for (let i = 0; i < 12; i++) {
      await immediate(); global.gc(); await immediate()
      if (state.plan.deref() === undefined) { released = true; break }
    }
    assert.equal(released, true, 'active Binary stream retained unused JSON metadata')
    assert.equal(state.transport.subscriptions.size, 1)
  } finally { state.stop(); state.transport.close() }
  console.log('binary-metadata-released')
}
main().catch(error => { console.error(error); process.exitCode = 1 })
`)
  const output = await execute(process.execPath, ['--expose-gc', script], { timeout: 15000 })
  expect(output.trim()).toBe('binary-metadata-released')
}, 60000)

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
  console.log('node-entry-ok')
}
void check()
`)
  await compile(`node.${extension}`, 'NodeNext', `node-${extension}`)
  const output = await execute(process.execPath, [join(directory, `node-${extension}`, `node.${extension === 'cts' ? 'cjs' : 'mjs'}`)], { timeout: 15000 })
  expect(output.trim()).toBe('node-entry-ok')
}, 60000)

it.each(['cts', 'mts'])('packed .%s entry retires failures with unreadable decoder messages', async extension => {
  writeFileSync(join(directory, `diagnostic.${extension}`), diagnosticConsumer)
  await compile(`diagnostic.${extension}`, 'NodeNext', `diagnostic-${extension}`)
  const output = await execute(process.execPath, [join(directory, `diagnostic-${extension}`, `diagnostic.${extension === 'cts' ? 'cjs' : 'mjs'}`)], { timeout: 15000 })
  expect(output.trim()).toBe('decoder-diagnostic-ok:4')
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
    Response, AbortController, DOMException, TextEncoder, TextDecoder, performance, setTimeout, clearTimeout,
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

async function execute(command: string, args: string[], options: ExecFileOptions = {}): Promise<string> {
  try {
    return (await executeFile(command, args, { ...options, encoding: 'utf8' })).stdout
  } catch (error) {
    if (error instanceof Error && 'stdout' in error && typeof error.stdout === 'string') {
      throw new Error(`${error.message}\n${error.stdout}`, { cause: error })
    }
    throw error
  }
}

const diagnosticConsumer = `
import { WsTransport, decodeLuxoJSON } from '@luxojs/client'
import type { TransportMode } from '@luxojs/client'

class Socket {
  static readonly CONNECTING = 0
  static readonly OPEN = 1
  static readonly CLOSING = 2
  static readonly CLOSED = 3
  static latest: Socket
  readyState = 0
  binaryType = ''
  cancellations = 0
  onopen: (() => void) | null = null
  onclose: (() => void) | null = null
  onmessage: ((event: { data: string | ArrayBuffer }) => void) | null = null
  constructor() {
    Socket.latest = this
    queueMicrotask(() => { this.readyState = 1; this.onopen?.() })
  }
  send(body: string | ArrayBuffer): void {
    let type: number, id: number
    if (typeof body === 'string') {
      const fields = decodeLuxoJSON(body) as Record<string, unknown>
      if ('$unsub' in fields) { this.cancellations++; return }
      type = '$sub' in fields ? 0x10 : 1
      if (typeof fields.$id !== 'number') throw new Error('invalid control ID')
      id = fields.$id
    } else {
      const bytes = new Uint8Array(body)
      type = bytes[0]; id = bytes[1]
      if (type === 0x11) { this.cancellations++; return }
    }
    if (id < 1 || id > 127) throw new Error('expected a small fixture ID')
    const subscription = type === 0x10
    this.receive(typeof body === 'string'
      ? subscription ? '{"$sub":' + id + ',"ok":true}' : '{"$id":' + id + ',"data":7}'
      : Uint8Array.from(subscription ? [0x13, id] : [2, id, 14]).buffer)
  }
  receive(data: string | ArrayBuffer): void { this.onmessage?.({ data }) }
  close(): void { this.readyState = 3; this.onclose?.() }
}

async function check(mode: TransportMode, fault: 'getter' | 'object'): Promise<void> {
  const transport = new WsTransport('ws://localhost/audit', { mode })
  transport.setSchema({ watch: { id: 7 } })
  let errors = 0, peers = 0, decodes = 0
  const cause = new Error()
  Object.defineProperty(cause, 'message', { get() {
    if (fault === 'getter') throw new Error('unreadable message')
    return { toString() { throw new Error('unsafe message conversion') } }
  } })
  const fail = (): never => { throw new Error('damaged subscription delivered data or completion') }
  try {
    const stop = await transport.subscribe('watch', {}, () => { decodes++; throw cause }, fail, {
      onComplete: fail,
      onError: error => {
        if (error.error !== 'DecodeError' || error.message !== 'invalid subscription response') throw error
        errors++
      },
    })
    const peer = await transport.subscribe('watch', {}, value => value, () => { peers++ })
    const socket = Socket.latest
    const data = (id: number) => socket.receive(mode === 'json'
      ? '{"$stream":' + id + ',"data":7}' : Uint8Array.from([0x12, id, 14]).buffer)
    data(1); data(1); stop(); stop()
    socket.receive(mode === 'json' ? '{"$end":1,"ok":true}' : Uint8Array.from([0x15, 1]).buffer)
    data(2)
    const value = await transport.call('watch')
    if (mode === 'json' ? value !== 7 : !(value instanceof Uint8Array) || value[0] !== 14) throw new Error('peer call failed')
    const cancellations = socket.cancellations
    if (errors !== 1 || decodes !== 1 || peers !== 1 || cancellations !== 1) throw new Error('retirement mismatch')
    peer()
    if (socket.cancellations !== 2) throw new Error('peer cancellation missing')
  } finally { transport.close() }
}

async function main(): Promise<void> {
  const original = globalThis.WebSocket
  globalThis.WebSocket = Socket as unknown as typeof WebSocket
  let cases = 0
  try {
    for (const mode of ['json', 'binary'] as const) {
      for (const fault of ['getter', 'object'] as const) { await check(mode, fault); cases++ }
    }
  } finally { globalThis.WebSocket = original }
  console.log('decoder-diagnostic-ok:' + cases)
}
void main()
`
