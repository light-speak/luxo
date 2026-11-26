import { deepStrictEqual, ok, rejects } from 'node:assert/strict'
import { pathToFileURL } from 'node:url'
import { getHeapStatistics } from 'node:v8'

const [phase, entry, mode, scenario] = process.argv.slice(2)
ok(['timeout-late-refresh', 'skip-401'].includes(phase))
ok(['json', 'binary'].includes(mode))
deepStrictEqual(scenario, phase)
deepStrictEqual(process.execArgv, [])
const module = await import(pathToFileURL(entry))
const { Encoder, FetchTransport, LuxoError } = module

const encode = new Encoder()
encode.writeFieldInt(1, 401n)
encode.writeFieldString(2, 'Unauthorized')
encode.writeFieldString(3, 'expired')
encode.writeEnd()
const encoder = new TextEncoder()
const errorBody = mode === 'json'
  ? encoder.encode('{"code":401,"error":"Unauthorized","message":"expired"}').buffer
  : new Uint8Array(encode.bytes()).buffer
const valueBody = mode === 'json' ? encoder.encode('{"data":42}').buffer : Uint8Array.of(42).buffer
const unauthorized = { status: 401, async arrayBuffer() { return errorBody } }
const success = { status: 200, async arrayBuffer() { return valueBody } }
const endpoint = 'http://fixture.invalid/luvia'

async function deadlineWitness() {
  const renewal = Promise.withResolvers()
  let serveSuccess = false, renewals = 0
  const headers = []
  const transport = new class extends FetchTransport {
    async executeRequest(_endpoint, request) {
      headers.push(request.headers.authorization)
      return serveSuccess ? success : unauthorized
    }
  }(endpoint, { mode, token: 'expired', timeout: 500, onTokenExpired() { renewals++; return renewal.promise } })
  transport.setSchema({ health: { id: 1 } })
  await rejects(transport.call('health'), error => error instanceof LuxoError && error.error === 'TimeoutError')
  deepStrictEqual(renewals, 1)
  deepStrictEqual(headers, ['Bearer expired'])
  renewal.resolve('late')
  await new Promise(resolve => setImmediate(resolve))
  serveSuccess = true
  deepStrictEqual(await transport.call('health'), mode === 'json' ? 42 : Uint8Array.of(42))
  deepStrictEqual(headers, ['Bearer expired', 'Bearer expired'])
  deepStrictEqual(renewals, 1)
  return { deadlineMs: 500, validatedLateTokenIgnored: true, counts: { sends: 2, renewals } }
}

async function measureSkippedUnauthorized() {
  let sends = 0, renewals = 0
  const transport = new class extends FetchTransport {
    async executeRequest(_endpoint, request) {
      deepStrictEqual(request.headers.authorization, 'Bearer expired')
      sends++
      return unauthorized
    }
  }(endpoint, { mode, token: 'expired', async onTokenExpired() { renewals++; return 'unexpected' } })
  transport.setSchema({ health: { id: 1 } })
  const options = { refreshOnUnauthorized: false }
  const operation = async () => {
    try {
      await transport.call('health', undefined, options)
    } catch (error) {
      if (error instanceof LuxoError && error.error === 'Unauthorized' &&
          error.code === 401 && error.message === 'expired') return
      throw error
    }
    throw new Error('expected the original structured unauthorized response')
  }
  const warmup = 32, operations = 128
  for (let i = 0; i < warmup; i++) await operation()
  const before = getHeapStatistics().total_allocated_bytes
  const startCpu = process.cpuUsage()
  const startWall = process.hrtime.bigint()
  for (let i = 0; i < operations; i++) await operation()
  const wallNs = Number(process.hrtime.bigint() - startWall)
  const cpu = process.cpuUsage(startCpu)
  const after = getHeapStatistics().total_allocated_bytes
  ok(Number.isSafeInteger(before) && Number.isSafeInteger(after) && before > 0 && after >= before)
  deepStrictEqual({ sends, renewals }, { sends: warmup + operations, renewals: 0 })
  return {
    warmup, operations, counts: { sends, renewals }, wallNs,
    userCpuUs: cpu.user, systemCpuUs: cpu.system, before, after, allocatedBytes: after - before,
    equivalentWorkload: false,
  }
}

const result = await (phase === 'timeout-late-refresh' ? deadlineWitness() : measureSkippedUnauthorized())
console.log(JSON.stringify({
  phase, scenario, entry, mode, pid: process.pid, executable: process.execPath,
  platform: process.platform, arch: process.arch, node: process.versions.node,
  v8: process.versions.v8, uv: process.versions.uv, validated: true, ...result,
}))
