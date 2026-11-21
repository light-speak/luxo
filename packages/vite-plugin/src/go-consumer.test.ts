import { expect, it } from 'vitest'
import { execFile, spawn } from 'node:child_process'
import { once } from 'node:events'
import { mkdirSync, mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync, readdirSync, copyFileSync } from 'node:fs'
import { basename, join, resolve, sep } from 'node:path'
import { createInterface } from 'node:readline'
import { createConnection, createServer } from 'node:net'
import type { Socket } from 'node:net'
import { promisify } from 'node:util'
import type { LuxoSchema } from '@luxojs/client'
import { generateTypes } from './codegen'
import { CallIdentity } from './call-identity'
import { analyzeVerifiedCalls } from './analyzer'

const runFile = promisify(execFile)
type GoConsumerScenario = 'complete' | 'enum-upgrade' | 'renewal' | 'decimals' | 'json-fields' | 'input-types'

it.each(['canonical', 'model'] as const)('generated closed sealed %s client compiles and executes with verified automatic case selection', async scenario => {
  const root = resolve(import.meta.dirname, '../../..')
  const scratch = join(root, '.tmp')
  mkdirSync(scratch, { recursive: true })
  const output = mkdtempSync(join(scratch, 'ts-sealed-types-'))
  let owner: CallIdentity | undefined
  try {
    const fixture = JSON.parse(readFileSync(scenario === 'model' ? join(import.meta.dirname, 'fixtures/sealed-model.json') : join(root, 'pkg/lux/codec/testdata/sealed-v1.json'), 'utf8')) as { schema: LuxoSchema; vectors?: { outputBytes: number[]; outputJSON: unknown }[] }
    await generateTypes(fixture.schema, output)
    mkdirSync(join(output, 'node_modules/@luxojs'), { recursive: true })
    symlinkSync(join(root, 'packages/client'), join(output, 'node_modules/@luxojs/client'), 'dir')
    writeFileSync(join(output, 'package.json'), JSON.stringify({ type: 'module' }))
    writeFileSync(join(output, 'tsconfig.json'), JSON.stringify({ compilerOptions: {
      target: 'ES2022', lib: ['ES2025', 'DOM'], module: 'NodeNext', moduleResolution: 'NodeNext', strict: true, outDir: 'out', types: [], sourceMap: !!process.env.LUXO_SDK_COVERAGE, inlineSources: !!process.env.LUXO_SDK_COVERAGE,
    }, include: ['*.ts'] }))
    const raw = [
      [1,4,0,1,14,0], [2,10,6,1,6,102,97,105,108,101,100,0], [3,2,0,0], [4,6,0,1,0,2,0,0],
      [4,19,0,1,1,6,1,4,0,1,14,0,2,1,1,4,3,2,0,0,0], [1,2,0,0], [0], [0],
      ...Array.from({length:4},()=>[1,4,0,1,14,0]),
    ]
    const json = [
      {Success:{value:7}}, {Failure:{reason:'failed'}}, {Pending:{}}, {Nested:{child:null,history:null}},
      {Nested:{child:{Success:{value:7}},history:[{Pending:{}}]}}, {Success:{}}, null, [],
      ...Array.from({length:4},()=>({Success:{value:7}})),
    ]
    const driver = process.env.LUXO_SEALED_HTTP_CONSUMER
    const responses = scenario === 'model' ? fixture.vectors! : undefined
    const prefix = driver ? 'const originalFetch=globalThis.fetch;\n' : `const originalFetch=globalThis.fetch; const raw=${JSON.stringify(responses?.map(item => item.outputBytes) ?? raw)}, json=${JSON.stringify(responses?.map(item => item.outputJSON) ?? json)}; let sent=0;
globalThis.fetch=async (_url, init) => { const index=sent++%${scenario === 'model' ? 3 : 12};
 return new Response(typeof init?.body==='string' ? JSON.stringify({data:json[index]}) : Uint8Array.from(raw[index])); };
`
    const file = join(output, 'consumer.ts')
    const source = prefix + readFileSync(join(import.meta.dirname, scenario === 'model' ? 'fixtures/sealed-model-consumer.ts.txt' : 'fixtures/sealed-consumer.ts.txt'), 'utf8')
    writeFileSync(file, source)
    owner = new CallIdentity(output, join(output, 'client.ts'))
    const transformed = await analyzeVerifiedCalls(source, file, fixture.schema, calls => owner!.identify(file, source, calls))
    if (scenario === 'model') expect(transformed).toContain('Owner{person{id},people{name}}')
    else {
      expect(transformed).toContain('Success{value}')
      expect(transformed).toContain('Failure{reason}')
      expect(transformed).toContain('child{')
      expect(transformed).toContain('history{')
    }
    await owner.close(); owner = undefined
    writeFileSync(file, transformed ?? source)
    await runFile(join(root, 'packages/vite-plugin/node_modules/.bin/tsc'), ['-p', output], { timeout: 60000 }).catch((error: unknown) => {
      const output = error as { stdout?: unknown; stderr?: unknown }
      throw new Error(`${String(error)}\n${String(output.stdout)}\n${String(output.stderr)}`)
    })
    const preload = join(output, 'go-http.mjs')
    if (driver) writeFileSync(preload, `import {writeFileSync,appendFileSync} from 'node:fs';
import {execFileSync} from 'node:child_process';
const input=${JSON.stringify(join(output, 'request.jsonl'))};
globalThis.fetch=async (_url,init)=>{
 const request=typeof init.body==='string'?{mode:'json',request:JSON.parse(init.body)}:{mode:'binary',bytes:[...new Uint8Array(init.body.buffer,init.body.byteOffset,init.body.byteLength)]};
 writeFileSync(input,JSON.stringify(request)+'\\n');
 const result=JSON.parse(execFileSync(${JSON.stringify(driver)},[input],{encoding:'utf8'}));
 if(process.env.LUXO_SEALED_CAPTURE)appendFileSync(process.env.LUXO_SEALED_CAPTURE,JSON.stringify({request,response:result})+'\\n');
 if(result.status!==200)throw Error('Go handler rejected SDK input: '+JSON.stringify(result));
 return new Response(request.mode==='json'?JSON.stringify(result.json):Uint8Array.from(result.bytes),{status:result.status});
};`)
    const result = await runFile(process.execPath, [...(driver ? ['--import', preload] : []), join(output, 'out/consumer.js')], { timeout: 30000, env: generatedCoverageEnvironment() })
    expect(result.stdout).toContain('sealed typed client json passed')
    expect(result.stdout).toContain('sealed typed client binary passed')
  } finally {
    await owner?.close()
    persistTypedCoverage(output)
    if (!output.startsWith(scratch + sep + 'ts-sealed-types-')) throw new Error('Unsafe sealed fixture cleanup')
    rmSync(output, { recursive: true, force: true })
  }
}, 120000)

