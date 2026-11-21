import type { LuxoField, LuxoSchema, LuxoSealed, LuxoSealedVariant, TypeUsage } from '@luxojs/client'
import { genCompleteCheck } from './codegen-response'

export interface SealedCodegen {
  usage(name: string): TypeUsage
  inputName(name: string): string
  fieldType(field: LuxoField, input: boolean): string
  binary(field: LuxoField): string
  json(field: LuxoField, value: string): string
}

export function sealedCaseName(owner: string, variant: LuxoSealedVariant): string {
  return `${owner}$${variant.name}$${variant.id}`
}

export function sealedFields(owner: LuxoSealed): LuxoField[] {
  return owner.variants.flatMap(variant => variant.fields)
}

export function genSealeds(schema: LuxoSchema, codegen: SealedCodegen): string {
  let out = ''
  for (const owner of Object.values(schema.sealeds ?? {})) {
    const usage = codegen.usage(owner.name)
    if (usage === 'input' || usage === 'inputOutput') out += inputTypes(owner, codegen)
    if (usage === 'output' || usage === 'inputOutput') {
      out += outputTypes(owner, codegen)
      out += decodeSealed(owner)
      for (const variant of owner.variants) out += decodePayload(owner, variant, codegen)
    }
  }
  return out
}

function inputTypes(owner: LuxoSealed, codegen: SealedCodegen): string {
  const name = codegen.inputName(owner.name)
  let out = `export type ${name} = ${owner.variants.map(variant => sealedCaseName(name, variant)).join(' | ') || 'never'}\n\n`
  for (const variant of owner.variants) {
    out += `export interface ${sealedCaseName(name, variant)} {\n  readonly $case: '${variant.name}'\n`
    for (const field of variant.fields) {
      out += `  ${JSON.stringify(field.name)}${field.hasDefault ? '?' : ''}: ${codegen.fieldType(field, true)}${field.nullable ? ' | null' : ''}\n`
    }
    out += '}\n\n'
  }
  return out
}

function outputTypes(owner: LuxoSealed, codegen: SealedCodegen): string {
  const cases = owner.variants.map(variant => `${sealedCaseName(owner.name, variant)}<Selection>`)
  let out = `export type ${owner.name}<Selection extends boolean = false> = ${cases.join(' | ') || 'never'}\n\n`
  for (const variant of owner.variants) {
    out += `export interface ${sealedCaseName(owner.name, variant)}<Selection extends boolean = false> {\n  readonly $case: '${variant.name}'\n`
    for (const field of variant.fields) {
      const value = codegen.fieldType(field, false) + (field.nullable ? ' | null' : '')
      out += `  ${JSON.stringify(field.name)}: true extends Selection ? Selected<${value}> : ${value}\n`
    }
    out += '}\n\n'
  }
  return out
}

