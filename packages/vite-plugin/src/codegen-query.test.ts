import { describe, expect, it } from 'vitest'
import type { LuxoSchema } from '@luxojs/client'
import { genQueryBuilders } from './codegen-query'
import { generateTypes } from './codegen'
import { stripTypeScriptTypes } from 'node:module'
import { spawnSync } from 'node:child_process'
import { mkdtempSync, mkdirSync, writeFileSync, rmSync, symlinkSync } from 'node:fs'
import { resolve, join } from 'node:path'
import { encodeBinaryBody, enumRawValue } from '@luxojs/client'
import type { Filter, Sorter } from '@luxojs/client'

const schema: LuxoSchema = { models: {}, enums: { State: { name: 'State', values: ['OPEN'] } }, apis: {
  listUsers: { id: 1, name: 'listUsers', module: 'user', paginated: true, query: {
    filters: [{ field: 'id', type: 'Int', operators: ['eq', 'gt'] },
      { field: 'state', type: 'State', operators: ['eq', 'ne'] }], sorters: ['id'],
  } },
  custom: { id: 2, name: 'custom', module: 'user', returnList: true },
} }

describe('query capability generation', () => {
  it('keeps Unicode query method names and original wire field strings', () => {
    const fixture: LuxoSchema = { models: {}, apis: { café: {
      id: 1, name: 'café', module: 'test', paginated: true,
      query: { filters: [{ field: '𐐀_name', type: 'String', operators: ['eq'] }], sorters: ['中文2'] },
    } } }
    const source = genQueryBuilders(fixture)
    expect(source).toContain('LuxoQuery_café')
    expect(source).toContain('𐐀_name_eq')
    expect(source).toContain('field: "𐐀_name"')
    expect(source).toContain('中文2_desc')
    mkdirSync('.tmp', { recursive: true })
    const directory = mkdtempSync(resolve('.tmp/unicode-query-'))
    try {
      writeFileSync(join(directory, 'package.json'), JSON.stringify({ type: 'module' }))
      writeFileSync(join(directory, 'queries.ts'), source)
      writeFileSync(join(directory, 'consumer.ts'), `import { LuxoQuery_café as q } from './queries.js'
if (q.𐐀_name_eq('coffee').field !== '𐐀_name' || q.中文2_desc().field !== '中文2') throw new Error('wire name')
`)
      const compiled = spawnSync(resolve('node_modules/.bin/tsc'), ['--ignoreConfig', '--skipLibCheck', '--strict',
        '--target', 'ES2022', '--module', 'NodeNext', '--moduleResolution', 'NodeNext',
        '--outDir', join(directory, 'out'), join(directory, 'consumer.ts')], { encoding: 'utf8', timeout: 60000 })
      expect(compiled.status, compiled.stdout + compiled.stderr).toBe(0)
      const executed = spawnSync(process.execPath, [join(directory, 'out/consumer.js')], { encoding: 'utf8', timeout: 15000 })
      expect(executed.status, executed.stdout + executed.stderr).toBe(0)
    } finally { rmSync(directory, { recursive: true }) }
    for (const invalid of ['2café', 'e\u0301', 'café²', '😀', '字\u200d']) {
      expect(() => genQueryBuilders({ ...fixture, apis: { [invalid]: { ...fixture.apis.café!, name: invalid } } })).toThrow()
    }
  })
  it('compiles and executes numeric scalars beside Double and Int64 enums in both module formats', async () => {
    const root = resolve(import.meta.dirname, '../../..'), temporaryRoot = join(root, '.tmp')
    mkdirSync(temporaryRoot, { recursive: true })
    const directory = mkdtempSync(join(temporaryRoot, 'ts-host-numeric-'))
    try {
      const scope = join(directory, 'node_modules/@luxojs')
      mkdirSync(scope, { recursive: true })
      symlinkSync(join(root, 'packages/client'), join(scope, 'client'), 'dir')
      await generateTypes(hostNumericSchema, directory)
      writeFileSync(join(directory, 'consumer.ts'), hostNumericConsumer)
      for (const moduleType of ['commonjs', 'module']) {
        writeFileSync(join(directory, 'package.json'), JSON.stringify({ type: moduleType }))
        const compiled = spawnSync(resolve('node_modules/.bin/tsc'), ['--ignoreConfig', '--skipLibCheck', '--strict',
          '--target', 'ES2022', '--module', 'NodeNext', '--moduleResolution', 'NodeNext',
          '--outDir', join(directory, 'out'), join(directory, 'consumer.ts')], { encoding: 'utf8', timeout: 60000 })
        expect(compiled.status, compiled.stdout + compiled.stderr).toBe(0)
        const executed = spawnSync(process.execPath, [join(directory, 'out/consumer.js')], { encoding: 'utf8', timeout: 15000 })
        expect(executed.status, executed.stdout + executed.stderr).toBe(0)
      }
    } finally { rmSync(directory, { recursive: true }) }
  })
  it('compiles generated helpers, rejects wrong types and preserves Binary controls', () => {
    mkdirSync('.tmp', { recursive: true })
    const directory = mkdtempSync(resolve('.tmp/query-'))
    try {
      writeFileSync(resolve(directory, 'queries.ts'), genQueryBuilders(schema))
      writeFileSync(resolve(directory, 'types.ts'), "export type State = 'OPEN'\n")
      writeFileSync(resolve(directory, 'consumer.ts'), `import { LuxoQuery_listUsers as q } from './queries.js'
q.id_eq(9007199254740993n)
q.state_eq('OPEN')
q.id_desc()
// @ts-expect-error Int is bigint, never number
q.id_eq(42)
// @ts-expect-error undeclared enum member
q.state_eq('CLOSED')
// @ts-expect-error unsupported operator
q.state_gt('OPEN')
// @ts-expect-error unadvertised field
q.password_eq('secret')
`)
      const compiled = spawnSync(resolve('node_modules/.bin/tsc'), ['--ignoreConfig', '--noEmit', '--skipLibCheck', '--strict',
        '--target', 'ES2022', '--module', 'NodeNext', '--moduleResolution', 'NodeNext', resolve(directory, 'consumer.ts')], { encoding: 'utf8' })
      expect(compiled.status, compiled.stdout + compiled.stderr).toBe(0)
      const source = genQueryBuilders(schema).replace(/^import .*\n/gm, '').replace('export const ', 'const ')
      const query = new Function('enumRawValue', `${stripTypeScriptTypes(source)}\nreturn LuxoQuery_listUsers`)(enumRawValue) as {
        id_eq(value: bigint): Filter; state_eq(value: string): Filter; id_desc(): Sorter
      }
      const actual = encodeBinaryBody({ id: 1 }, { $filters: [query.id_eq(9007199254740993n), query.state_eq('OPEN')], $sorters: [query.id_desc()] })
      const expected = encodeBinaryBody({ id: 1 }, { $filters: [
        { field: 'id', op: 'eq', value: 9007199254740993n }, { field: 'state', op: 'eq', value: 'OPEN' },
      ], $sorters: [{ field: 'id', order: 'desc' }] })
      expect(actual).toEqual(expected)
    } finally { rmSync(directory, { recursive: true }) }
  })
  it('only emits explicitly advertised API fields and operators with typed values', () => {
    const source = genQueryBuilders(schema)
    expect(source).toContain('export const LuxoQuery_listUsers')
    expect(source).toContain('id_eq(value: bigint): Filter')
    expect(source).toContain('state_eq(value: Models.State): Filter')
    expect(source).toContain('id_desc(): Sorter')
    expect(source).not.toContain('state_gt')
    expect(source).not.toContain('LuxoQuery_custom')
  })
  it('rejects unsupported capabilities before writing generated files', () => {
    for (const type of ['JSON', 'Bytes', 'Missing']) {
      expect(() => genQueryBuilders({ ...schema, apis: { bad: {
        id: 1, name: 'bad', module: 'x', paginated: true,
        query: { filters: [{ field: 'raw', type, operators: ['eq'] }] },
      } } })).toThrow()
    }
    expect(() => genQueryBuilders({ ...schema, apis: { bad: {
      id: 1, name: 'bad', module: 'x', paginated: true,
      query: { filters: [{ field: 'active', type: 'Boolean', operators: ['gt'] }] },
    } } })).toThrow()
  })
})

