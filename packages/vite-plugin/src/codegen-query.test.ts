import { describe, expect, it } from 'vitest'
import type { LuxoSchema } from '@luxojs/client'
import { genQueryBuilders } from './codegen-query'
import { stripTypeScriptTypes } from 'node:module'
import { spawnSync } from 'node:child_process'
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from 'node:fs'
import { resolve } from 'node:path'
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
