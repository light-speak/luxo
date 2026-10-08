import { expect, it } from 'vitest'
import { execFile, spawn } from 'node:child_process'
import { once } from 'node:events'
import { mkdirSync, mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from 'node:fs'
import { join, resolve, sep } from 'node:path'
import { createInterface } from 'node:readline'
import { createConnection, createServer } from 'node:net'
import type { Socket } from 'node:net'
import { promisify } from 'node:util'
import type { LuxoSchema } from '@luxojs/client'
import { generateTypes } from './codegen'

const runFile = promisify(execFile)

it.each([
  ['commonjs', false], ['module', false], ['commonjs', true], ['module', true],
] as const)('explicit wrapper baseline %s gateway=%s interoperates with Go', async (moduleType, gateway) => {
  await verifyGoConsumer(moduleType, gateway, false)
}, 180000)

it.each(['commonjs', 'module'])('TS 7 generated %s client interoperates with the real Go router', async moduleType => {
  await verifyGoConsumer(moduleType, false)
}, 180000)

it.each(['commonjs', 'module'])('TS 7 generated %s client preserves numbers, errors and streams through a generated gateway and TCP RPC', async moduleType => {
  await verifyGoConsumer(moduleType, true)
}, 180000)

it.each(['commonjs', 'module'])('old %s SDK preserves new server enum members in both deployments', async moduleType => {
  const root = resolve(import.meta.dirname, '../../..')
  const scratch = join(root, '.tmp')
  mkdirSync(scratch, { recursive: true })
  const directory = mkdtempSync(join(scratch, 'ts-enum-upgrade-'))
  try {
    const oldBinary = await buildGoFixture(root, join(directory, 'old'))
    const nextBinary = await buildGoFixture(root, join(directory, 'next'), true)
    for (const gateway of [false, true]) {
      const output = join(directory, gateway ? 'gateway-consumer' : 'direct-consumer')
      let oldSchema: LuxoSchema | undefined
      await withGoDeployment(oldBinary, gateway, async endpoint => {
        oldSchema = await readGoSchema(endpoint)
        expect(oldSchema.enums?.FixtureState.values).toEqual(['OPEN', 'CLOSED'])
        await generateTypes(oldSchema, output)
      })
      const oldTypes = readFileSync(join(output, 'types.ts'), 'utf8')
      await withGoDeployment(nextBinary, gateway, async endpoint => {
        const nextSchema = await readGoSchema(endpoint)
        expect(nextSchema.enums?.FixtureState.values).toEqual(['OPEN', 'CLOSED', 'FUTURE'])
        if (!oldSchema) throw new Error('Missing original Schema')
        for (const [name, api] of Object.entries(oldSchema.apis)) {
          const next = nextSchema.apis[name]
          expect(next.id).toBe(api.id)
          expect(next.params).toEqual(name === 'echoInteger'
            ? [...(api.params ?? []), expect.objectContaining({ name: 'delta', hasDefault: true })]
            : api.params)
        }
        expect(nextSchema.apis.aFutureInteger).toBeDefined()
        await runGoConsumer(root, output, endpoint, moduleType, true)
      })
      expect(readFileSync(join(output, 'types.ts'), 'utf8')).toBe(oldTypes)
    }
  } finally {
    if (!directory.startsWith(`${scratch}${sep}ts-enum-upgrade-`)) throw new Error('Unsafe fixture cleanup')
    rmSync(directory, { recursive: true, force: true })
  }
}, 180000)

async function buildGoFixture(root: string, directory: string, futureEnum = false): Promise<string> {
  mkdirSync(directory, { recursive: true })
  await runFile('go', ['run', './tests/sdk-interop', '-out', directory, ...(futureEnum ? ['-future-enum'] : [])], { cwd: root, timeout: 120000 })
  const binary = join(directory, 'server')
  await runFile('go', ['build', '-o', binary, directory], { cwd: root, timeout: 120000 })
  return binary
}

async function readGoSchema(endpoint: string): Promise<LuxoSchema> {
  const response = await fetch(`${endpoint}?$schema`, {
    headers: { 'X-Introspection-Key': 'sdk-interop' }, signal: AbortSignal.timeout(10000),
  })
  expect(response.status).toBe(200)
  return await response.json() as LuxoSchema
}

async function verifyGoConsumer(moduleType: string, gateway: boolean, staticRequests?: boolean): Promise<void> {
  const root = resolve(import.meta.dirname, '../../..')
  const scratch = join(root, '.tmp')
  mkdirSync(scratch, { recursive: true })
  const directory = mkdtempSync(join(scratch, 'ts-go-interop-'))
  try {
    const binary = await buildGoFixture(root, directory)
    await withGoDeployment(binary, gateway, async endpoint => {
      const output = join(directory, 'consumer')
      const schema = await readGoSchema(endpoint)
      expect(schema.apis.echoInteger.params?.[0].typeName).toBe('Int')
      await generateTypes(schema, output, staticRequests === undefined ? undefined : { staticRequests })
      await runGoConsumer(root, output, endpoint, moduleType)
    })
  } finally {
    if (!directory.startsWith(`${scratch}${sep}ts-go-interop-`)) throw new Error('Unsafe fixture cleanup')
    rmSync(directory, { recursive: true, force: true })
  }
}

async function runGoConsumer(root: string, output: string, endpoint: string, moduleType: string, enumUpgrade = false): Promise<void> {
  await withDisconnectProxy(endpoint, async (proxy, disconnect) => {
    prepareConsumer(root, output, endpoint, moduleType, proxy, enumUpgrade)
    await runFile(join(root, 'packages/vite-plugin/node_modules/.bin/tsc'), ['-p', output], { timeout: 60000 })
    const pending = runFile(process.execPath, [join(output, 'out/consumer.js')], { timeout: 30000 })
    const lines = createInterface({ input: pending.child.stdout! })
    let disconnected = 0
    lines.on('line', line => {
      if (/^disconnect (json|binary) ready$/.test(line)) { disconnect(); disconnected++ }
    })
    try {
      const result = await pending
      for (const channel of ['http:', 'ws:']) {
        for (const mode of ['json', 'binary']) expect(result.stdout).toContain(`${channel} ${mode} passed`)
      }
      for (const mode of ['json', 'binary']) {
        expect(result.stdout).toContain(`refresh ${mode} passed`)
        expect(result.stdout).toContain(`disconnect ${mode} passed`)
      }
      expect(disconnected).toBe(2)
    } finally { lines.close() }
  })
}

async function withGoDeployment(binary: string, gateway: boolean, use: (endpoint: string) => Promise<void>): Promise<void> {
  if (!gateway) return withGoService(binary, use)
  await withGoService(binary, async backend => {
    await withGoService(binary, use, ['-gateway', new URL(backend).host])
  }, ['-rpc'], 'tcp:')
}

function prepareConsumer(root: string, output: string, endpoint: string, moduleType: string, disconnectEndpoint: string, enumUpgrade: boolean): void {
  const scope = join(output, 'node_modules/@luxojs')
  mkdirSync(scope, { recursive: true })
  symlinkSync(join(root, 'packages/client'), join(scope, 'client'), 'dir')
  const fixture = JSON.parse(readFileSync(join(root, 'pkg/lux/codec/testdata/protocol-v1.json'), 'utf8')) as {
    int64Cases: { value: string }[]
    jsonNumberTokens: string[]
    dateTimeCases: { input: string; seconds: string; canonical: string }[]
    uuidCanonicalCases: { input: string; canonical: string | null }[]
  }
  const integers = fixture.int64Cases.map(entry => entry.value)
  const source = readFileSync(join(import.meta.dirname, 'fixtures/go-consumer.ts.txt'), 'utf8')
  const assertions = enumUpgrade ? 'verifyEnumUpgrade' : 'verifyComplete'
  writeFileSync(join(output, 'consumer.ts'), `const endpoint = ${JSON.stringify(endpoint)}\nconst disconnectEndpoint = ${JSON.stringify(disconnectEndpoint)}\nconst integerCases = ${JSON.stringify(integers)}\nconst jsonNumberTokens = ${JSON.stringify(fixture.jsonNumberTokens)}\nconst dateTimeCases = ${JSON.stringify(fixture.dateTimeCases)}\nconst uuidCanonicalCases = ${JSON.stringify(fixture.uuidCanonicalCases)}\n${source}\nvoid runInterop(${assertions}).catch(error => { throw error })\n`)
  writeFileSync(join(output, 'package.json'), JSON.stringify({ type: moduleType }))
  writeFileSync(join(output, 'tsconfig.json'), JSON.stringify({ compilerOptions: {
    target: 'ES2022', lib: ['ES2025', 'DOM'], module: 'NodeNext', moduleResolution: 'NodeNext',
    strict: true, outDir: 'out', types: [],
  }, include: ['*.ts'] }))
}

async function withDisconnectProxy(endpoint: string, use: (endpoint: string, disconnect: () => void) => Promise<void>): Promise<void> {
  const upstream = new URL(endpoint), sockets = new Set<Socket>()
  const proxy = createServer(socket => {
    const target = createConnection({ host: '127.0.0.1', port: Number(upstream.port) })
    sockets.add(socket); sockets.add(target)
    socket.on('error', () => target.destroy())
    target.on('error', () => socket.destroy())
    socket.on('close', () => { sockets.delete(socket); target.destroy() })
    target.on('close', () => { sockets.delete(target); socket.destroy() })
    socket.pipe(target).pipe(socket)
  })
  const disconnect = () => { for (const socket of sockets) socket.destroy() }
  proxy.listen(0, '127.0.0.1')
  await once(proxy, 'listening')
  try {
    const address = proxy.address()
    if (!address || typeof address === 'string') throw new Error('Unexpected proxy endpoint')
    const publicEndpoint = new URL(upstream)
    publicEndpoint.port = String(address.port)
    await use(publicEndpoint.toString(), disconnect)
  } finally {
    disconnect()
    await new Promise<void>((resolve, reject) => proxy.close(error => { if (error) reject(error); else resolve() }))
  }
}

async function withGoService(binary: string, use: (endpoint: string) => Promise<void>, args: string[] = [], protocol = 'http:'): Promise<void> {
  const server = spawn(binary, args, { stdio: 'pipe' })
  const closed = once(server, 'close')
  const lines = createInterface({ input: server.stdout })
  let errors = ''
  server.stderr.setEncoding('utf8').on('data', (chunk: string) => { errors += chunk })
  try {
    const endpoint: unknown = (await once(lines, 'line', { signal: AbortSignal.timeout(10000) }))[0]
    if (typeof endpoint !== 'string' || new URL(endpoint).hostname !== '127.0.0.1' || new URL(endpoint).protocol !== protocol || !new URL(endpoint).port) {
      throw new Error('Unexpected fixture endpoint')
    }
    await use(endpoint)
  } finally {
    lines.close()
    server.stdin.end()
    const timeout = setTimeout(() => server.kill('SIGKILL'), 10000)
    try {
      const [code] = await closed
      expect(code, errors).toBe(0)
    } finally {
      clearTimeout(timeout)
    }
  }
}