const hostNumericSchema: LuxoSchema = {
  models: {}, enums: {
    Double: { name: 'Double', values: ['OPEN'] }, Int64: { name: 'Int64', values: ['OPEN'] },
  }, types: { Payload: { name: 'Payload', usage: 'inputOutput', fields: [
    { id: 1, name: 'score', type: 'Float' }, { id: 2, name: 'nanos', type: 'Duration' },
    { id: 3, name: 'state', type: 'Enum', typeName: 'Double' }, { id: 4, name: 'code', type: 'Enum', typeName: 'Int64' },
  ] } }, apis: {
    echo: { id: 1, name: 'echo', module: 'test', returnType: 'Payload', params: [{ id: 1, name: 'input', type: 'Model', typeName: 'Payload' }] },
    score: { id: 2, name: 'score', module: 'test', returnType: 'Float', params: [{ id: 1, name: 'value', type: 'Float' }] },
    nanos: { id: 3, name: 'nanos', module: 'test', returnType: 'Duration', params: [{ id: 1, name: 'value', type: 'Duration' }] },
    rows: { id: 4, name: 'rows', module: 'test', returnType: 'Payload', returnList: true, paginated: true,
      params: [{ id: 1, name: 'page', type: 'Int', hasDefault: true }, { id: 2, name: 'pageSize', type: 'Int', hasDefault: true }],
      query: { filters: [{ field: 'score', type: 'Float', operators: ['eq'] }, { field: 'state', type: 'Double', operators: ['eq'] }] } },
  },
}

const hostNumericConsumer = `import { Decoder, Encoder, decodeLuxoJSON, enumRawValue } from '@luxojs/client'
import { decodePayload, decodeJSONPayload } from './types.js'
import { LuxoQuery_rows } from './queries.js'
import * as Client from './client.js'
void Client
for (const nanos of [-9223372036854775808n, 9223372036854775807n]) {
  const encoder = new Encoder()
  encoder.writeVarint(0); encoder.writeVarint(1); encoder.writeFixed64(1.25)
  encoder.writeVarint(2); encoder.writeSvarint(nanos)
  encoder.writeVarint(3); encoder.writeString('OPEN')
  encoder.writeVarint(4); encoder.writeString('FUTURE'); encoder.writeVarint(0)
  const raw = decodeLuxoJSON('{"score":1.25,"nanos":' + nanos + ',"state":"OPEN","code":"FUTURE"}')
  if (typeof raw !== 'object' || raw === null || Array.isArray(raw)) throw new Error('invalid object')
  for (const value of [decodePayload(new Decoder(encoder.bytes())), decodeJSONPayload(raw as { [key: string]: unknown })]) {
    if (value.score !== 1.25 || value.nanos !== nanos || enumRawValue(value.state) !== 'OPEN'
      || enumRawValue(value.code) !== 'FUTURE') throw new Error('numeric or enum identity changed')
  }
}
LuxoQuery_rows.score_eq(1.25)
LuxoQuery_rows.state_eq('OPEN')
`