it('generated closed generic client compiles with verified automatic selection', async () => {
  const schemaFile = process.env.LUXO_GENERIC_SCHEMA
  const driver = process.env.LUXO_GENERIC_HTTP_CONSUMER
  const root = resolve(import.meta.dirname, '../../..'), scratch = join(root, '.tmp')
  const output = mkdtempSync(join(scratch, 'ts-generic-types-'))
  let owner: CallIdentity | undefined
  try {
    const schema = JSON.parse(readFileSync(schemaFile ?? join(import.meta.dirname, 'fixtures/closed-generics.json'), 'utf8')) as LuxoSchema
    await generateTypes(schema, output)
    mkdirSync(join(output, 'node_modules/@luxojs'), { recursive: true })
    symlinkSync(join(root, 'packages/client'), join(output, 'node_modules/@luxojs/client'), 'dir')
    writeFileSync(join(output, 'package.json'), JSON.stringify({ type: 'module' }))
    writeFileSync(join(output, 'tsconfig.json'), JSON.stringify({ compilerOptions: {
      target: 'ES2022', lib: ['ES2025', 'DOM'], module: 'NodeNext', moduleResolution: 'NodeNext', strict: true, outDir: 'out', types: [], sourceMap: !!process.env.LUXO_SDK_COVERAGE, inlineSources: !!process.env.LUXO_SDK_COVERAGE,
    }, include: ['*.ts'] }))
    const file = join(output, 'consumer.ts')
    const source = `const genericModes = ${JSON.stringify(driver ? ['json','binary'] : ['json'])} as const;\n` + readFileSync(join(import.meta.dirname, 'fixtures/generic-consumer.ts.txt'), 'utf8')
    writeFileSync(file, source)
    owner = new CallIdentity(output, join(output, 'client.ts'))
    const transformed = await analyzeVerifiedCalls(source, file, schema, calls => owner!.identify(file, source, calls))
    expect(transformed).toContain('items{value{value}}')
    await owner.close(); owner = undefined
    writeFileSync(file, transformed ?? source)
    const preload = join(output, 'go-http.mjs')
    writeFileSync(preload, driver ? `import {writeFileSync,appendFileSync} from 'node:fs';
import {execFileSync} from 'node:child_process';
const input=${JSON.stringify(join(output, 'request.jsonl'))};
globalThis.fetch=async (_url,init)=>{
 const request=typeof init.body==='string'?{mode:'json',request:JSON.parse(init.body)}:{mode:'binary',bytes:[...new Uint8Array(init.body.buffer,init.body.byteOffset,init.body.byteLength)]};
 writeFileSync(input,JSON.stringify(request)+'\\n');
 const result=JSON.parse(execFileSync(${JSON.stringify(driver)},[input],{encoding:'utf8'}));
 if(process.env.LUXO_GENERIC_CAPTURE)appendFileSync(process.env.LUXO_GENERIC_CAPTURE,JSON.stringify({request,response:result})+'\\n');
 if(result.status!==200)throw Error('Go handler rejected SDK input: '+JSON.stringify(result));
 return new Response(request.mode==='json'?JSON.stringify(result.json):Uint8Array.from(result.bytes),{status:result.status});
};` : `globalThis.fetch=async (_url,init)=>{
 const request=JSON.parse(init.body);
 const data=request.$api==='readPage'?{items:[{value:{value:42}}],total:1,page:1,pageSize:20}
  :request.$api==='readCursor'?{items:[{value:{value:43}}],nextCursor:'next',hasMore:true}:request.value;
 return new Response(JSON.stringify({data}));
};`)
    await runFile(join(root, 'packages/vite-plugin/node_modules/.bin/tsc'), ['-p', output], { timeout: 60000 }).catch((error: unknown) => {
      const output = error as { stdout?: unknown; stderr?: unknown }
      throw new Error(`${String(error)}\n${String(output.stdout)}\n${String(output.stderr)}`)
    })
    const result = await runFile(process.execPath, ['--import', preload, join(output, 'out/consumer.js')], { timeout: 30000, env: generatedCoverageEnvironment() })
    expect(result.stdout).toContain('closed generics json passed')
    if (driver) expect(result.stdout).toContain('closed generics binary passed')
  } finally {
    await owner?.close()
    persistTypedCoverage(output)
    if (!output.startsWith(scratch + sep + 'ts-generic-types-')) throw new Error('Unsafe generic fixture cleanup')
    rmSync(output, { recursive: true, force: true })
  }
}, 120000)

