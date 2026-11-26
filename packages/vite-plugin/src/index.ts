import type { HmrContext, ModuleNode, Plugin, ViteDevServer } from 'vite'
import { hash } from 'node:crypto'
import { existsSync, readFileSync } from 'node:fs'
import { resolve, sep } from 'node:path'
import MagicString from 'magic-string'
import { analyzeVerifiedEdits, type TextEdit } from './analyzer'
import { BuildOwner, CallIdentity } from './call-identity'
import { generateTypes } from './codegen'
import type { LuxoSchema } from '@luxojs/client'

export interface LuxoPluginOptions {
  /** Luvia endpoint URL for schema introspection */
  endpoint?: string
  /** Introspection key */
  introspectionKey?: string
  /** Path to local schema file (alternative to endpoint) */
  schema?: string
  /** Output directory for generated types (default: src/luxo) */
  outDir?: string
}

/** Generate an SDK without starting Vite; return the loaded schema for field analysis. */
export async function generateSDK(options: LuxoPluginOptions = {}, projectRoot = process.cwd()): Promise<LuxoSchema> {
  const { schema } = await loadSchema(options, localSchemaFile(options, projectRoot))
  await generateTypes(schema, resolve(projectRoot, options.outDir || 'src/luxo'))
  return schema
}

/** Luxo Vite plugin — compile-time field tracking + type generation */
export function luxo(options: LuxoPluginOptions = {}): Plugin {
  const build = new PluginBuild(options)
  return {
    name: 'luxo', enforce: 'pre',
    configResolved: config => build.configure(config.root),
    configureServer: server => build.serve(server),
    buildStart() {
      if (build.schemaFile) this.addWatchFile(build.schemaFile)
      return build.start()
    },
    transform(code, id) {
      const file = normalizePath(id.split('?', 1)[0]!)
      if (!/\.(?:[mc]?ts|tsx)$/.test(file) || file.includes('/node_modules/')) return null
      // The next schema can make this module eligible even if its current transform returns null.
      const schemaFile = build.schemaFile
      return build.transform(code, id, file, schemaFile ? () => this.addWatchFile(schemaFile) : undefined)
    },
    watchChange: (id, change) => build.invalidate(id, change.event),
    handleHotUpdate: context => build.hotUpdate(context),
    buildEnd: error => error ? build.close() : undefined,
    closeBundle: () => build.close(),
  }
}

class PluginBuild {
  private schema: LuxoSchema | null = null
  private root = process.cwd()
  private generatedDir: string
  private identity?: CallIdentity
  private readonly lifecycle = new BuildOwner()
  private readonly callers = new Set<string>()
  private readonly sources = new Set<string>()
  private server?: ViteDevServer
  private generatedLocal?: LocalSchema
  private readonly generatedFiles = new Set<string>()
  private readonly pendingWrites = new Map<string, string>()
  private stopWatchingDeletedSchema?: () => void
  private activeLocal = false
  schemaFile?: string

  constructor(private readonly options: LuxoPluginOptions) {
    this.generatedDir = resolve(this.root, options.outDir || 'src/luxo')
    this.schemaFile = localSchemaFile(options, this.root)
  }

  configure(root: string): void {
    if (root !== this.root) {
      this.generatedLocal = undefined
      this.generatedFiles.clear()
      this.pendingWrites.clear()
    }
    this.root = root
    this.generatedDir = resolve(root, this.options.outDir || 'src/luxo')
    this.schemaFile = localSchemaFile(this.options, root)
  }

  serve(server: ViteDevServer): void {
    this.server = server
    if (this.schemaFile) server.watcher.add(this.schemaFile)
  }

  async start(): Promise<void> {
    await this.lifecycle.run(async () => {
      // Load schema from endpoint or local file.
      this.schema = null
      this.callers.clear()
      await this.closeIdentity()
      this.activeLocal = false
      const loaded = await loadSchema(this.options, this.schemaFile, this.generatedLocal)
      await this.generate(loaded)
      this.install(loaded.schema)
    })
  }

  async transform(code: string, id: string, sourceID: string, watchSchema?: () => void): Promise<ReturnType<typeof renderMappedSource> | null> {
    if (isInsideDirectory(sourceID, this.generatedDir)) {
      if (!this.server) watchSchema?.()
      return null
    }
    if (!this.schema || !this.identity) return null
    // Analyze field access and inject $select.
    const current = this.identity
    const edits = await analyzeVerifiedEdits(code, id, this.schema, calls => {
      this.callers.add(sourceID)
      return current.identify(sourceID, code, calls)
    }, watchSchema && (() => {
      if (this.server) this.sources.add(sourceID)
      else watchSchema()
    }))
    if (this.identity !== current) throw new Error(`[luxo] Schema changed during selection analysis: ${id}`)
    return edits ? renderMappedSource(code, sourceID, edits) : null
  }

