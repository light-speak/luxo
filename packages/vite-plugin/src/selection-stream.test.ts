import { stripTypeScriptTypes } from 'node:module'
import { expect, it } from 'vitest'
import { prepareSelection, type LuxoSchema } from '@luxojs/client'
import { analyzeAndTransform, analyzeVerifiedCalls } from './analyzer'

const schema: LuxoSchema = {
  models: {
    User: { name: 'User', fields: [{ id: 1, name: 'name', type: 'String' }] },
    Update: { name: 'Update', fields: [
      { id: 1, name: 'id', type: 'Int' },
      { id: 2, name: 'user', type: 'User', relation: true },
      { id: 3, name: 'title', type: 'String' },
    ] },
  },
  apis: {
    updates: { id: 1, name: 'updates', module: 'updates', returnType: 'Update', stream: true },
    listUpdates: { id: 2, name: 'listUpdates', module: 'updates', returnType: 'Update', returnList: true },
  },
}

const callbacks = [
  'update => consume(update.user.name)',
  'function(update) { consume(update.user.name) }',
  '((update: Update) => consume(update.user.name))',
  '((update: Update) => consume(update.user.name)) as ((update: Update) => void)',
  '((update: Update) => consume(update.user.name)) satisfies ((update: Update) => void)',
  '((update: Update) => consume(update.user.name))!',
]

const cases = [false, true].flatMap(prepared => [false, true].flatMap(observer =>
  callbacks.map(callback => ({ prepared, observer, callback }))))

it.each([false, true].flatMap(prepared => ['map', 'forEach'].flatMap(method =>
  callbacks.map(callback => ({ prepared, method, callback })))))
('tracks array callback wrappers (prepared=$prepared, $method, $callback)', ({ prepared, method, callback }) => {
  const source = `async function render(client) {
    const updates = await client.listUpdates(); updates.${method}(${callback})
  }`
  const output = analyzeAndTransform(source, 'array.ts', schema, prepared)
  expect(output).toContain(prepared ? 'Prepare("user{name}")' : "$select: 'user{name}'")
})

it('treats an external array callback as escaping element access', () => {
  const source = 'const updates = await client.listUpdates(); updates.map(render)'
  expect(analyzeAndTransform(source, 'array.ts', schema)).toContain("$select: 'id,title'")
})

it.each(cases)('tracks the onData relation (prepared=$prepared, observer=$observer, $callback)', ({ prepared, observer, callback }) => {
  const source = `client.subscribeUpdates({}, ${callback}${observer ? ', observer' : ''})`
  const output = analyzeAndTransform(source, 'stream.ts', schema, prepared)
  expect(output).not.toBeNull()
  expect(output).toContain(prepared ? 'Prepare("user{name}")' : "$select: 'user{name}'")
})

it.each([false, true])('delivers the projected relation and retains observer identity (prepared=%s)', async prepared => {
  const source = `async function watch(client, consume, observer) {
    return await client.subscribeUpdates({}, update => consume(update.user.name), observer)
  }`
  const output = analyzeAndTransform(source, 'stream.ts', schema, prepared)!
  const code = stripTypeScriptTypes(output.replace(/^import .*\n/gm, ''), { mode: 'strip' })
  const create = new Function('$luxoSelectionPrepare', code + '\nreturn watch') as
    (prepare: typeof prepareSelection) => (client: object, consume: (name: string) => void, observer: object) => Promise<() => void>
  const values: string[] = []
  const observer = { onComplete() {} }
  const cancel = () => {}
  const result = await create(prepareSelection)({
    async subscribeUpdates(params: { $select: string | { text: string } }, onData: (update: { user?: { name: string } }) => void, actualObserver: object) {
      const selected = typeof params.$select === 'string' ? params.$select : params.$select.text
      const update = selected === 'user{name}' ? { user: { name: 'selected' } } : {}
      expect(actualObserver).toBe(observer)
      onData(update)
      return cancel
    },
  }, name => values.push(name), observer)
  expect(result).toBe(cancel)
  expect(values).toEqual(['selected'])
})

it('retains the safe fallback for a callback hidden inside a spread tuple', () => {
  const source = 'client.subscribeUpdates(...args, function observer() { consume(observerField) })'
  const output = analyzeAndTransform(source, 'unknown-stream.ts', schema)
  expect(output).toContain("$luxoSelectionArgs('id,title', ...args, function observer()")
  expect(output).not.toContain('user{name}')
})

it('does not bind observer callback parameters as data after a leading spread', () => {
  const source = 'client.subscribeUpdates(...args, ((update: Update) => consume(update.user.name)) as unknown as StreamObserver)'
  const output = analyzeAndTransform(source, 'unknown-stream.ts', schema)
  expect(output).toContain("$luxoSelectionArgs('id,title', ...args")
})

it('keeps ambiguous callable arguments on the safe projection', () => {
  const source = 'client.subscribeUpdates(...params, update => consume(update.user.name), error => consume(error.user.name))'
  expect(analyzeAndTransform(source, 'ambiguous.ts', schema)).toContain("$luxoSelectionArgs('id,title',")
})

it('keeps an external callback on the safe projection even when observer is inline', () => {
  const source = 'client.subscribeUpdates({}, onData, { onError(error) { consume(error) } })'
  expect(analyzeAndTransform(source, 'external.ts', schema)).toContain("$select: 'id,title'")
})

it('honors an explicit selection with observer', () => {
  const source = "client.subscribeUpdates({ $select: 'user{name}' }, update => consume(update.user.name), observer)"
  expect(analyzeAndTransform(source, 'manual.ts', schema)).toBeNull()
})

it.each([false, true].flatMap(prepared => [false, true].map(observer => ({ prepared, observer }))))
('keeps visible data callbacks after a spread prefix (prepared=$prepared, observer=$observer)', ({ prepared, observer }) => {
  const source = `client.subscribeUpdates(...params, update => consume(update.user.name)${observer ? ', observer' : ''})`
  const output = analyzeAndTransform(source, 'spread-callback.ts', schema, prepared)
  expect(output).toContain(prepared ? 'Prepare("user{name}")' : "$luxoSelectionArgs('user{name}',")
})

it('requires verified SDK identity before changing a same-name stream call', async () => {
  const source = 'ordinary.subscribeUpdates({}, update => consume(update.user.name), observer)'
  expect(await analyzeVerifiedCalls(source, 'ordinary.ts', schema, async () => new Set())).toBeNull()
})