it.each(['commonjs', 'module'])('generated %s rejects invalid dynamic Binary inputs through Go transports', async moduleType => {
  const root = resolve(import.meta.dirname, '../../..')
  const scratch = join(root, '.tmp')
  mkdirSync(scratch, { recursive: true })
  const directory = mkdtempSync(join(scratch, 'ts-input-types-'))
  try {
    const binary = await buildGoFixture(root, directory)
    for (const gateway of [false, true]) {
      await withGoDeployment(binary, gateway, async endpoint => {
        const schema = await readGoSchema(endpoint)
        for (const staticRequests of [false, true]) {
          const output = join(directory, `consumer-${gateway}-${staticRequests}`)
          await generateTypes(schema, output, { staticRequests })
          await runGoConsumer(root, output, endpoint, moduleType, 'input-types')
        }
      })
    }
  } finally {
    if (!directory.startsWith(`${scratch}${sep}ts-input-types-`)) throw new Error('Unsafe fixture cleanup')
    rmSync(directory, { recursive: true, force: true })
  }
}, 180000)

it.each(['commonjs', 'module'])('generated %s requests preserve structured JSON null through real Go transports', async moduleType => {
  const root = resolve(import.meta.dirname, '../../..')
  const scratch = join(root, '.tmp')
  mkdirSync(scratch, { recursive: true })
  const directory = mkdtempSync(join(scratch, 'ts-json-field-'))
  try {
    const binary = await buildGoFixture(root, directory)
    for (const gateway of [false, true]) {
      await withGoDeployment(binary, gateway, async endpoint => {
        const schema = await readGoSchema(endpoint)
        expect(schema.types?.JSONSnapshot.fields).toEqual([{ id: 1, name: 'payload', type: 'JSON', typeName: 'JSON' }])
        for (const staticRequests of [false, true]) {
          const output = join(directory, `consumer-${gateway}-${staticRequests}`)
          await generateTypes(schema, output, { staticRequests })
          await runGoConsumer(root, output, endpoint, moduleType, 'json-fields')
          console.info(`JSON field interop ${moduleType} gateway=${gateway} static=${staticRequests} passed`)
        }
      })
    }
  } finally {
    if (!directory.startsWith(`${scratch}${sep}ts-json-field-`)) throw new Error('Unsafe fixture cleanup')
    rmSync(directory, { recursive: true, force: true })
  }
}, 180000)

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

