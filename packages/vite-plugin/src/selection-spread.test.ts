import { stripTypeScriptTypes } from 'node:module'
import { parse } from '@babel/parser'
import { describe, expect, it } from 'vitest'
import { prepareSelection, type LuxoSchema } from '@luxojs/client'
import { analyzeAndTransform, analyzeVerifiedCalls } from './analyzer'

const schema: LuxoSchema = {
  models: { User: { name: 'User', fields: [
    { id: 1, name: 'id', type: 'Int' },
    { id: 2, name: 'name', type: 'String' },
  ] } },
  apis: {
    getUser: { id: 1, name: 'getUser', module: 'user', returnType: 'User', params: [{ id: 1, name: 'id', type: 'Int' }] },
    me: { id: 2, name: 'me', module: 'user', returnType: 'User' },
    liveUsers: { id: 3, name: 'liveUsers', module: 'user', returnType: 'User', stream: true },
  },
}

type Params = { id?: bigint; $select?: string | { text: string } }
type Options = { signal: AbortSignal }
type User = { id: bigint; name: string }

function executable<T>(source: string): T {
  const code = stripTypeScriptTypes(source.replace(/^import .*\n/gm, ''), { mode: 'strip' })
  return new Function('$luxoSelectionPrepare', code + '\nreturn view')(prepareSelection) as T
}

