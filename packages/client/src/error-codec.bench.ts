import { describe, expect, test } from 'vitest'
import { createRequire } from 'node:module'
import { join } from 'node:path'
import type { LuxoError as WireError } from './error'

// Measure native TS-compiled modules, not different Vite module-runner wrappers.
// Compile src/error-codec.ts to CommonJS under .tmp and set LUXO_BENCH_MODULES.
const compiled = process.env.LUXO_BENCH_MODULES
if (!compiled) throw new Error('Compile CommonJS modules under .tmp and set LUXO_BENCH_MODULES')
const requireCompiled = createRequire(import.meta.url)
const codec = requireCompiled(join(compiled, 'codec.js')) as typeof import('./codec')
const errors = requireCompiled(join(compiled, 'error-codec.js')) as typeof import('./error-codec')
const integers = requireCompiled(join(compiled, 'int64.js')) as typeof import('./int64')
const json = requireCompiled(join(compiled, 'json.js')) as typeof import('./json')
const { LuxoError } = requireCompiled(join(compiled, 'error.js')) as typeof import('./error')
const { Decoder, Encoder } = codec
const { jsonResponseError, decodeBinaryError } = errors
const { int64ToSafeNumber } = integers
const { decodeJSONEnvelope, decodeLuxoJSON, encodeLuxoJSON, normalizeLuxoJSON } = json
const textDecoder = new TextDecoder()

for (const detailed of [false, true]) {
  const envelope = detailed ? {
    error: 'Conflict', code: 409, message: '发布失败', traceId: 'trace', cause: '版本冲突',
    data: { items: Array.from({ length: 16 }, (_, index) => ({
      id: 9223372036854775807n - BigInt(index), ratio: 1.5, name: '任务',
    })) },
  } : { error: 'Unauthorized', code: 401, message: 'expired' }
  const text = encodeLuxoJSON(envelope)
  const bytes = binaryEnvelope(envelope)

  describe(detailed ? 'detailed errors, 16 records' : 'minimal errors', () => {
    test('JSON error decoding', async ({ bench }) => {
      expect(fields(previousJSON(decodeJSONEnvelope(text), 502)))
        .toEqual(fields(jsonResponseError(decodeJSONEnvelope(text), 502)))
      const result = await bench.compare(
        bench('previous JSON defaults', () => batch(() => previousJSON(decodeJSONEnvelope(text), 502))),
        bench('strict JSON fields', () => batch(() => jsonResponseError(decodeJSONEnvelope(text), 502))),
        { time: 500 },
      )
      report(detailed ? 'JSON, 16 records' : 'JSON, minimal',
        result.get('previous JSON defaults'), result.get('strict JSON fields'))
    })

    test('Binary error decoding', async ({ bench }) => {
      expect(fields(previousBinary(bytes, 502))).toEqual(fields(decodeBinaryError(bytes, 502)))
      const result = await bench.compare(
        bench('previous Binary fields', () => batch(() => previousBinary(bytes, 502))),
        bench('strict Binary fields and UTF-8', () => batch(() => decodeBinaryError(bytes, 502))),
        { time: 500 },
      )
      report(detailed ? 'Binary, 16 records' : 'Binary, minimal',
        result.get('previous Binary fields'), result.get('strict Binary fields and UTF-8'))
    })
  })
}

function fields(error: WireError): Record<string, unknown> {
  return { error: error.error, code: error.code, message: error.message,
    traceId: error.traceId, cause: error.cause, data: error.data }
}

function binaryEnvelope(envelope: Record<string, unknown>): Uint8Array {
  const encoder = new Encoder()
  encoder.writeFieldInt(1, BigInt(envelope.code as number))
  encoder.writeFieldString(2, envelope.error as string)
  encoder.writeFieldString(3, envelope.message as string)
  if (envelope.traceId !== undefined) encoder.writeFieldString(4, envelope.traceId as string)
  if (envelope.data !== undefined) encoder.writeFieldString(5, encodeLuxoJSON(envelope.data))
  if (envelope.cause !== undefined) encoder.writeFieldString(6, envelope.cause as string)
  encoder.writeEnd()
  return encoder.bytes()
}

function batch(operation: () => WireError): number {
  let checksum = 0
  for (let index = 0; index < 1000; index++) {
    const error = operation()
    checksum += error.code + error.message.length + (error.traceId?.length ?? 0)
  }
  return checksum
}

function report(
  name: string,
  before: { latency: { p50: number; mean: number; rme: number } },
  after: { latency: { p50: number; mean: number; rme: number } },
): void {
  // Latency is ms per 1000-operation batch, so multiply by 1000 for ns/op.
  console.log(JSON.stringify({
    name,
    beforeMedianNs: before.latency.p50 * 1000, afterMedianNs: after.latency.p50 * 1000,
    beforeMeanNs: before.latency.mean * 1000, afterMeanNs: after.latency.mean * 1000,
    beforeRME: before.latency.rme, afterRME: after.latency.rme,
  }))
}

// Frozen pre-strict-check behavior, keeping the current lossless JSON/int64 helpers.
// These are not released-SHA baselines; only valid fixtures are compared.
function previousJSON(value: Record<string, unknown>, status: number): WireError {
  return new LuxoError(typeof value.error === 'string' ? value.error : 'Internal',
    typeof value.code === 'number' && Number.isSafeInteger(value.code) ? value.code : status,
    typeof value.message === 'string' ? value.message : '',
    typeof value.traceId === 'string' ? value.traceId : undefined,
    normalizeLuxoJSON(value.data), typeof value.cause === 'string' ? value.cause : undefined)
}

function previousBinary(data: Uint8Array, status: number): WireError {
  const dec = new Decoder(data)
  let code = status, name = 'Error', message = `HTTP ${status}`
  let traceId: string | undefined, cause: string | undefined, errorData: unknown
  let seen = 0, ended = false
  while (dec.remaining > 0) {
    if (!dec.nextField()) { ended = dec.error === null; break }
    switch (dec.fieldID) {
      case 1:
        try { code = int64ToSafeNumber(dec.readInt()) } catch {
          return previousParseError(status, 'invalid error code')
        }
        seen |= 1
        break
      case 2: name = dec.readString(); seen |= 2; break
      case 3: message = dec.readString(); seen |= 4; break
      case 4: traceId = dec.readString(); break
      case 5:
        try { errorData = decodeLuxoJSON(textDecoder.decode(dec.readBytes())) } catch {
          return previousParseError(status, 'invalid JSON data')
        }
        break
      case 6: cause = dec.readString(); break
      default: return previousParseError(status, `unknown binary error field ${dec.fieldID}`)
    }
  }
  if (dec.error !== null) return previousParseError(status, dec.error)
  if (!ended) return previousParseError(status, 'missing end marker')
  if (dec.remaining !== 0) return previousParseError(status, 'trailing bytes')
  if (seen !== 7) return previousParseError(status, 'missing required fields')
  return new LuxoError(name, code, message, traceId, errorData, cause)
}

function previousParseError(status: number, message: string): WireError {
  return new LuxoError('ParseError', status, `invalid binary error response: ${message}`)
}