it.each(['commonjs', 'module'])('TS 7 generated %s WS client renews expired identity through a generated gateway and TCP RPC', async moduleType => {
  await verifyGoConsumer(moduleType, true, undefined, 'renewal')
}, 180000)

it.each(['commonjs', 'module'])('generated %s Decimal arrays preserve compact exponents through gateway and TCP RPC', async moduleType => {
  await verifyGoConsumer(moduleType, true, undefined, 'decimals')
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
        await runGoConsumer(root, output, endpoint, moduleType, 'enum-upgrade')
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

async function verifyGoConsumer(moduleType: string, gateway: boolean, staticRequests?: boolean, scenario: GoConsumerScenario = 'complete'): Promise<void> {
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
      await runGoConsumer(root, output, endpoint, moduleType, scenario)
    })
  } finally {
    if (!directory.startsWith(`${scratch}${sep}ts-go-interop-`)) throw new Error('Unsafe fixture cleanup')
    rmSync(directory, { recursive: true, force: true })
  }
}

async function runGoConsumer(root: string, output: string, endpoint: string, moduleType: string, scenario: GoConsumerScenario = 'complete'): Promise<void> {
  if (scenario === 'renewal' || scenario === 'decimals' || scenario === 'json-fields' || scenario === 'input-types') {
    prepareConsumer(root, output, endpoint, moduleType, endpoint, scenario)
    await runFile(join(root, 'packages/vite-plugin/node_modules/.bin/tsc'), ['-p', output], { timeout: 60000 })
    const result = await runFile(process.execPath, [join(output, 'out/consumer.js')], { timeout: 30000 })
    if (scenario === 'renewal') {
      for (const mode of ['json', 'binary']) expect(result.stdout).toContain(`generated ws renewal ${mode} passed`)
    } else if (scenario === 'input-types') {
      for (const channel of ['http', 'ws']) expect(result.stdout).toContain(`input types binary ${channel} passed`)
    } else {
      expectConsumerChannels(result.stdout)
    }
    return
  }
  await withDisconnectProxy(endpoint, async (proxy, disconnect) => {
    prepareConsumer(root, output, endpoint, moduleType, proxy, scenario)
    await runFile(join(root, 'packages/vite-plugin/node_modules/.bin/tsc'), ['-p', output], { timeout: 60000 })
    const pending = runFile(process.execPath, [join(output, 'out/consumer.js')], { timeout: 30000 })
    const lines = createInterface({ input: pending.child.stdout! })
    let disconnected = 0
    lines.on('line', line => {
      if (/^disconnect (json|binary) ready$/.test(line)) { disconnect(); disconnected++ }
    })
    try {
      const result = await pending
      expectConsumerChannels(result.stdout)
      for (const mode of ['json', 'binary']) {
        expect(result.stdout).toContain(`refresh ${mode} passed`)
        expect(result.stdout).toContain(`disconnect ${mode} passed`)
      }
      expect(disconnected).toBe(2)
    } finally { lines.close() }
  })
}

