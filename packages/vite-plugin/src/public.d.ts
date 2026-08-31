import type { Plugin } from 'vite'

export interface LuxoPluginOptions {
  endpoint?: string
  introspectionKey?: string
  schema?: string
  outDir?: string
}

export function luxo(options?: LuxoPluginOptions): Plugin
export default luxo
