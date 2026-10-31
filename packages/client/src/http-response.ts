import { LuxoError } from './error'
import type { HTTPResponse } from './transport'

export function responseLimit(value = 2 * 1024 * 1024): number {
  if (!Number.isSafeInteger(value) || value <= 0 || value > 0x7fffffff) {
    throw new LuxoError('ConfigError', 0, 'invalid HTTP response byte limit')
  }
  return value
}

/** Native executors bound collection themselves; Fetch exposes its byte stream. */
export async function readHTTPBody(response: HTTPResponse, maximum: number): Promise<Uint8Array> {
  const body = response.body
  if (body === undefined) {
    const bytes = new Uint8Array(await response.arrayBuffer())
    checkSize(bytes.byteLength, maximum, response.status)
    return bytes
  }
  if (body === null) return new Uint8Array()
  const reader = body.getReader()
  try {
    const encoding = response.headers?.get('content-encoding')
    if (!encoding || encoding === 'identity') {
      checkSize(Number(response.headers?.get('content-length')), maximum, response.status)
    }
    return await collect(reader, maximum, response.status)
  } catch (error) {
    // Cancellation must not wait for a failing or stalled peer to finish its body.
    void reader.cancel(error).catch(() => {})
    throw error
  } finally {
    reader.releaseLock()
  }
}

async function collect(reader: ReadableStreamDefaultReader<Uint8Array>, maximum: number, status: number): Promise<Uint8Array> {
  let first: Uint8Array | undefined
  let chunks: Uint8Array[] | undefined
  let size = 0
  for (;;) {
    const { done, value } = await reader.read()
    if (done) break
    size += value.byteLength
    checkSize(size, maximum, status)
    if (!value.byteLength) continue
    if (!first) first = value
    else (chunks ??= [first]).push(value)
  }
  if (!chunks) {
    if (!first) return new Uint8Array()
    // Fetch may belong to another realm (iframe/VM); retain bytes without copying.
    return first instanceof Uint8Array ? first : localView(first)
  }
  const bytes = new Uint8Array(size)
  let offset = 0
  for (const chunk of chunks) { bytes.set(chunk, offset); offset += chunk.byteLength }
  return bytes
}

function checkSize(size: number, maximum: number, status: number): void {
  if (size > maximum) throw new LuxoError('ParseError', status, 'HTTP response body exceeds limit')
}

function localView(bytes: Uint8Array): Uint8Array {
  return new Uint8Array(bytes.buffer, bytes.byteOffset, bytes.byteLength)
}
