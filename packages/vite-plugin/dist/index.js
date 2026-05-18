var __create = Object.create;
var __defProp = Object.defineProperty;
var __getOwnPropDesc = Object.getOwnPropertyDescriptor;
var __getOwnPropNames = Object.getOwnPropertyNames;
var __getProtoOf = Object.getPrototypeOf;
var __hasOwnProp = Object.prototype.hasOwnProperty;
var __export = (target, all) => {
  for (var name in all)
    __defProp(target, name, { get: all[name], enumerable: true });
};
var __copyProps = (to, from, except, desc) => {
  if (from && typeof from === "object" || typeof from === "function") {
    for (let key of __getOwnPropNames(from))
      if (!__hasOwnProp.call(to, key) && key !== except)
        __defProp(to, key, { get: () => from[key], enumerable: !(desc = __getOwnPropDesc(from, key)) || desc.enumerable });
  }
  return to;
};
var __toESM = (mod, isNodeMode, target) => (target = mod != null ? __create(__getProtoOf(mod)) : {}, __copyProps(
  // If the importer is in node compatibility mode or this is not an ESM
  // file that has been converted to a CommonJS file using a Babel-
  // compatible transform (i.e. "__esModule" has not been set), then set
  // "default" to the CommonJS "module.exports" for node compatibility.
  isNodeMode || !mod || !mod.__esModule ? __defProp(target, "default", { value: mod, enumerable: true }) : target,
  mod
));
var __toCommonJS = (mod) => __copyProps(__defProp({}, "__esModule", { value: true }), mod);

// src/index.ts
var index_exports = {};
__export(index_exports, {
  default: () => index_default,
  luxo: () => luxo
});
module.exports = __toCommonJS(index_exports);