describe('spread argument selection', () => {
  it.each([false, true].flatMap(prepared => [
    '...args', '...args, options()', '...empty, ...args, options()', '...args, ...options()',
  ].map(argumentsSource => ({ prepared, argumentsSource }))))
  ('preserves call evaluation and options: $argumentsSource (prepared=$prepared)', async ({ prepared, argumentsSource }) => {
    const source = `async function view(client, args, empty, options) {
      const user = await client.getUser(${argumentsSource}); return user.name
    }`
    const result = analyzeAndTransform(source, 'spread.ts', schema, prepared)
    expect(result).not.toBeNull()
    const run = executable<(client: object, args: Iterable<unknown>, empty: [], options: () => unknown) => Promise<string>>(result!)
    const events: string[] = []
    const options = { signal: new AbortController().signal }
    const tupleHasOptions = argumentsSource === '...args'
    const params: Params = { id: 9007199254740993n }
    const args = { *[Symbol.iterator]() {
      events.push('iterate'); yield params
      if (tupleHasOptions) yield options
      events.push('exhausted')
    } }
    const client = { get getUser() {
      events.push('callee')
      return async function(this: object, actual: Params, actualOptions: Options) {
        expect(this).toBe(client)
        expect(actual.id).toBe(params.id)
        expect(actualOptions).toBe(options)
        const selected = actual.$select
        expect(typeof selected === 'string' ? selected : selected?.text).toBe('name')
        events.push('invoke'); return { id: params.id!, name: 'selected' }
      }
    } }
    const optionsValue = () => { events.push('options'); return argumentsSource.endsWith('...options()') ? [options] : options }
    expect(await run(client, args, [], optionsValue)).toBe('selected')
    expect(events).toEqual(['callee', 'iterate', 'exhausted', ...(tupleHasOptions ? [] : ['options']), 'invoke'])
    expect(params).toEqual({ id: 9007199254740993n })
  })

  it.each([false, true])('preserves an explicit selection inside a tuple (prepared=%s)', async prepared => {
    const source = 'async function view(client, args) { const user = await client.getUser(...args); return user.name }'
    const output = analyzeAndTransform(source, 'manual.ts', schema, prepared)
    const run = executable<(client: object, args: readonly [Params, Options]) => Promise<string>>(output!)
    const params = { id: 42n, $select: 'id' }
    const options = { signal: new AbortController().signal }
    expect(await run({ async getUser(actual: Params, actualOptions: Options) {
      expect(actual).toEqual(params); expect(actualOptions).toBe(options)
      return { id: 42n, name: 'manual' }
    } }, [params, options])).toBe('manual')
  })

  it.each(['...args', '...empty, ...args', '...args, /* keep 中文 */'])('handles an empty argument tuple: %s', async argumentsSource => {
    const source = `async function view(client, args, empty) { const user = await client.me(${argumentsSource}); return user.name }`
    const output = analyzeAndTransform(source, 'empty.ts', schema, true)
    const run = executable<(client: object, args: [], empty: []) => Promise<string>>(output!)
    expect(await run({ async me(params: Params) {
      expect(typeof params.$select === 'object' && params.$select.text).toBe('name')
      return { id: 42n, name: 'empty' }
    } }, [], [])).toBe('empty')
    if (argumentsSource.includes('keep')) expect(output).toContain('/* keep 中文 */')
  })

  it('keeps stream callbacks and observer positions intact', async () => {
    const source = 'async function view(client, args) { return await client.subscribeLiveUsers(...args) }'
    const output = analyzeAndTransform(source, 'stream.ts', schema, true)
    expect(output).not.toBeNull()
    const run = executable<(client: object, args: readonly [Params, (user: User) => void, object]) => Promise<() => void>>(output!)
    const values: User[] = []
    const onData = (user: User) => { values.push(user) }
    const observer = { onComplete() {} }
    const cancel = () => {}
    expect(await run({ async subscribeLiveUsers(params: Params, actualData: typeof onData, actualObserver: object) {
      expect(params.$select).toBeDefined()
      expect(actualData).toBe(onData); expect(actualObserver).toBe(observer)
      actualData({ id: 42n, name: 'event' }); return cancel
    } }, [{}, onData, observer])).toBe(cancel)
    expect(values).toEqual([{ id: 42n, name: 'event' }])
  })

  it('retains nested selection edits inside the spread expression', async () => {
    const source = `async function view(client) {
      const user = await client.getUser(...await (async () => {
        const seed = await client.getUser({ id: 42n }); return [{ id: seed.id }]
      })()); return user.name
    }`
    const output = analyzeAndTransform(source, 'nested-spread.ts', schema, true)
    const run = executable<(client: object) => Promise<string>>(output!)
    const selections: string[] = []
    expect(await run({ async getUser(params: Params) {
      const selected = params.$select
      selections.push(typeof selected === 'string' ? selected : selected!.text)
      return { id: params.id!, name: 'nested' }
    } })).toBe('nested')
    expect(selections).toEqual(['id', 'name'])
  })

  it('leaves an ordinary same-name spread call unchanged after identity verification', async () => {
    const source = 'const user = await ordinary.getUser(...args); consume(user.name)'
    expect(await analyzeVerifiedCalls(source, 'ordinary.ts', schema, async () => new Set())).toBeNull()
  })

  it('emits one helper and keeps directives and TSX syntax valid', () => {
    const source = `#!/usr/bin/env node
"use client";
async function view(client, args) {
  const first = await client.getUser(...args)
  const second = await client.getUser(...args)
  return <div>{first.id}{second.name}</div>
}`
    const output = analyzeAndTransform(source, 'component.tsx', schema, true)!
    expect(() => parse(output, { sourceType: 'module', plugins: ['typescript', 'jsx'] })).not.toThrow()
    expect(output.startsWith('#!/usr/bin/env node\n"use client";')).toBe(true)
    expect(output.match(/function \$luxoSelectionArgs</g)).toHaveLength(1)
    expect(output).toContain('Prepare("id")')
    expect(output).toContain('Prepare("name")')
  })

  it('avoids collisions with source helper names', async () => {
    const source = `const $luxoSelectionArgs = 'source';
async function view(client, args) { const user = await client.getUser(...args); return [user.name, $luxoSelectionArgs] }`
    const output = analyzeAndTransform(source, 'collision.ts', schema)!
    expect(output).toContain('function $luxoSelection_Args<')
    const run = executable<(client: object, args: readonly [Params]) => Promise<string[]>>(output)
    expect(await run({ async getUser(params: Params) {
      expect(params.$select).toBe('name'); return { id: 1n, name: 'result' }
    } }, [{ id: 1n }])).toEqual(['result', 'source'])
  })

  it('propagates iterator failures without invoking the SDK', async () => {
    const source = 'async function view(client, args) { const user = await client.getUser(...args); return user.name }'
    const output = analyzeAndTransform(source, 'iterator.ts', schema)!
    const run = executable<(client: object, args: Iterable<Params>) => Promise<string>>(output)
    const failure = new Error('iterator failed')
    let called = false
    const args = { *[Symbol.iterator](): Generator<Params> { throw failure } }
    await expect(run({ getUser() { called = true } }, args)).rejects.toBe(failure)
    expect(called).toBe(false)
  })
})