function decodeSealed(owner: LuxoSealed): string {
  const name = owner.name
  let out = `export function decode${name}(dec: Decoder): ${name}<true> { return decode${name}$value(dec, 1) }\n`
  out += `function decode${name}$value(dec: Decoder, depth: number): ${name}<true> {\n`
  out += "  if (depth > 64) throw new RangeError('model nesting exceeds 64 levels')\n"
  out += '  const tag = dec.readVariantTag()\n'
  out += owner.variants.length ? '  const payload = dec.readBytes()\n' : '  dec.readBytes()\n'
  out += genCompleteCheck('dec', 'dec.remaining !== 0', `${name} sealed`)
  out += '  switch (tag) {\n'
  for (const variant of owner.variants) out += `    case ${variant.id}: return decode${sealedCaseName(name, variant)}$row(new Decoder(payload), depth + 1)\n`
  out += `    default: throw new TypeError('unknown ${name} sealed tag ' + tag)\n  }\n}\n\n`
  out += `export function decodeJSON${name}(data: Record<string, unknown>): ${name}<true> { return decodeJSON${name}$value(data, 1) }\n`
  out += `function decodeJSON${name}$value(data: Record<string, unknown>, depth: number): ${name}<true> {\n`
  out += "  if (depth > 64) throw new RangeError('model nesting exceeds 64 levels')\n"
  out += `  if (!data || typeof data !== 'object' || Array.isArray(data)) throw new TypeError('${name} sealed value must be an object')\n`
  out += `  const keys = Object.keys(data)\n  if (keys.length !== 1) throw new TypeError('${name} requires exactly one case')\n`
  out += '  switch (keys[0]) {\n'
  for (const variant of owner.variants) out += `    case '${variant.name}': return decodeJSON${sealedCaseName(name, variant)}$payload(data['${variant.name}'], depth + 1)\n`
  out += `    default: throw new TypeError('unknown ${name} sealed case ' + keys[0])\n  }\n}\n\n`
  out += `export function decodeSealedList${name}(data: Uint8Array): ${name}<true>[] { return decodeSealedList${name}$value(new Decoder(data), 1, true) }\n`
  out += `function decodeSealedList${name}$value(dec: Decoder, depth: number, complete = false): ${name}<true>[] {\n`
  out += '  const count = dec.readListCount()\n'
  out += `  const values: ${name}<true>[] = []\n  for (let i = 0; i < count; i++) values.push(decode${name}$value(new Decoder(dec.readBytes()), depth))\n`
  out += `  if (dec.error !== null || (complete && dec.remaining !== 0)) throw new Error(dec.error ?? 'trailing ${name} sealed list data')\n  return values\n}\n\n`
  out += `export function decodeJSON${name}$list(data: unknown): ${name}<true>[] { return decodeJSON${name}$items(data, 1) }\n`
  out += `function decodeJSON${name}$items(data: unknown, depth: number): ${name}<true>[] {\n  if (!Array.isArray(data)) throw new TypeError('${name} list must be an array')\n  return (data as unknown[]).map(item => decodeJSON${name}$value(item as Record<string, unknown>, depth))\n}\n\n`
  return out
}

function payloadObject(owner: LuxoSealed, variant: LuxoSealedVariant): string {
  const fields = [`$case: '${variant.name}'`, ...variant.fields.map(field => `[${JSON.stringify(field.name)}]: undefined`)]
  return `  const obj: ${sealedCaseName(owner.name, variant)}<true> = { ${fields.join(', ')} }\n`
}

function decodePayload(owner: LuxoSealed, variant: LuxoSealedVariant, codegen: SealedCodegen): string {
  const name = sealedCaseName(owner.name, variant)
  let out = `function decode${name}$row(dec: Decoder, depth: number): ${name}<true> {\n`
  out += "  if (depth > 64) throw new RangeError('model nesting exceeds 64 levels')\n"
  out += payloadObject(owner, variant)
  out += '  dec.skipArenaHeader()\n  while (dec.nextField()) {\n    switch (dec.fieldID) {\n'
  for (const field of variant.fields) out += `      case ${field.id}: obj[${JSON.stringify(field.name)}] = ${codegen.binary(field)}; break\n`
  out += `      default: throw new TypeError('unknown ${owner.name}.${variant.name} field ID ' + dec.fieldID)\n    }\n  }\n`
  out += genCompleteCheck('dec', 'dec.remaining !== 0', `${name} payload`)
  out += '  return obj\n}\n\n'
  out += `function decodeJSON${name}$payload(raw: unknown, depth: number): ${name}<true> {\n`
  out += "  if (depth > 64) throw new RangeError('model nesting exceeds 64 levels')\n"
  out += `  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) throw new TypeError('${owner.name}.${variant.name} payload must be an object')\n`
  out += '  const data = raw as Record<string, unknown>\n'
  out += payloadObject(owner, variant)
  out += '  for (const key of Object.keys(data)) {\n    switch (key) {\n'
  for (const field of variant.fields) out += `      case '${field.name}': break\n`
  out += `      default: throw new TypeError('unknown ${owner.name}.${variant.name} payload field ' + key)\n    }\n  }\n`
  for (const field of variant.fields) {
    const value = `data[${JSON.stringify(field.name)}]`
    out += `  if (Object.hasOwn(data, '${field.name}')) {\n`
    if (!field.nullable && field.type !== 'JSON') out += `    if (${value} == null) throw new TypeError('${owner.name}.${variant.name}.${field.name} is not nullable')\n`
    const read = codegen.json(field, value)
    out += `    obj[${JSON.stringify(field.name)}] = ${field.nullable ? `${value} === null ? null : ` : ''}${read}\n  }\n`
  }
  return out + '  return obj\n}\n\n'
}
