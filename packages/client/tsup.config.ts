import { defineConfig } from 'tsup'

export default defineConfig({
  entry: ['src/index.ts'],
  format: ['cjs', 'esm'],
  dts: { compilerOptions: { ignoreDeprecations: '6.0' } },
  clean: true,
  external: ['typescript'],
})
