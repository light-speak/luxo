import { expect, it } from 'vitest'
import { execFile } from 'node:child_process'
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { join, resolve, sep } from 'node:path'
import { promisify } from 'node:util'
import { build } from 'tsdown'
import { generateNullableRootBenchmark } from './nullable-root.bench'

const execute = promisify(execFile)
const root = resolve(import.meta.dirname, '../../..')
const temporaryRoot = join(root, '.tmp')

it.each(['cjs', 'esm'] as const)('executes generated nullable root fixtures as static %s code', async format => {
  mkdirSync(temporaryRoot, { recursive: true })
  const directory = mkdtempSync(join(temporaryRoot, 'nullable-root-test-'))
  try {
    await generateNullableRootBenchmark(directory)
    const checker = join(import.meta.dirname, 'nullable-root.bench.ts')
    writeFileSync(join(directory, 'check.ts'),
      `import { verifyNullableRoots } from ${JSON.stringify(checker)}\n` +
      "import * as roots from './roots'\nverifyNullableRoots(roots)\nconsole.log('verified nullable roots')\n")
    const extension = format === 'cjs' ? '.cjs' : '.mjs'
    await build({ config: false, cwd: root, entry: [join(directory, 'check.ts')],
      outDir: join(directory, 'out'), format, target: 'es2022', platform: 'node', dts: false,
      outExtensions: () => ({ js: extension }),
      alias: { '@luxojs/client': join(root, 'packages/client/src/index.ts') },
    })
    const result = await execute(process.execPath, [join(directory, 'out', `check${extension}`)], { timeout: 30000 })
    expect(result.stdout).toContain('verified nullable roots')
  } finally {
    if (directory.startsWith(temporaryRoot + sep)) rmSync(directory, { recursive: true })
  }
}, 45000)