  async invalidate(file: string, event: 'create' | 'update' | 'delete'): Promise<void> {
    const schemaFile = this.schemaFile
    file = normalizePath(file)
    if (schemaFile === file) {
      const server = this.server
      if (server && this.activeLocal && event === 'delete') this.watchDeletedSchema(schemaFile, server)
      if (event !== 'delete') this.cancelDeletedSchemaWatch()
      if (server && (event !== 'update' || server.config.server.hmr === false)) {
        await this.refreshWatchedSchema(schemaFile, server, event !== 'update')
      }
      return
    }
    if (this.pendingWrites.has(file)) {
      const ownWrite = event !== 'delete' && this.matchesPendingWrite(file)
      if (!ownWrite || event !== 'update' || this.server?.config.server.hmr === false) {
        this.pendingWrites.delete(file)
      }
      if (ownWrite) return
    }
    const deleted = event === 'delete'
    await this.identity?.invalidate(file, deleted)
    if (deleted) {
      this.callers.delete(file)
      this.sources.delete(file)
    }
    // Type-only dependencies can change selections without changing emitted imports.
    for (const module of this.callerModules()) this.server!.moduleGraph.invalidateModule(module)
  }

  async hotUpdate(context: HmrContext): Promise<ModuleNode[]> {
    const file = normalizePath(context.file)
    if (this.pendingWrites.has(file)) {
      const ownWrite = this.matchesPendingWrite(file)
      this.pendingWrites.delete(file)
      // The schema update already includes the generated modules.
      if (ownWrite) return []
    }
    if (this.schemaFile !== file) return [...new Set([...context.modules, ...this.callerModules()])]
    if (!this.activeLocal) return context.modules
    // Wait for editor writes; refresh still decodes the original bytes as strict UTF-8.
    try {
      await context.read()
    } catch {
      // Refresh reports a failed source read with the configured path.
    }
    return [...new Set([...context.modules, ...await this.refreshLocal(file)])]
  }

  private callerModules(): ModuleNode[] {
    return this.modulesByFile(this.callers)
  }

  private modulesByFile(files: ReadonlySet<string>): ModuleNode[] {
    if (!this.server) return []
    return [...files].flatMap(file => [...this.server!.moduleGraph.getModulesByFile(file) ?? []])
  }

  private watchDeletedSchema(file: string, server: ViteDevServer): void {
    if (this.stopWatchingDeletedSchema) return
    // Chokidar can lose a recreated file after unlink. Poll only while that exact path is missing.
    let timer: ReturnType<typeof setTimeout> | undefined
    const resume = () => {
      if (this.stopWatchingDeletedSchema !== stop || this.server !== server) return
      if (!existsSync(file)) {
        timer = setTimeout(resume, 100).unref()
        return
      }
      this.stopWatchingDeletedSchema = undefined
      server.watcher.unwatch(file).add(file)
      void this.refreshWatchedSchema(file, server, true)
    }
    const stop = () => clearTimeout(timer)
    this.stopWatchingDeletedSchema = stop
    queueMicrotask(resume)
  }

  private cancelDeletedSchemaWatch(): void {
    this.stopWatchingDeletedSchema?.()
    this.stopWatchingDeletedSchema = undefined
  }

  private async refreshWatchedSchema(file: string, server: ViteDevServer, notify: boolean): Promise<void> {
    const modules = await this.refreshLocal(file)
    // Vite's legacy hot-update hook only sees changes, so recreated schemas need this dispatch.
    if (this.server === server && notify && modules.length && server.config.server.hmr !== false) {
      server.ws.send({ type: 'full-reload', path: '*' })
    }
  }

  private refreshLocal(file: string): Promise<ModuleNode[]> {
    return this.lifecycle.run(async () => {
      const server = this.server
      if (!server || !this.activeLocal) return []
      try {
        const local = await readLocalSchema(file, this.generatedLocal)
        if (local === this.generatedLocal && this.schema === local.schema && this.hasGeneratedFiles()) return []
        await this.generate({ schema: local.schema, local })
        await this.closeIdentity()
        this.install(local.schema)
        this.callers.clear()
        const modules = [...new Set([
          ...this.modulesByFile(this.sources), ...this.modulesByFile(this.generatedFiles),
        ])]
        const seen = new Set<ModuleNode>()
        for (const module of modules) server.moduleGraph.invalidateModule(module, seen)
        return modules
      } catch (cause) {
        if (!existsSync(file)) this.watchDeletedSchema(file, server)
        server.config.logger.error(`[luxo] Cannot refresh schema file ${file}: ${String(cause)}`)
        return []
      }
    })
  }

