/** Bounded contiguous storage, independent of DATA frame/chunk count. */
export class HTTP2Body {
  private storage?: Uint8Array<ArrayBuffer>
  private length = 0

  constructor(private readonly limit: number, private readonly expected?: number) {}

  append(chunk: Uint8Array): void {
    const length = this.length + chunk.byteLength
    if (length > this.limit || (this.expected !== undefined && length > this.expected)) {
      throw new Error('HTTP/2 response exceeds its body limit or Content-Length')
    }
    if (chunk.byteLength === 0) return
    if (!this.storage || length > this.storage.byteLength) {
      const capacity = this.expected ?? Math.min(this.limit, Math.max(length, (this.storage?.byteLength ?? 0) * 2))
      const next = new Uint8Array(capacity)
      if (this.storage) next.set(this.storage.subarray(0, this.length))
      this.storage = next
    }
    this.storage.set(chunk, this.length)
    this.length = length
  }

  finish(): ArrayBuffer {
    if (this.expected !== undefined && this.length !== this.expected) throw new Error('incomplete HTTP/2 response body')
    if (!this.storage) return new ArrayBuffer(0)
    const result = this.length === this.storage.byteLength ? this.storage.buffer : this.storage.buffer.slice(0, this.length)
    this.storage = undefined
    return result
  }

  clear(): void { this.storage = undefined }
}
