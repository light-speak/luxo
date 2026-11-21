import { expect, it } from 'vitest'
import { spawnSync } from 'node:child_process'
import { mkdirSync, mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from 'node:fs'
import { join, resolve } from 'node:path'
import { generateTypes } from './codegen'
import type { LuxoSchema } from '@luxojs/client'

it.each(['commonjs', 'module'])('compiles %s input helpers without merging business types', async type => {
  const root = resolve(import.meta.dirname, '../../..')
  const temporary = join(root, '.tmp')
  mkdirSync(temporary, { recursive: true })
  const directory = mkdtempSync(join(temporary, 'ts-input-aliases-'))
  const schema: LuxoSchema = { models: {
    User: { name: 'User', usage: 'inputOutput', fields: [
      { id: 1, name: 'id', type: 'Int' },
      { id: 2, name: 'parent', type: 'Model', typeName: 'User', nullable: true },
    ] },
    LuxoUser: { name: 'LuxoUser', usage: 'inputOutput', fields: [{ id: 1, name: 'id', type: 'Int' }] },
  }, types: Object.fromEntries(['UserInput', 'LuxoUserInput2'].map(name => [name,
    { name, usage: 'output' as const, fields: [{ id: 1, name: 'label', type: 'String' }] }])), apis: {
    readUser: { id: 1, name: 'readUser', module: 'test', returnType: 'User' },
    writeUser: { id: 2, name: 'writeUser', module: 'test', returnType: 'Int',
      params: [{ id: 1, name: 'input', type: 'Model', typeName: 'User' }] },
  } }
  try {
    await generateTypes(schema, directory)
    mkdirSync(join(directory, 'node_modules/@luxojs'), { recursive: true })
    symlinkSync(join(root, 'packages/client'), join(directory, 'node_modules/@luxojs/client'), 'dir')
    writeFileSync(join(directory, 'package.json'), JSON.stringify({ type }))
    writeFileSync(join(directory, 'consumer.ts'), `import type { LuxoClient } from './client.js'
import type { UserInput } from './types.js'
const input: Parameters<LuxoClient['writeUser']>[0] = { input: { id: 1n, parent: null } }
const output: UserInput<true> = { label: 'ok' }
void input; void output
`)
    writeFileSync(join(directory, 'tsconfig.json'), JSON.stringify({ compilerOptions: {
      target: 'ES2022', module: 'NodeNext', moduleResolution: 'NodeNext', strict: true, noEmit: true, types: [],
    }, include: ['*.ts'] }))
    const result = spawnSync(join(root, 'packages/vite-plugin/node_modules/.bin/tsc'), ['-p', directory], { encoding: 'utf8', timeout: 30000 })
    expect(result.status, result.stdout + result.stderr).toBe(0)
    expect(readFileSync(join(directory, 'types.ts'), 'utf8')).toContain('interface LuxoUserInput3 {')
  } finally { rmSync(directory, { recursive: true }) }
})

it.each(['commonjs', 'module'].flatMap(type => ['Record', '__proto__', 'constructor', 'Promise', 'validateJSONValue', 'validateJSONArray', 'Café', '𐐀Name'].map(name => [type, name] as const)))
('compiles %s output with business type %s', async (type, name) => {
  const root = resolve(import.meta.dirname, '../../..')
  const temporary = join(root, '.tmp')
  mkdirSync(temporary, { recursive: true })
  const directory = mkdtempSync(join(temporary, 'ts-codegen-names-'))
  const schema: LuxoSchema = {
    enums: {},
    types: {},
    models: { [name]: { name, fields: [
      { id: 1, name: 'id', type: 'Int' },
      { id: 2, name: 'child', type: 'Model', typeName: name, nullable: true },
      { id: 3, name: '__proto__', type: 'String', nullable: true },
      { id: 4, name: 'constructor', type: 'String' },
      { id: 5, name: 'payload', type: 'JSON' },
      { id: 6, name: 'café', type: 'String' },
      { id: 7, name: '𐐀_name', type: 'String', nullable: true },
    ] } },
    apis: {
      ['__proto__']: { id: 4, name: '__proto__', module: 'test', returnType: name },
      café: { id: 7, name: 'café', module: 'test', returnType: name,
        params: [{ id: 1, name: 'entrée', type: 'Model', typeName: name }] },
      read: { id: 1, name: 'read', module: 'test', returnType: name },
      ping: { id: 5, name: 'ping', module: 'test', returnType: 'Int' },
      watch: { id: 6, name: 'watch', module: 'test', returnType: name, stream: true },
      list: { id: 2, name: 'list', module: 'test', returnType: name, returnList: true, paginated: true, params: [
        { id: 7, name: 'page', type: 'Int', hasDefault: true },
        { id: 9, name: 'pageSize', type: 'Int', hasDefault: true },
      ] },
      save: { id: 3, name: 'save', module: 'test', returnType: name,
        params: [{ id: 1, name: 'input', type: 'Model', typeName: name }] },
    },
  }
  try {
    await generateTypes(schema, directory)
    const files = ['types.ts', 'schema.ts', 'requests.ts', 'client.ts', 'queries.ts']
    const previous = files.map(file => readFileSync(join(directory, file), 'utf8'))
    await expect(generateTypes({ models: {}, apis: {
      broken: { id: 1, name: 'broken', module: 'test', returnType: 'Int',
        params: [{ id: 1, name: 'input', type: 'Model', typeName: 'Missing' }] },
    } }, directory)).rejects.toThrow('missing input type Missing')
    for (const usage of ['output', 'unused'] as const) {
      await expect(generateTypes({ models: {}, types: {
        Broken: { name: 'Broken', usage, fields: [{ id: 1, name: 'child', type: 'Model', typeName: 'Missing' }] },
      }, apis: {} }, directory)).rejects.toThrow('missing model type Missing')
    }
    for (const [index, file] of files.entries()) expect(readFileSync(join(directory, file), 'utf8')).toBe(previous[index])
    mkdirSync(join(directory, 'node_modules/@luxojs'), { recursive: true })
    symlinkSync(join(root, 'packages/client'), join(directory, 'node_modules/@luxojs/client'), 'dir')
    writeFileSync(join(directory, 'package.json'), JSON.stringify({ type }))
    writeFileSync(join(directory, 'tsconfig.json'), JSON.stringify({ compilerOptions: {
      target: 'ES2022', module: 'NodeNext', moduleResolution: 'NodeNext', strict: true, noEmit: true, types: [],
    }, include: ['*.ts'] }))
    const result = spawnSync(join(root, 'packages/vite-plugin/node_modules/.bin/tsc'),
      ['-p', directory], { encoding: 'utf8', timeout: 30000 })
    expect(result.status, result.stdout + result.stderr).toBe(0)
  } finally {
    rmSync(directory, { recursive: true })
  }
})