  private async generate({ schema, local }: LoadedSchema): Promise<void> {
    this.activeLocal = Boolean(local)
    if (local && local === this.generatedLocal && this.hasGeneratedFiles()) return
    this.generatedLocal = undefined
    this.generatedFiles.clear()
    this.pendingWrites.clear()
    if (local) {
      try {
        await generateTypes(schema, this.generatedDir, {
          onWrite: (file, contents) => {
            file = normalizePath(file)
            this.generatedFiles.add(file)
            if (this.server) this.pendingWrites.set(file, hash('sha256', contents))
          },
        })
      } catch (cause) {
        this.pendingWrites.clear()
        throw cause
      }
    } else {
      await generateTypes(schema, this.generatedDir)
    }
    this.generatedLocal = local
  }

  private hasGeneratedFiles(): boolean {
    for (const file of this.generatedFiles) if (!existsSync(file)) return false
    return true
  }

  private matchesPendingWrite(file: string): boolean {
    try {
      return hash('sha256', readFileSync(file)) === this.pendingWrites.get(file)
    } catch {
      return false
    }
  }

  private install(schema: LuxoSchema): void {
    this.schema = schema
    this.identity = new CallIdentity(this.root, resolve(this.generatedDir, 'client.ts'))
  }

  async close(): Promise<void> {
    this.cancelDeletedSchemaWatch()
    await this.lifecycle.run(async () => {
      this.cancelDeletedSchemaWatch()
      this.schema = null
      this.callers.clear()
      this.sources.clear()
      this.pendingWrites.clear()
      this.server = undefined
      await this.closeIdentity()
    })
  }

  private async closeIdentity(): Promise<void> {
    const current = this.identity
    this.identity = undefined
    try {
      await current?.close()
    } catch (cause) {
      this.identity = current
      throw cause
    }
  }
}

function renderMappedSource(code: string, source: string, edits: readonly TextEdit[]) {
  const mapped = new MagicString(code)
  // Selection analysis only inserts text; preserve the string renderer's same-offset order.
  for (const edit of [...edits].sort((a, b) => b.start - a.start)) {
    mapped.prependLeft(edit.start, edit.replacement)
  }
  const map = mapped.generateMap({ source, hires: true })
  return { code: mapped.toString(), map: { ...map, sourcesContent: [code] } }
}

function isInsideDirectory(file: string, directory: string): boolean {
  const normalizedDirectory = normalizePath(directory).replace(/\/$/, '')
  return file === normalizedDirectory || file.startsWith(`${normalizedDirectory}/`)
}

interface LocalSchema {
  schema: LuxoSchema
  bytes: Buffer
}

interface LoadedSchema {
  schema: LuxoSchema
  local?: LocalSchema
}

function localSchemaFile(options: LuxoPluginOptions, root: string): string | undefined {
  return options.schema ? normalizePath(resolve(root, options.schema)) : undefined
}

function normalizePath(path: string): string {
  return sep === '\\' ? path.replaceAll('\\', '/') : path
}

async function readLocalSchema(file: string, previous?: LocalSchema): Promise<LocalSchema> {
  const fs = await import('fs')
  const bytes = fs.readFileSync(file)
  return previous?.bytes.equals(bytes) ? previous : { schema: decodeSchema(bytes), bytes }
}

async function loadSchema(options: LuxoPluginOptions, file?: string, previous?: LocalSchema): Promise<LoadedSchema> {
  const failures: unknown[] = []
  // Try endpoint first
  if (options.endpoint) {
    try {
      return { schema: await fetchSchema(options.endpoint, options.introspectionKey) }
    } catch (e) {
      failures.push(e)
    }
  }

  // Try local schema file
  if (file) {
    try {
      const local = await readLocalSchema(file, previous)
      if (failures.length > 0) console.warn('[luxo] Schema endpoint failed; using the explicitly configured local schema.')
      return { schema: local.schema, local }
    } catch (e) {
      failures.push(new Error(`[luxo] Cannot read schema file ${file}`, { cause: e }))
    }
  }

  throw new AggregateError(failures, '[luxo] No schema available. Configure a working endpoint or schema file.')
}

async function fetchSchema(endpoint: string, key?: string): Promise<LuxoSchema> {
  const url = new URL(endpoint)
  url.searchParams.set('$schema', '')
  url.hash = ''
  const headers: Record<string, string> = {}
  if (key) headers['X-Introspection-Key'] = key
  const response = await fetch(url.toString(), { headers, redirect: 'error' })
  if (!response.ok) throw new Error(`[luxo] Schema endpoint returned HTTP ${response.status}`)
  return decodeSchema(new Uint8Array(await response.arrayBuffer()))
}

/** Decode once, before generation; accept one document BOM, never replacement decoding. */
function decodeSchema(bytes: Uint8Array): LuxoSchema {
  const text = new TextDecoder('utf-8', { fatal: true }).decode(bytes)
  const value: unknown = JSON.parse(text)
  if (!isRecord(value) || !isRecord(value.models) || !isRecord(value.apis)) {
    throw new TypeError('[luxo] Expected a Schema object with models and apis maps')
  }
  return value as unknown as LuxoSchema
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
}

export default luxo