// src/analyzer.ts
var import_parser = require("@babel/parser");
var import_traverse = __toESM(require("@babel/traverse"));
var t = __toESM(require("@babel/types"));
var traverse = import_traverse.default.default ?? import_traverse.default;
var MAX_NESTING_DEPTH = 5;
function analyzeAndTransform(code, _id, schema) {
  let ast;
  try {
    ast = (0, import_parser.parse)(code, {
      sourceType: "module",
      plugins: ["typescript", "jsx"]
    });
  } catch {
    return null;
  }
  const varToAPI = /* @__PURE__ */ new Map();
  const varToType = /* @__PURE__ */ new Map();
  const varToParent = /* @__PURE__ */ new Map();
  const apiTrees = /* @__PURE__ */ new Map();
  traverse(ast, {
    VariableDeclarator(path) {
      const id = path.node.id;
      const init = path.node.init;
      if (!init) return;
      if (t.isObjectPattern(id)) {
        const expr2 = t.isAwaitExpression(init) ? init.argument : init;
        if (t.isCallExpression(expr2) && t.isMemberExpression(expr2.callee)) {
          const method = expr2.callee.property;
          if (t.isIdentifier(method)) {
            const apiMeta = Object.values(schema.apis).find((a) => a.name === method.name);
            if (apiMeta == null ? void 0 : apiMeta.returnType) {
              const model = schema.models[apiMeta.returnType];
              if (model) {
                const root = new FieldNode("root");
                for (const prop of id.properties) {
                  if (t.isObjectProperty(prop) && t.isIdentifier(prop.key)) {
                    const fieldName = prop.key.name;
                    if (model.fields.some((f) => f.name === fieldName)) {
                      root.addChild(fieldName);
                    }
                  }
                }
                if (root.children.size > 0 && root.children.size < model.fields.length) {
                  apiTrees.set(method.name, root);
                  varToAPI.set("__destruct__" + method.name, method.name);
                }
              }
            }
          }
        }
        return;
      }
      if (!t.isIdentifier(id)) return;
      const expr = t.isAwaitExpression(init) ? init.argument : init;
      if (t.isCallExpression(expr) && t.isMemberExpression(expr.callee)) {
        const method = expr.callee.property;
        if (t.isIdentifier(method)) {
          const apiMeta = Object.values(schema.apis).find((a) => a.name === method.name);
          if (apiMeta == null ? void 0 : apiMeta.returnType) {
            varToAPI.set(id.name, method.name);
            varToType.set(id.name, apiMeta.returnType);
          }
        }
      }
      if (t.isMemberExpression(expr) && t.isIdentifier(expr.object) && t.isIdentifier(expr.property)) {
        const parentVar = expr.object.name;
        if (varToAPI.has(parentVar) || varToParent.has(parentVar)) {
          varToParent.set(id.name, { var: parentVar, field: expr.property.name });
          const parentType = varToType.get(parentVar);
          if (parentType) {
            const model = schema.models[parentType];
            const field = model == null ? void 0 : model.fields.find((f) => f.name === expr.property.name);
            if ((field == null ? void 0 : field.type) && schema.models[field.type]) {
              varToType.set(id.name, field.type);
            }
          }
        }
      }
    },
    // Track forEach/map lambda params: arr.forEach(item => ...)
    CallExpression(path) {
      const callee = path.node.callee;
      if (!t.isMemberExpression(callee)) return;
      const method = callee.property;
      if (!t.isIdentifier(method)) return;
      if (!["forEach", "map", "filter", "find", "some", "every", "flatMap"].includes(method.name)) return;
      const obj = callee.object;
      let sourceVar;
      let sourceField;
      if (t.isMemberExpression(obj) && t.isIdentifier(obj.object) && t.isIdentifier(obj.property)) {
        sourceVar = obj.object.name;
        sourceField = obj.property.name;
      } else if (t.isIdentifier(obj)) {
        sourceVar = obj.name;
      }
      if (!sourceVar) return;
      if (!varToAPI.has(sourceVar) && !varToParent.has(sourceVar)) return;
      const arg = path.node.arguments[0];
      if (!arg) return;
      let paramName;
      if (t.isArrowFunctionExpression(arg) || t.isFunctionExpression(arg)) {
        const firstParam = arg.params[0];
        if (t.isIdentifier(firstParam)) {
          paramName = firstParam.name;
        }
      }
      if (!paramName) return;
      if (sourceField) {
        varToParent.set(paramName, { var: sourceVar, field: sourceField });
        const parentType = varToType.get(sourceVar);
        if (parentType) {
          const model = schema.models[parentType];
          const field = model == null ? void 0 : model.fields.find((f) => f.name === sourceField);
          if ((field == null ? void 0 : field.type) && schema.models[field.type]) {
            varToType.set(paramName, field.type);
          }
        }
      } else {
        const parent = varToParent.get(sourceVar);
        if (parent) {
          varToParent.set(paramName, parent);
          const parentType = varToType.get(sourceVar);
          if (parentType) varToType.set(paramName, parentType);
        }
      }
    }
  });
  if (varToAPI.size === 0) return null;
  for (const apiName of varToAPI.values()) {
    if (!apiTrees.has(apiName)) {
      apiTrees.set(apiName, new FieldNode("root"));
    }
  }
  const processChain = (chain) => {
    const rootVar = chain[0];
    let apiName;
    let fieldChain;
    if (varToAPI.has(rootVar)) {
      apiName = varToAPI.get(rootVar);
      fieldChain = chain.slice(1);
    } else {
      const resolved = resolveParentChain(rootVar, varToParent);
      if (!resolved) return;
      apiName = varToAPI.get(resolved.rootVar);
      fieldChain = [...resolved.fields, ...chain.slice(1)];
    }
    if (!apiName) return;
    const tree = apiTrees.get(apiName);
    if (!tree) return;
    const apiMeta = Object.values(schema.apis).find((a) => a.name === apiName);
    if (!(apiMeta == null ? void 0 : apiMeta.returnType)) return;
    addFieldChain(tree, fieldChain, schema.models[apiMeta.returnType], schema);
  };
  traverse(ast, {
    MemberExpression(path) {
      const chain = extractChain(path.node);
      if (chain && chain.length >= 2) processChain(chain);
    },
    OptionalMemberExpression(path) {
      const chain = extractChain(path.node);
      if (chain && chain.length >= 2) processChain(chain);
    }
  });
  let modified = false;
  let result = code;
  for (const [apiName, tree] of apiTrees) {
    const selectStr = tree.toSelectString();
    if (!selectStr) continue;
    const depth = tree.maxDepth();
    if (depth > MAX_NESTING_DEPTH) {
      console.warn(
        `[luxo] Warning: ${apiName} has ${depth}-level nested field selection (max recommended: ${MAX_NESTING_DEPTH}). Deep nesting may cause performance issues. Consider using @native or restructuring your query.`
      );
    }
    const apiMeta = Object.values(schema.apis).find((a) => a.name === apiName);
    if (apiMeta == null ? void 0 : apiMeta.returnType) {
      const model = schema.models[apiMeta.returnType];
      if (model && tree.children.size >= model.fields.length) continue;
    }
    const callStart = new RegExp(`await\\s+\\w+\\.${escapeRegex(apiName)}\\(`);
    const startMatch = callStart.exec(result);
    if (!startMatch) continue;
    const startIdx = startMatch.index;
    const argsStart = startIdx + startMatch[0].length;
    let parenDepth = 1;
    let i = argsStart;
    while (i < result.length && parenDepth > 0) {
      if (result[i] === "(") parenDepth++;
      else if (result[i] === ")") parenDepth--;
      if (parenDepth > 0) i++;
    }
    if (parenDepth !== 0) continue;
    const argsStr = result.substring(argsStart, i);
    if (argsStr.includes("$select")) continue;
    const trimmed = argsStr.trim();
    const before = result.substring(0, argsStart);
    const after = result.substring(i);
    const injection = trimmed === "" ? `{ $select: '${selectStr}' }` : `${argsStr}, { $select: '${selectStr}' }`;
    result = before + injection + after;
    modified = true;
  }
  return modified ? result : null;
}
var FieldNode = class _FieldNode {
  constructor(name) {
    this.name = name;
  }
  name;
  children = /* @__PURE__ */ new Map();
  addChild(name) {
    let child = this.children.get(name);
    if (!child) {
      child = new _FieldNode(name);
      this.children.set(name, child);
    }
    return child;
  }
  toSelectString() {
    if (this.children.size === 0) return "";
    const parts = [];
    for (const child of this.children.values()) {
      const nested = child.toSelectString();
      parts.push(nested ? `${child.name}{${nested}}` : child.name);
    }
    return parts.join(",");
  }
  maxDepth() {
    if (this.children.size === 0) return 0;
    let max = 0;
    for (const child of this.children.values()) {
      max = Math.max(max, child.maxDepth());
    }
    return max + 1;
  }
};
function extractChain(node) {
  const chain = [];
  let current = node;
  while (t.isMemberExpression(current) || t.isOptionalMemberExpression(current)) {
    const prop = current.property;
    if (t.isIdentifier(prop)) {
      chain.unshift(prop.name);
    } else {
      break;
    }
    current = current.object;
    if ((t.isMemberExpression(current) || t.isOptionalMemberExpression(current)) && current.computed) {
      current = current.object;
    }
  }
  if (t.isIdentifier(current)) {
    chain.unshift(current.name);
  }
  return chain.length >= 2 ? chain : null;
}
function resolveParentChain(varName, parents) {
  const fields = [];
  let current = varName;
  const seen = /* @__PURE__ */ new Set();
  while (parents.has(current)) {
    if (seen.has(current)) break;
    seen.add(current);
    const parent = parents.get(current);
    fields.unshift(parent.field);
    current = parent.var;
  }
  if (fields.length === 0) return null;
  return { rootVar: current, fields };
}
function addFieldChain(root, chain, model, schema) {
  let currentModel = model;
  let currentNode = root;
  for (const seg of chain) {
    const field = currentModel == null ? void 0 : currentModel.fields.find((f) => f.name === seg);
    if (!field) break;
    currentNode = currentNode.addChild(seg);
    currentModel = field.type ? schema.models[field.type] : void 0;
  }
}
function escapeRegex(s) {
  return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

// src/codegen.ts
var import_fs = require("fs");
var import_path = require("path");
async function generateTypes(schema, outDir) {
  if (!(0, import_fs.existsSync)(outDir)) {
    (0, import_fs.mkdirSync)(outDir, { recursive: true });
  }
  (0, import_fs.writeFileSync)((0, import_path.join)(outDir, "types.ts"), genTypes(schema));
  (0, import_fs.writeFileSync)((0, import_path.join)(outDir, "schema.ts"), genSchema(schema));
  (0, import_fs.writeFileSync)((0, import_path.join)(outDir, "client.ts"), genClient(schema));
  console.log(`[luxo] Generated types \u2192 ${outDir}/`);
}
function luxoTypeToTS(type) {
  switch (type) {
    case "Int":
    case "Float":
    case "Duration":
      return "number";
    case "String":
    case "DateTime":
    case "UUID":
    case "Decimal":
    case "Enum":
      return "string";
    case "Boolean":
      return "boolean";
    case "Bytes":
      return "string";
    default:
      return type;
  }
}
function resolveFieldType(field, schema) {
  var _a, _b;
  const isList = field.isList || field.list;
  const typeName = field.typeName || field.type;
  let ts;
  if ((_a = schema.enums) == null ? void 0 : _a[typeName]) {
    ts = typeName;
  } else if (schema.models[typeName]) {
    ts = typeName;
  } else if ((_b = schema.types) == null ? void 0 : _b[typeName]) {
    ts = typeName;
  } else {
    ts = luxoTypeToTS(field.type);
  }
  return isList ? `${ts}[]` : ts;
}
function genTypes(schema) {
  let out = "// Auto-generated by @luxo/vite-plugin. DO NOT EDIT.\n\n";
  out += "import type { Page } from '@luxojs/client'\n";
  out += "import { Decoder, ColumnarDecoder } from '@luxojs/client'\n\n";
  if (schema.enums) {
    for (const e of Object.values(schema.enums)) {
      const values = e.values.map((v) => `'${v}'`).join(" | ");
      out += `export type ${e.name} = ${values}
`;
      out += `export const ${e.name}Values = [${e.values.map((v) => `'${v}'`).join(", ")}] as const

`;
    }
  }
  if (schema.types) {
    for (const t2 of Object.values(schema.types)) {
      out += `export interface ${t2.name} {
`;
      for (const field of t2.fields) {
        const ts = resolveFieldType(field, schema);
        out += field.nullable ? `  ${field.name}: ${ts} | null
` : `  ${field.name}: ${ts}
`;
      }
      out += "}\n\n";
      out += `export function decode${t2.name}(dec: Decoder): ${t2.name} {
`;
      out += `  const obj: Record<string, unknown> = {}
`;
      out += `  while (dec.nextField()) {
`;
      out += `    switch (dec.fieldID) {
`;
      for (const field of t2.fields) {
        out += `      case ${field.id}: obj['${field.name}'] = `;
        out += genFieldRead(field, schema) + "; break\n";
      }
      out += `      default: break
`;
      out += `    }
`;
      out += `  }
`;
      out += `  return obj as ${t2.name}
`;
      out += "}\n\n";
      out += genColumnarDecode(t2, schema);
      out += genPaginatedDecode(t2);
    }
  }
  for (const model of Object.values(schema.models)) {
    out += `export interface ${model.name} {
`;
    for (const field of model.fields) {
      const ts = resolveFieldType(field, schema);
      out += field.nullable ? `  ${field.name}: ${ts} | null
` : `  ${field.name}: ${ts}
`;
    }
    out += "}\n\n";
    out += `export function decode${model.name}(dec: Decoder): ${model.name} {
`;
    out += `  const obj: Record<string, unknown> = {}
`;
    out += `  while (dec.nextField()) {
`;
    out += `    switch (dec.fieldID) {
`;
    for (const field of model.fields) {
      out += `      case ${field.id}: obj['${field.name}'] = `;
      out += genFieldRead(field, schema) + "; break\n";
    }
    out += `      default: break
`;
    out += `    }
`;
    out += `  }
`;
    out += `  return obj as ${model.name}
`;
    out += "}\n\n";
    out += genColumnarDecode(model, schema);
    out += genPaginatedDecode(model);
  }
  out += "export type { Page }\n";
  return out;
}
function columnTSType(field) {
  const n = field.nullable;
  switch (field.type) {
    case "Int":
    case "Duration":
    case "DateTime":
    case "Float":
      return n ? "(number | null)[]" : "number[]";
    case "String":
    case "Enum":
    case "UUID":
    case "Decimal":
      return n ? "(string | null)[]" : "string[]";
    case "Boolean":
      return n ? "(boolean | null)[]" : "boolean[]";
    default:
      return n ? "(string | null)[]" : "string[]";
  }
}
function columnDefault(field) {
  if (field.nullable) return "null";
  switch (field.type) {
    case "Int":
    case "Duration":
    case "DateTime":
    case "Float":
      return "0";
    case "Boolean":
      return "false";
    default:
      return "''";
  }
}
function genColumnRead(field) {
  const n = field.nullable;
  switch (field.type) {
    case "Int":
    case "Duration":
    case "DateTime":
      return n ? "r.readColumnIntPtr()" : "r.readColumnInt()";
    case "Float":
      return n ? "r.readColumnFloatPtr()" : "r.readColumnFloat()";
    case "String":
    case "Enum":
    case "UUID":
    case "Decimal":
      return n ? "r.readColumnStringPtr()" : "r.readColumnString()";
    case "Boolean":
      return n ? "r.readColumnBoolPtr()" : "r.readColumnBool()";
    default:
      return n ? "r.readColumnStringPtr()" : "r.readColumnString()";
  }
}
function genColumnarDecode(model, schema) {
  const fields = model.fields.filter((f) => !f.relation);
  let out = `export function decodeColumnar${model.name}(data: Uint8Array): ${model.name}[] {
`;
  out += `  const r = new ColumnarDecoder(data)
`;
  for (const f of fields) {
    out += `  let _${f.name}: ${columnTSType(f)} | undefined
`;
  }
  out += `  while (r.nextColumn()) {
`;
  out += `    switch (r.fieldID) {
`;
  for (const f of fields) {
    out += `      case ${f.id}: _${f.name} = ${genColumnRead(f)}; break
`;
  }
  out += `      default: break
`;
  out += `    }
`;
  out += `  }
`;
  out += `  const items: ${model.name}[] = new Array(r.count)
`;
  out += `  for (let i = 0; i < r.count; i++) {
`;
  out += `    items[i] = {
`;
  for (const f of fields) {
    out += `      ${f.name}: _${f.name} ? _${f.name}[i] : ${columnDefault(f)},
`;
  }
  out += `    } as ${model.name}
`;
  out += `  }
`;
  out += `  return items
`;
  out += "}\n\n";
  return out;
}
function genPaginatedDecode(model) {
  const fields = model.fields.filter((f) => !f.relation);
  let out = `export function decodePaginated${model.name}(data: Uint8Array): Page<${model.name}> {
`;
  out += `  const r = new ColumnarDecoder(data)
`;
  for (const f of fields) {
    out += `  let _${f.name}: ${columnTSType(f)} | undefined
`;
  }
  out += `  while (r.nextColumn()) {
`;
  out += `    switch (r.fieldID) {
`;
  for (const f of fields) {
    out += `      case ${f.id}: _${f.name} = ${genColumnRead(f)}; break
`;
  }
  out += `      default: break
`;
  out += `    }
`;
  out += `  }
`;
  out += `  const items: ${model.name}[] = new Array(r.count)
`;
  out += `  for (let i = 0; i < r.count; i++) {
`;
  out += `    items[i] = {
`;
  for (const f of fields) {
    out += `      ${f.name}: _${f.name} ? _${f.name}[i] : ${columnDefault(f)},
`;
  }
  out += `    } as ${model.name}
`;
  out += `  }
`;
  out += `  const total = r.readSvarint()
`;
  out += `  const page = r.readSvarint()
`;
  out += `  const pageSize = r.readSvarint()
`;
  out += `  return { items, total, page, pageSize }
`;
  out += "}\n\n";
  return out;
}
function genFieldRead(field, schema) {
  var _a, _b;
  const n = field.nullable;
  const tn = field.typeName || field.type;
  if (field.relation || tn && (schema.models[tn] || ((_a = schema.types) == null ? void 0 : _a[tn]))) {
    if (field.isList) {
      return `dec.readArray(() => decode${tn}(dec))`;
    }
    return n ? `dec.readNullable(() => decode${tn}(dec))` : `decode${tn}(dec)`;
  }
  switch (field.type) {
    case "Int":
    case "Duration":
      return n ? "dec.readIntPtr()" : "dec.readInt()";
    case "Float":
      return n ? "dec.readFloatPtr()" : "dec.readFloat()";
    case "String":
    case "DateTime":
    case "UUID":
    case "Decimal":
    case "Enum":
      return n ? "dec.readStringPtr()" : "dec.readString()";
    case "Boolean":
      return n ? "dec.readBoolPtr()" : "dec.readBool()";
    default: {
      const fallbackTn = field.typeName || field.type;
      if (schema.models[fallbackTn] || ((_b = schema.types) == null ? void 0 : _b[fallbackTn])) {
        if (field.isList) {
          return `dec.readArray(() => decode${fallbackTn}(dec))`;
        }
        return n ? `dec.readNullable(() => decode${tn}(dec))` : `decode${tn}(dec)`;
      }
      return "null";
    }
  }
}
function genSchema(schema) {
  let out = "// Auto-generated by @luxo/vite-plugin. DO NOT EDIT.\n\n";
  out += "import type { APISchema } from '@luxojs/client'\n\n";
  out += "/** API schema map \u2014 used by binary transport for encoding requests. */\n";
  out += "export const LUXO_SCHEMA: Record<string, APISchema> = {\n";
  for (const api of Object.values(schema.apis)) {
    if (api.name.startsWith("svc:")) continue;
    out += `  '${api.name}': { id: ${api.id}`;
    if (api.params && api.params.length > 0) {
      out += ", params: [\n";
      for (const p of api.params) {
        out += `    { fieldID: ${p.id}, name: '${p.name}', type: '${p.type}' },
`;
      }
      out += "  ]";
    }
    out += " },\n";
  }
  out += "}\n";
  return out;
}
function genClient(schema) {
  let out = "// Auto-generated by @luxo/vite-plugin. DO NOT EDIT.\n\n";
  out += "import type { Transport, TransportOptions, Page } from '@luxojs/client'\n";
  out += "import { FetchTransport, WsTransport, Decoder } from '@luxojs/client'\n";
  out += "import { LUXO_SCHEMA } from './schema'\n";
  const typeImports = /* @__PURE__ */ new Set();
  const decoderImports = /* @__PURE__ */ new Set();
  for (const api of Object.values(schema.apis)) {
    if (api.returnType && !isScalar(api.returnType)) {
      typeImports.add(api.returnType);
      decoderImports.add(`decode${api.returnType}`);
      if (api.paginated) {
        decoderImports.add(`decodePaginated${api.returnType}`);
      } else if (api.returnList) {
        decoderImports.add(`decodeColumnar${api.returnType}`);
      }
    }
  }
  if (typeImports.size > 0) {
    out += `import type { ${Array.from(typeImports).join(", ")} } from './types'
`;
    out += `import { ${Array.from(decoderImports).join(", ")} } from './types'
`;
  }
  out += "\nexport class LuxoClient {\n";
  out += "  private transport: Transport\n\n";
  out += "  constructor(transport: Transport) {\n";
  out += "    this.transport = transport\n";
  out += "    transport.setSchema(LUXO_SCHEMA)\n";
  out += "  }\n\n";
  out += "  /** Create client from URL \u2014 auto-detects HTTP vs WebSocket transport. */\n";
  out += "  static create(endpoint: string, options?: TransportOptions): LuxoClient {\n";
  out += "    const transport = endpoint.startsWith('ws') ? new WsTransport(endpoint, options) : new FetchTransport(endpoint, options)\n";
  out += "    return new LuxoClient(transport)\n";
  out += "  }\n\n";
  out += "  /** Switch transport mode (json for debug, binary for production). */\n";
  out += "  setMode(mode: 'json' | 'binary'): void { this.transport.setMode(mode) }\n\n";
  out += "  /** Update auth token. */\n";
  out += "  setToken(token: string): void { this.transport.setToken(token) }\n\n";
  out += "  /** Close transport (WebSocket). */\n";
  out += "  close(): void { this.transport.close?.() }\n\n";
  out += "  static readonly schema = LUXO_SCHEMA\n\n";
  for (const api of Object.values(schema.apis)) {
    if (api.name.startsWith("svc:")) continue;
    out += genMethod(api, schema);
  }
  out += "}\n";
  return out;
}
function genDecode(api) {
  const t2 = api.returnType;
  if (!t2) return "d instanceof Uint8Array ? new Decoder(d).readInt() : d as number";
  if (isScalar(t2)) {
    const ts = luxoTypeToTS(t2);
    const br = t2 === "Float" ? "new Decoder(d).readFloat()" : t2 === "Boolean" ? "new Decoder(d).readBool()" : t2 === "Int" || t2 === "Duration" ? "new Decoder(d).readInt()" : "new Decoder(d).readString()";
    return `d instanceof Uint8Array ? ${br} : d as ${ts}`;
  }
  if (api.paginated) {
    return `d instanceof Uint8Array ? decodePaginated${t2}(d) : d as Page<${t2}>`;
  }
  if (api.returnList) {
    return `d instanceof Uint8Array ? decodeColumnar${t2}(d) : d as ${t2}[]`;
  }
  const binDec = `decode${t2}(new Decoder(d))`;
  return `d instanceof Uint8Array ? ${binDec} : d as ${t2}`;
}
function resolveParamType(p, schema) {
  var _a, _b;
  if ((_a = schema.enums) == null ? void 0 : _a[p.type]) return p.type;
  if (schema.models[p.type]) return p.type;
  if ((_b = schema.types) == null ? void 0 : _b[p.type]) return p.type;
  return luxoTypeToTS(p.type);
}
function genMethod(api, schema) {
  const retTS = getReturnType(api);
  const decode = genDecode(api);
  const call = (params) => `    const d = await this.transport.call('${api.name}'${params ? `, ${params}` : ""})
    return ${decode}
`;
  if (api.params && api.params.length > 0) {
    const paramFields = api.params.map((p) => `${p.name}: ${resolveParamType(p, schema)}`).join("; ");
    if (api.name.startsWith("list") && api.paginated) {
      return `  async ${api.name}(params?: { ${paramFields}; $select?: string; $filters?: unknown[]; $sorters?: unknown[] }): Promise<${retTS}> {
` + call("params") + `  }

`;
    }
    return `  async ${api.name}(params: { ${paramFields} }): Promise<${retTS}> {
` + call("params") + `  }

`;
  }
  if (api.name.startsWith("list") && api.paginated) {
    return `  async ${api.name}(params?: { page?: number; pageSize?: number; $select?: string; $filters?: unknown[]; $sorters?: unknown[] }): Promise<${retTS}> {
` + call("params") + `  }

`;
  }
  if (api.name.startsWith("create") && api.returnType) {
    return `  async ${api.name}(input: Partial<${api.returnType}>): Promise<${retTS}> {
` + call("input") + `  }

`;
  }
  if (api.name.startsWith("update") && api.returnType) {
    return `  async ${api.name}(id: number, input: Partial<${api.returnType}>): Promise<${retTS}> {
` + call("{ id, ...input }") + `  }

`;
  }
  return `  async ${api.name}(): Promise<${retTS}> {
` + call("") + `  }

`;
}
function getReturnType(api) {
  if (!api.returnType) return "number";
  const ts = luxoTypeToTS(api.returnType);
  if (api.paginated) return `Page<${ts}>`;
  if (api.returnList) return `${ts}[]`;
  return ts;
}
function isScalar(type) {
  return ["Int", "Float", "String", "Boolean", "DateTime", "Duration", "UUID", "Bytes", "Decimal", "Enum"].includes(type);
}

// src/index.ts
function luxo(options = {}) {
  let schema = null;
  const outDir = options.outDir || "src/luxo";
  return {
    name: "luxo",
    async buildStart() {
      schema = await loadSchema(options);
      if (!schema) {
        console.warn("[luxo] No schema available \u2014 skipping codegen. Set endpoint + key or schemaFile option.");
        return;
      }
      await generateTypes(schema, outDir);
    },
    transform(code, id) {
      if (!id.endsWith(".ts") && !id.endsWith(".tsx")) return null;
      if (id.includes("node_modules")) return null;
      if (id.includes("/luxo/")) return null;
      if (!schema) return null;
      const result = analyzeAndTransform(code, id, schema);
      if (!result) return null;
      return { code: result, map: null };
    }
  };
}
async function loadSchema(options) {
  if (options.endpoint) {
    try {
      let url = `${options.endpoint}?$schema`;
      if (options.introspectionKey) {
        url += `&key=${options.introspectionKey}`;
      }
      const resp = await fetch(url);
      if (resp.ok) {
        return await resp.json();
      }
      console.warn(`[luxo] Failed to fetch schema: ${resp.status}`);
    } catch (e) {
      console.warn(`[luxo] Failed to connect to ${options.endpoint}:`, e);
    }
  }
  if (options.schema) {
    try {
      const fs = await import("fs");
      const data = fs.readFileSync(options.schema, "utf-8");
      return JSON.parse(data);
    } catch (e) {
      console.warn(`[luxo] Failed to read schema file:`, e);
    }
  }
  console.warn("[luxo] No schema available. Set endpoint or schema option.");
  return null;
}
var index_default = luxo;
// Annotate the CommonJS export names for ESM import in node:
0 && (module.exports = {
  luxo
});
