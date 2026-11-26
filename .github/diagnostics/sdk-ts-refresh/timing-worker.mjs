import { deepStrictEqual, ok } from 'node:assert/strict'
import { pathToFileURL } from 'node:url'

const [phase, entry, mode, scenario] = process.argv.slice(2)
ok(['smoke', 'sample'].includes(phase))
ok(['json', 'binary'].includes(mode))
ok(['no-callback', 'configured', 'one-401'].includes(scenario))
deepStrictEqual(process.execArgv, [])
const module = await import(pathToFileURL(entry))

// Keep this block byte-identical to the established FetchTransport fixture.
const bytes = (text) => new TextEncoder().encode(text).buffer
const jsonUnauthorized = bytes('{"code":401,"error":"Unauthorized","message":"expired"}')
const jsonValue = bytes('{"data":42}')
const binaryValue = Uint8Array.of(42).buffer

function fixture(module, mode, callback, expectsRefresh) {
  const expires = new module.Encoder()
  expires.writeFieldInt(1, 401n)
  expires.writeFieldString(2, 'Unauthorized')
  expires.writeFieldString(3, 'expired')
  expires.writeEnd()
  const binaryUnauthorized = new Uint8Array(expires.bytes()).buffer
  const success = { status: 200, async arrayBuffer() { return mode === 'json' ? jsonValue : binaryValue } }
  const unauthorized = { status: 401, async arrayBuffer() { return mode === 'json' ? jsonUnauthorized : binaryUnauthorized } }
  let renewals = 0, sends = 0
  const renewal = callback ? async () => { renewals++; return 'fresh' } : undefined
  const transport = new class extends module.FetchTransport {
    async executeRequest(_endpoint, request) {
      sends++
      return request.headers.authorization === 'Bearer expired' ? unauthorized : success
    }
  }('http://fixture.invalid/luvia', { mode, token: 'fresh', onTokenExpired: renewal })
  transport.setSchema({ health: { id: 1 } })
  return {
    async operation() {
      if (expectsRefresh) transport.setToken('expired')
      return transport.call('health')
    },
    counts() { return { renewals, sends } },
  }
}

const expires = scenario === 'one-401'
const arm = fixture(module, mode, scenario !== 'no-callback', expires)
const value = mode === 'json' ? 42 : Uint8Array.of(42)
const work = operations => ({ renewals: expires ? operations : 0, sends: operations * (expires ? 2 : 1) })

deepStrictEqual(await arm.operation(), value)
deepStrictEqual(arm.counts(), work(1))

const meta = {
  phase, entry, mode, scenario, pid: process.pid, executable: process.execPath,
  platform: process.platform, arch: process.arch, node: process.versions.node,
  v8: process.versions.v8, uv: process.versions.uv,
}

if (phase === 'smoke') {
  console.log(JSON.stringify({ ...meta, validated: true, operations: 1, counts: arm.counts() }))
} else {
  const warmupBatches = 12
  const iterations = 2000
  let result
  const batch = async () => {
    for (let i = 0; i < iterations; i++) result = await arm.operation()
  }

  for (let i = 0; i < warmupBatches; i++) await batch()
  const cpuStart = process.cpuUsage()
  const wallStart = process.hrtime.bigint()
  await batch()
  const wallNs = Number(process.hrtime.bigint() - wallStart)
  const cpu = process.cpuUsage(cpuStart)
  deepStrictEqual(result, value)
  deepStrictEqual(arm.counts(), work(1 + (warmupBatches + 1) * iterations))
  console.log(JSON.stringify({
    ...meta, validated: true, counts: arm.counts(), warmupBatches, iterations, wallNs,
    userCpuUs: cpu.user, systemCpuUs: cpu.system,
  }))
}
