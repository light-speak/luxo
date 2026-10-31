import { defineConfig } from 'tsdown'

export default defineConfig({
  entry: ['src/index.ts'],
  format: ['cjs', 'esm'],
  target: 'node22',
  platform: 'node',
  outExtensions: ({ format }) => ({ js: format === 'cjs' ? '.js' : '.mjs' }),
  outputOptions: { exports: 'named' },
  dts: false,
  clean: true,
  deps: { neverBundle: ['vite', '@luxojs/client', '@babel/parser', '@babel/traverse', '@babel/types'] },
})
