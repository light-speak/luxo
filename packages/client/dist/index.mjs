// src/error.ts
var LuxoError = class extends Error {
  constructor(error, code, message, traceId) {
    super(message);
    this.error = error;
    this.code = code;
    this.traceId = traceId;
    this.name = "LuxoError";
  }
  error;
  code;
  traceId;
};

// src/codec.ts
var textEncoder = new TextEncoder();
var textDecoder = new TextDecoder();
var INITIAL_BUF_SIZE = 1024;
var Encoder = class {
  buf;
  view;
  pos;
  constructor(initialSize = INITIAL_BUF_SIZE) {
    this.buf = new Uint8Array(initialSize);
    this.view = new DataView(this.buf.buffer);
    this.pos = 0;
  }
  /** Ensure at least `n` bytes available */
  grow(n) {
    if (this.pos + n <= this.buf.length) return;
    let newLen = this.buf.length;
    const needed = this.pos + n;
    while (newLen < needed) newLen <<= 1;
    const next = new Uint8Array(newLen);
    next.set(this.buf, 0);
    this.buf = next;
    this.view = new DataView(this.buf.buffer);
  }
  /** Write unsigned LEB128 varint */
  writeVarint(v) {
    if (v < 128) {
      this.grow(1);
      this.buf[this.pos++] = v;
      return;
    }
    this.grow(8);
    while (v >= 128) {
      this.buf[this.pos++] = v & 127 | 128;
      v = Math.floor(v / 128);
    }
    this.buf[this.pos++] = v;
  }
  /** Write signed zigzag-encoded varint (matches Go: (v<<1) ^ (v>>63)) */
  writeSvarint(v) {
    if (v >= 0) {
      this.writeVarint(v * 2);
    } else {
      this.writeVarint(-v * 2 - 1);
    }
  }
  /** Write float64 as 8 bytes little-endian */
  writeFixed64(v) {
    this.grow(8);
    this.view.setFloat64(this.pos, v, true);
    this.pos += 8;
  }
  /** Write length-prefixed UTF-8 string */
  writeString(v) {
    const encoded = textEncoder.encode(v);
    this.writeVarint(encoded.length);
    this.grow(encoded.length);
    this.buf.set(encoded, this.pos);
    this.pos += encoded.length;
  }
  /** Write boolean as varint 0 or 1 */
  writeBool(v) {
    this.grow(1);
    this.buf[this.pos++] = v ? 1 : 0;
  }
  // --- Field writers (fieldID + value) ---
  writeFieldInt(fieldID, v) {
    this.writeVarint(fieldID);
    this.writeSvarint(v);
  }
  writeFieldFloat(fieldID, v) {
    this.writeVarint(fieldID);
    this.writeFixed64(v);
  }
  writeFieldString(fieldID, v) {
    this.writeVarint(fieldID);
    this.writeString(v);
  }
  writeFieldBool(fieldID, v) {
    this.writeVarint(fieldID);
    this.writeBool(v);
  }
  /** Write end marker (fieldID 0) */
  writeEnd() {
    this.grow(1);
    this.buf[this.pos++] = 0;
  }
  /** Return a trimmed copy of the encoded data (safe for fetch body) */
  bytes() {
    return this.buf.slice(0, this.pos);
  }
  /** Reset encoder for reuse without reallocating */
  reset() {
    this.pos = 0;
  }
};
var Decoder = class {
  data;
  view;
  off;
  fieldID;
  constructor(data) {
    this.data = data;
    this.view = new DataView(data.buffer, data.byteOffset, data.byteLength);
    this.off = 0;
    this.fieldID = 0;
  }
  /** Read next field ID. Returns false at end marker (0x00) or EOF. */
  nextField() {
    if (this.off >= this.data.length) {
      this.fieldID = 0;
      return false;
    }
    const id = this.readVarintRaw();
    this.fieldID = id;
    return id !== 0;
  }
  /** Read unsigned LEB128 varint */
  readVarintRaw() {
    let v = 0;
    let mul = 1;
    while (this.off < this.data.length) {
      const b = this.data[this.off++];
      v += (b & 127) * mul;
      if (b < 128) return v;
      mul *= 128;
      if (mul > 562949953421312) return 0;
    }
    return 0;
  }
  /** Read signed zigzag-encoded int */
  readInt() {
    const uv = this.readVarintRaw();
    const half = Math.floor(uv / 2);
    return uv & 1 ? -(half + 1) : half;
  }
  /** Read float64 from 8 bytes little-endian */
  readFloat() {
    if (this.off + 8 > this.data.length) return 0;
    const v = this.view.getFloat64(this.off, true);
    this.off += 8;
    return v;
  }
  /** Read length-prefixed UTF-8 string */
  readString() {
    const len = this.readVarintRaw();
    if (len === 0) return "";
    if (this.off + len > this.data.length) return "";
    const bytes = this.data.subarray(this.off, this.off + len);
    this.off += len;
    return textDecoder.decode(bytes);
  }
  /** Read boolean (varint 0 or 1) */
  readBool() {
    return this.readVarintRaw() !== 0;
  }
  // --- Nullable readers (1-byte flag + value if present) ---
  readIntPtr() {
    if (this.off >= this.data.length) return null;
    if (this.data[this.off++] === 0) return null;
    return this.readInt();
  }
  readFloatPtr() {
    if (this.off >= this.data.length) return null;
    if (this.data[this.off++] === 0) return null;
    return this.readFloat();
  }
  readStringPtr() {
    if (this.off >= this.data.length) return null;
    if (this.data[this.off++] === 0) return null;
    return this.readString();
  }
  readBoolPtr() {
    if (this.off >= this.data.length) return null;
    if (this.data[this.off++] === 0) return null;
    return this.readBool();
  }
  /** Read a nested model using a decoder function. */
  readNullable(decode) {
    const present = this.readVarintRaw();
    if (present === 0) return null;
    return decode();
  }
  /** Read an array of items using a decoder function. */
  readArray(decode) {
    const count = this.readVarintRaw();
    const items = [];
    for (let i = 0; i < count; i++) {
      items.push(decode());
    }
    return items;
  }
  /** Skip remaining bytes (useful when encountering unknown fields) */
  get remaining() {
    return this.data.length - this.off;
  }
};
var ColumnarDecoder = class {
  buf;
  view;
  off;
  count;
  fieldID;
  constructor(data) {
    this.buf = data;
    this.view = new DataView(data.buffer, data.byteOffset, data.byteLength);
    this.off = 0;
    this.fieldID = 0;
    this.count = this.readVarintRaw();
  }
  /** Advance to next column. Returns false at end marker (0x00) or EOF. */
  nextColumn() {
    if (this.off >= this.buf.length) return false;
    const id = this.readVarintRaw();
    this.fieldID = id;
    if (id === 0) return false;
    return true;
  }
  /** Read count svarint values (zigzag-encoded int column). */
  readColumnInt() {
    const result = new Array(this.count);
    for (let i = 0; i < this.count; i++) {
      result[i] = this.readSvarint();
    }
    return result;
  }
  /** Read count fixed64 float values. */
  readColumnFloat() {
    const result = new Array(this.count);
    for (let i = 0; i < this.count; i++) {
      result[i] = this.view.getFloat64(this.off, true);
      this.off += 8;
    }
    return result;
  }
  /** Read count length-prefixed string values. */
  readColumnString() {
    const result = new Array(this.count);
    for (let i = 0; i < this.count; i++) {
      const len = this.readVarintRaw();
      if (len === 0) {
        result[i] = "";
        continue;
      }
      result[i] = textDecoder.decode(this.buf.subarray(this.off, this.off + len));
      this.off += len;
    }
    return result;
  }
  /** Read count boolean values (varint 0/1). */
  readColumnBool() {
    const result = new Array(this.count);
    for (let i = 0; i < this.count; i++) {
      result[i] = this.readVarintRaw() !== 0;
    }
    return result;
  }
  /** Read count nullable int values (0x00=null, 0x01+svarint). */
  readColumnIntPtr() {
    const result = new Array(this.count);
    for (let i = 0; i < this.count; i++) {
      if (this.buf[this.off++] === 0) {
        result[i] = null;
        continue;
      }
      result[i] = this.readSvarint();
    }
    return result;
  }
  /** Read count nullable float values (0x00=null, 0x01+fixed64). */
  readColumnFloatPtr() {
    const result = new Array(this.count);
    for (let i = 0; i < this.count; i++) {
      if (this.buf[this.off++] === 0) {
        result[i] = null;
        continue;
      }
      result[i] = this.view.getFloat64(this.off, true);
      this.off += 8;
    }
    return result;
  }
  /** Read count nullable string values (0x00=null, 0x01+string). */
  readColumnStringPtr() {
    const result = new Array(this.count);
    for (let i = 0; i < this.count; i++) {
      if (this.buf[this.off++] === 0) {
        result[i] = null;
        continue;
      }
      const len = this.readVarintRaw();
      if (len === 0) {
        result[i] = "";
        continue;
      }
      result[i] = textDecoder.decode(this.buf.subarray(this.off, this.off + len));
      this.off += len;
    }
    return result;
  }
  /** Read count nullable boolean values (0x00=null, 0x01+varint). */
  readColumnBoolPtr() {
    const result = new Array(this.count);
    for (let i = 0; i < this.count; i++) {
      if (this.buf[this.off++] === 0) {
        result[i] = null;
        continue;
      }
      result[i] = this.readVarintRaw() !== 0;
    }
    return result;
  }
  /** Current read position (for reading pagination metadata after 0x00). */
  offset() {
    return this.off;
  }
  /** Read one signed zigzag-encoded varint at current position. */
  readSvarint() {
    const uv = this.readVarintRaw();
    const half = Math.floor(uv / 2);
    return uv & 1 ? -(half + 1) : half;
  }
  /** Read unsigned LEB128 varint. */
  readVarintRaw() {
    let v = 0;
    let mul = 1;
    while (this.off < this.buf.length) {
      const b = this.buf[this.off++];
      v += (b & 127) * mul;
      if (b < 128) return v;
      mul *= 128;
      if (mul > 562949953421312) return 0;
    }
    return 0;
  }
};
function fieldMaskSet(mask, fieldID) {
  const byteIdx = fieldID >>> 3;
  const bitIdx = fieldID & 7;
  if (byteIdx >= mask.length) {
    const next = new Uint8Array(byteIdx + 1);
    next.set(mask, 0);
    next[byteIdx] = 1 << bitIdx;
    return next;
  }
  mask[byteIdx] |= 1 << bitIdx;
  return mask;
}
function fieldMaskHas(mask, fieldID) {
  const byteIdx = fieldID >>> 3;
  if (byteIdx >= mask.length) return false;
  return (mask[byteIdx] & 1 << (fieldID & 7)) !== 0;
}

