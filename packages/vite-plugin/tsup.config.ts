import { defineConfig } from 'tsup'

export default defineConfig({
  entry: ['src/index.ts'],
  format: ['cjs', 'esm'],
  dts: false,
  clean: true,
  external: ['vite', '@luxojs/client', '@babel/parser', '@babel/traverse', '@babel/types'],
})
