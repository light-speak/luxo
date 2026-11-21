import { beforeEach, expect, it, vi } from 'vitest'

type Declaration = { path: string; index: number; kind: number }
type Change = { openFiles?: string[]; closeFiles?: string[]; fileChanges?: { changed?: string[]; deleted?: string[] } }
type FileSystem = { readFile(file: string): string | undefined; fileExists(file: string): boolean | undefined }
const state = vi.hoisted(() => ({
  version: '7.0.2', mode: 'valid', failUpdate: false, updateError: undefined as unknown,
  requests: [] as Change[], symbols: [] as ({ declarations: Declaration[] } | undefined)[],
  fs: undefined as FileSystem | undefined,
  close: vi.fn<() => Promise<void>>(), dispose: vi.fn<() => Promise<void>>(),
}))

vi.mock('typescript', () => ({ get version() { return state.version } }))
vi.mock('typescript/unstable/async', () => ({ API: class {
  constructor(options: { fs: FileSystem }) { state.fs = options.fs }
  async updateSnapshot(change: Change) {
    state.requests.push(change)
    if (state.failUpdate) throw state.updateError ?? new Error('native snapshot failed')
    return { getDefaultProjectForFile: async () => state.mode === 'project' ? undefined : project(), dispose: state.dispose }
  }
  close = state.close
} }))

import { CallIdentity } from './call-identity'

const declaration: Declaration = { path: '/project/sdk/client.ts', index: 17, kind: 179 }
function project() {
  return {
    program: { getSourceFile: async () => state.mode === 'source' ? undefined : {} },
    checker: {
      getSymbolAtLocation: async () => state.mode === 'module' ? undefined : {
        getExports: async () => new Map(state.mode === 'client' ? [] : [['LuxoClient', {
          name: 'LuxoClient', getMembers: async () => new Map([['method', { declarations: [declaration] }]]),
        }]]),
      },
      getSymbolAtPosition: async () => state.symbols,
    },
  }
}

beforeEach(() => {
  state.version = '7.0.2'
  state.mode = 'valid'
  state.failUpdate = false
  state.updateError = undefined
  state.requests = []
  state.symbols = []
  state.fs = undefined
  state.close.mockReset().mockResolvedValue(undefined)
  state.dispose.mockReset().mockResolvedValue(undefined)
})

function create() { return new CallIdentity('/project', '/project/sdk/client.ts') }
const file = '/project/app.ts'

it('rejects an unvalidated compiler before starting the native session', async () => {
  state.version = '6.0.3'
  const owner = create()
  await expect(owner.identify(file, '', [])).rejects.toThrow('validated TypeScript 7.0.2; received 6.0.3')
  expect(state.requests).toEqual([])
  await owner.close()
  expect(state.close).not.toHaveBeenCalled()
})

it.each(['project', 'module', 'client'])('reports a missing %s and disposes the acquired snapshot', async mode => {
  state.mode = mode
  const owner = create()
  await expect(owner.identify(file, '', [])).rejects.toThrow('Cannot resolve SDK call identity')
  expect(state.dispose).toHaveBeenCalledOnce()
  await owner.close()
  expect(state.close).toHaveBeenCalledOnce()
})

it('does not invent SDK calls when the source project does not contain its generated client', async () => {
  state.mode = 'source'
  const owner = create()
  expect(await owner.identify(file, '', [{ memberStart: 1 }])).toEqual(new Set())
  await owner.close()
})

it('requires nonempty complete declaration identity rather than a matching name or one matching overload', async () => {
  const ordinary = { ...declaration, path: '/project/ordinary.ts' }
  state.symbols = [undefined, { declarations: [] }, { declarations: [ordinary] },
    { declarations: [declaration, ordinary] }, { declarations: [declaration] }]
  const owner = create()
  expect(await owner.identify(file, '', [1, 2, 3, 4, 5].map(memberStart => ({ memberStart })))).toEqual(new Set([5]))
  await owner.close()
})

it('keeps one client anchor and closes the previous caller before opening fresh source', async () => {
  const owner = create()
  await owner.identify(file, 'first', [])
  await owner.identify(file, '', [])
  expect(state.requests.map(request => request.openFiles)).toEqual([
    [file, '/project/sdk/client.ts'], undefined, [file],
  ])
  expect(state.requests[1]!.closeFiles).toEqual([file])
  expect(state.fs?.readFile(file)).toBe('')
  expect(state.fs?.fileExists(file)).toBe(true)
  expect(state.fs?.fileExists('/other')).toBeUndefined()
  await owner.close()
  expect(state.fs?.readFile(file)).toBeUndefined()
  await expect(owner.identify(file, '', [])).rejects.toThrow('selection compiler is closed')
  await owner.invalidate(file, true)
  await owner.close()
  expect(state.close).toHaveBeenCalledOnce()
})

it('invalidates deleted input and releases the client anchor before updating its source', async () => {
  const owner = create()
  await owner.invalidate(file, false)
  expect(state.requests).toEqual([])
  await owner.identify(file, 'source', [])
  await owner.invalidate('/project/sdk/client.ts', false)
  expect(state.requests[2]!.closeFiles).toEqual(['/project/sdk/client.ts'])
  await owner.identify(file, 'changed', [])
  expect(state.requests[4]!.openFiles).toEqual([file, '/project/sdk/client.ts'])
  await owner.invalidate(file, true)
  expect(state.requests.at(-1)!.fileChanges).toEqual({ deleted: [file] })
  expect(state.fs?.readFile(file)).toBeUndefined()
  await owner.close()
})

it('releases ownership after a native request fails so cleanup and subsequent requests can run', async () => {
  const owner = create()
  state.failUpdate = true
  await expect(owner.identify(file, '', [])).rejects.toThrow('native snapshot failed')
  state.failUpdate = false
  expect(await owner.identify(file, '', [])).toEqual(new Set())
  await owner.close()
  expect(state.close).toHaveBeenCalledOnce()
})

it('retains the active document when releasing it fails so the next operation retries cleanup', async () => {
  const owner = create()
  await owner.identify(file, 'first', [])
  state.failUpdate = true
  await expect(owner.identify(file, 'second', [])).rejects.toThrow('native snapshot failed')
  state.failUpdate = false
  await owner.identify(file, 'third', [])
  expect(state.requests[1]!.closeFiles).toEqual([file])
  expect(state.requests[2]!.closeFiles).toEqual([file])
  await owner.close()
})

it('preserves non-Error failures from the native API without falling back to names', async () => {
  const owner = create()
  state.failUpdate = true
  state.updateError = 'native string failure'
  await expect(owner.identify(file, '', [])).rejects.toMatchObject({
    message: expect.stringContaining('native string failure'), cause: 'native string failure',
  })
  await owner.close()
})

it('reports the first close error while retrying child cleanup', async () => {
  const owner = create()
  await owner.identify(file, '', [])
  state.close.mockRejectedValueOnce(new Error('release failed'))
  await expect(owner.close()).rejects.toThrow('release failed')
  expect(state.close).toHaveBeenCalledTimes(2)
  await owner.close()
  expect(state.close).toHaveBeenCalledTimes(2)
})

it('retains a failed API for a later close attempt when both cleanup attempts fail', async () => {
  const owner = create()
  await owner.identify(file, '', [])
  state.close.mockRejectedValueOnce(new Error('release failed')).mockRejectedValueOnce(new Error('cleanup failed'))
  await expect(owner.close()).rejects.toBeInstanceOf(AggregateError)
  await owner.close()
  expect(state.close).toHaveBeenCalledTimes(3)
})