// src/transport.ts
var FetchTransport = class {
  constructor(endpoint, options) {
    this.endpoint = endpoint;
    this.mode = (options == null ? void 0 : options.mode) ?? "json";
    this.timeout = (options == null ? void 0 : options.timeout) ?? 3e4;
    this.onTokenExpired = options == null ? void 0 : options.onTokenExpired;
    if (options == null ? void 0 : options.headers) this.headers = { ...options.headers };
    if (options == null ? void 0 : options.token) this.headers["Authorization"] = `Bearer ${options.token}`;
  }
  endpoint;
  headers = {};
  mode = "json";
  schema = {};
  timeout;
  onTokenExpired;
  setSchema(schema) {
    this.schema = schema;
  }
  setMode(mode) {
    this.mode = mode;
  }
  setToken(token) {
    this.headers["Authorization"] = `Bearer ${token}`;
  }
  /** Enable/disable request logging */
  debug = false;
  async call(api, params) {
    const start = performance.now();
    try {
      const result = await (this.mode === "binary" ? this.binaryCall(api, params) : this.jsonCall(api, params));
      const ms = (performance.now() - start).toFixed(1);
      if (this.debug) {
        const mode = this.mode === "binary" ? "\u{1F535}" : "\u{1F7E2}";
        const size = result instanceof Uint8Array ? `${result.length}B` : "json";
        console.log(`${mode} ${api} ${ms}ms \u2192 ${size}`, params ?? "");
      }
      return result;
    } catch (e) {
      const ms = (performance.now() - start).toFixed(1);
      if (this.debug) {
        console.error(`\u{1F534} ${api} ${ms}ms \u2717`, e instanceof LuxoError ? e.message : e, params ?? "");
      }
      throw e;
    }
  }
  async jsonCall(api, params) {
    const body = { $api: api };
    if (params) Object.assign(body, params);
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), this.timeout);
    let resp;
    try {
      resp = await fetch(this.endpoint, {
        method: "POST",
        headers: { "Content-Type": "application/json", ...this.headers },
        body: JSON.stringify(body),
        signal: controller.signal
      });
    } catch (e) {
      clearTimeout(timer);
      if (e instanceof DOMException && e.name === "AbortError") {
        throw new LuxoError("TimeoutError", 0, `request timed out after ${this.timeout}ms`);
      }
      throw new LuxoError("NetworkError", 0, e instanceof Error ? e.message : String(e));
    }
    clearTimeout(timer);
    if (resp.status === 401 && this.onTokenExpired) {
      const newToken = await this.onTokenExpired();
      if (newToken) {
        this.setToken(newToken);
        return this.jsonCall(api, params);
      }
    }
    let json;
    try {
      json = await resp.json();
    } catch {
      throw new LuxoError("ParseError", resp.status, `invalid JSON (HTTP ${resp.status})`);
    }
    if (json.error) {
      throw new LuxoError(json.error, json.code ?? resp.status, json.message ?? "", json.traceId);
    }
    return json.data;
  }
  async binaryCall(api, params) {
    const meta = this.schema[api];
    if (!meta) throw new LuxoError("ConfigError", 0, `no schema for "${api}" \u2014 call setSchema() or use LuxoClient.create()`);
    const enc = new Encoder();
    enc.writeVarint(meta.id);
    enc.writeVarint(0);
    if (params && meta.params) {
      for (const pm of meta.params) {
        const v = params[pm.name];
        if (v === void 0 || v === null) continue;
        switch (pm.type) {
          case "Int":
          case "Duration":
            enc.writeFieldInt(pm.fieldID, v);
            break;
          case "Float":
            enc.writeFieldFloat(pm.fieldID, v);
            break;
          case "String":
          case "Enum":
          case "UUID":
          case "Decimal":
            enc.writeFieldString(pm.fieldID, v);
            break;
          case "Boolean":
            enc.writeFieldBool(pm.fieldID, v);
            break;
          case "DateTime":
            enc.writeFieldString(pm.fieldID, v);
            break;
        }
      }
    }
    enc.writeEnd();
    if (this.debug) {
      const b = enc.bytes();
      const hex = Array.from(b).map((x) => x.toString(16).padStart(2, "0")).join(" ");
      console.log(`\u{1F527} ${api} binary ${b.length}B: ${hex}`, params);
    }
    let resp;
    try {
      resp = await fetch(this.endpoint, {
        method: "POST",
        headers: { "Content-Type": "application/x-luxo", "X-Luxo-Mode": "binary", ...this.headers },
        body: enc.bytes()
      });
    } catch (e) {
      throw new LuxoError("NetworkError", 0, e instanceof Error ? e.message : String(e));
    }
    if (!resp.ok) {
      try {
        const json = await resp.json();
        throw new LuxoError(json.error ?? "Error", json.code ?? resp.status, json.message ?? "", json.traceId);
      } catch (e) {
        if (e instanceof LuxoError) throw e;
        throw new LuxoError("Error", resp.status, `HTTP ${resp.status}`);
      }
    }
    return new Uint8Array(await resp.arrayBuffer());
  }
};
var WsTransport = class {
  // ms, doubles each attempt (exponential backoff)
  constructor(endpoint, options) {
    this.endpoint = endpoint;
    this.mode = (options == null ? void 0 : options.mode) ?? "json";
    this.token = options == null ? void 0 : options.token;
  }
  endpoint;
  ws = null;
  mode = "json";
  schema = {};
  pending = /* @__PURE__ */ new Map();
  seq = 0;
  connectPromise = null;
  token;
  closed = false;
  reconnectAttempts = 0;
  maxReconnectAttempts = 10;
  reconnectDelay = 1e3;
  setSchema(schema) {
    this.schema = schema;
  }
  setMode(mode) {
    this.mode = mode;
  }
  setToken(token) {
    this.token = token;
  }
  connect() {
    var _a;
    if (((_a = this.ws) == null ? void 0 : _a.readyState) === WebSocket.OPEN) return Promise.resolve();
    if (this.connectPromise) return this.connectPromise;
    this.connectPromise = new Promise((resolve, reject) => {
      const url = this.token ? `${this.endpoint}?token=${this.token}` : this.endpoint;
      const ws = new WebSocket(url);
      ws.binaryType = "arraybuffer";
      ws.onopen = () => {
        this.ws = ws;
        this.connectPromise = null;
        this.reconnectAttempts = 0;
        resolve();
      };
      ws.onerror = () => {
        this.connectPromise = null;
        reject(new LuxoError("NetworkError", 0, "WebSocket connection failed"));
      };
      ws.onmessage = (event) => {
        if (this.mode === "binary" && event.data instanceof ArrayBuffer) {
          this.handleBinaryResponse(new Uint8Array(event.data));
        } else {
          this.handleJSONResponse(typeof event.data === "string" ? event.data : "");
        }
      };
      ws.onclose = () => {
        this.ws = null;
        this.connectPromise = null;
        for (const [, p] of this.pending) {
          p.reject(new LuxoError("NetworkError", 0, "WebSocket closed"));
        }
        this.pending.clear();
        if (!this.closed && this.reconnectAttempts < this.maxReconnectAttempts) {
          const delay = this.reconnectDelay * Math.pow(2, this.reconnectAttempts);
          this.reconnectAttempts++;
          setTimeout(() => this.connect(), Math.min(delay, 3e4));
        }
      };
    });
    return this.connectPromise;
  }
  async call(api, params) {
    await this.connect();
    const id = ++this.seq;
    return new Promise((resolve, reject) => {
      this.pending.set(id, { resolve, reject });
      if (this.mode === "binary") {
        const meta = this.schema[api];
        if (!meta) {
          reject(new LuxoError("ConfigError", 0, `no schema for "${api}"`));
          return;
        }
        const enc = new Encoder();
        enc.writeVarint(id);
        enc.writeVarint(meta.id);
        enc.writeVarint(0);
        if (params && meta.params) {
          for (const pm of meta.params) {
            const v = params[pm.name];
            if (v === void 0 || v === null) continue;
            switch (pm.type) {
              case "Int":
                enc.writeFieldInt(pm.fieldID, v);
                break;
              case "Float":
                enc.writeFieldFloat(pm.fieldID, v);
                break;
              case "String":
                enc.writeFieldString(pm.fieldID, v);
                break;
              case "Boolean":
                enc.writeFieldBool(pm.fieldID, v);
                break;
            }
          }
        }
        enc.writeEnd();
        this.ws.send(enc.bytes());
      } else {
        this.ws.send(JSON.stringify({ $id: id, $api: api, ...params }));
      }
    });
  }
  handleJSONResponse(data) {
    try {
      const json = JSON.parse(data);
      const id = json.$id;
      const p = this.pending.get(id);
      if (!p) return;
      this.pending.delete(id);
      if (json.error) {
        p.reject(new LuxoError(json.error, json.code ?? 0, json.message ?? "", json.traceId));
      } else {
        p.resolve(json.data);
      }
    } catch {
    }
  }
  handleBinaryResponse(data) {
    let off = 0;
    let id = 0;
    let shift = 0;
    while (off < data.length) {
      const b = data[off++];
      id += (b & 127) * 2 ** shift;
      if (b < 128) break;
      shift += 7;
    }
    const p = this.pending.get(id);
    if (!p) return;
    this.pending.delete(id);
    p.resolve(data.subarray(off));
  }
  close() {
    var _a;
    this.closed = true;
    (_a = this.ws) == null ? void 0 : _a.close();
    this.ws = null;
  }
};
export {
  ColumnarDecoder,
  Decoder,
  Encoder,
  FetchTransport,
  LuxoError,
  WsTransport,
  fieldMaskHas,
  fieldMaskSet
};
