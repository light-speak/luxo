import { defineConfig } from 'tsdown'

export default defineConfig({
  entry: ['src/index.ts'],
  format: ['cjs', 'esm'],
  target: 'es2022',
  platform: 'neutral',
  outExtensions: ({ format }) => ({ js: format === 'cjs' ? '.js' : '.mjs' }),
  dts: false,
  clean: true,
  deps: { neverBundle: ['typescript'] },
})
