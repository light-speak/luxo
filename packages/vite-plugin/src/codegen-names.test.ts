import { expect, it } from 'vitest'
import { spawnSync } from 'node:child_process'
import { mkdirSync, mkdtempSync, rmSync, symlinkSync, writeFileSync } from 'node:fs'
import { join, resolve } from 'node:path'
import { generateTypes } from './codegen'
import type { LuxoSchema } from '@luxojs/client'

it.each(['commonjs', 'module'])('compiles a business Record type in %s output', async type => {
  const root = resolve(import.meta.dirname, '../../..')
  const temporary = join(root, '.tmp')
  mkdirSync(temporary, { recursive: true })
  const directory = mkdtempSync(join(temporary, 'ts-codegen-names-'))
  const schema: LuxoSchema = {
    models: { Record: { name: 'Record', fields: [
      { id: 1, name: 'id', type: 'Int' },
      { id: 2, name: 'child', type: 'Model', typeName: 'Record', nullable: true },
    ] } },
    apis: {
      read: { id: 1, name: 'read', module: 'test', returnType: 'Record' },
      list: { id: 2, name: 'list', module: 'test', returnType: 'Record', returnList: true, paginated: true },
      save: { id: 3, name: 'save', module: 'test', returnType: 'Record',
        params: [{ id: 1, name: 'input', type: 'Model', typeName: 'Record' }] },
    },
  }
  try {
    await generateTypes(schema, directory)
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