function expectConsumerChannels(output: string): void {
  for (const channel of ['http:', 'ws:']) {
    for (const mode of ['json', 'binary']) expect(output).toContain(`${channel} ${mode} passed`)
  }
}

async function withGoDeployment(binary: string, gateway: boolean, use: (endpoint: string) => Promise<void>): Promise<void> {
  if (!gateway) return withGoService(binary, use)
  await withGoService(binary, async backend => {
    await withGoService(binary, use, ['-gateway', new URL(backend).host])
  }, ['-rpc'], 'tcp:')
}

function prepareConsumer(root: string, output: string, endpoint: string, moduleType: string, disconnectEndpoint: string, scenario: GoConsumerScenario): void {
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
  const invocation = scenario === 'renewal' ? 'verifyGeneratedWSRenewal()'
    : scenario === 'input-types' ? 'verifyDynamicInputTypes()'
    : scenario === 'decimals' ? 'runInterop((client, websocket) => verifyScalarArrays(client, websocket, [4]), false)'
    : scenario === 'json-fields' ? 'runInterop(verifyJSONFields, false)'
    : `runInterop(${scenario === 'enum-upgrade' ? 'verifyEnumUpgrade' : 'verifyComplete'})`
  writeFileSync(join(output, 'consumer.ts'), `const endpoint = ${JSON.stringify(endpoint)}\nconst disconnectEndpoint = ${JSON.stringify(disconnectEndpoint)}\nconst integerCases = ${JSON.stringify(integers)}\nconst jsonNumberTokens = ${JSON.stringify(fixture.jsonNumberTokens)}\nconst dateTimeCases = ${JSON.stringify(fixture.dateTimeCases)}\nconst uuidCanonicalCases = ${JSON.stringify(fixture.uuidCanonicalCases)}\n${source}\nvoid ${invocation}.catch(error => { throw error })\n`)
  writeFileSync(join(output, 'package.json'), JSON.stringify({ type: moduleType }))
  writeFileSync(join(output, 'tsconfig.json'), JSON.stringify({ compilerOptions: {
    target: 'ES2022', lib: ['ES2025', 'DOM'], module: 'NodeNext', moduleResolution: 'NodeNext',
    strict: true, outDir: 'out', types: [], sourceMap: !!process.env.LUXO_SDK_COVERAGE, inlineSources: !!process.env.LUXO_SDK_COVERAGE,
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

function generatedCoverageEnvironment(): NodeJS.ProcessEnv | undefined {
  const output = process.env.LUXO_SDK_COVERAGE
  if (!output) return undefined
  const directory = join(output, 'node-generated')
  mkdirSync(directory, { recursive: true })
  return { ...process.env, NODE_V8_COVERAGE: directory }
}

function persistTypedCoverage(output: string): void {
  const root = process.env.LUXO_SDK_COVERAGE
  if (!root) return
  const directory = join(root, basename(output))
  mkdirSync(directory, { recursive: true })
  for (const file of readdirSync(output)) {
    if (file.endsWith('.ts') || file === 'tsconfig.json') copyFileSync(join(output, file), join(directory, file))
  }
  const compiled = join(output, 'out')
  mkdirSync(join(directory, 'out'), { recursive: true })
  for (const file of readdirSync(compiled)) {
    if (file.endsWith('.js') || file.endsWith('.map')) copyFileSync(join(compiled, file), join(directory, 'out', file))
  }
}
