import { version as typescriptVersion } from 'typescript'
import type { API, NodeHandle, Project } from 'typescript/unstable/async'
import type { SelectionCandidate } from './analyzer'

function declarationKey(node: NodeHandle): string {
  return `${node.path}:${node.index}:${node.kind}`
}

/** Serializes state changes and releases waiters even when an operation fails. */
export class BuildOwner {
  private tail: Promise<void> = Promise.resolve()

  async run<T>(action: () => Promise<T>): Promise<T> {
    const previous = this.tail
    let release!: () => void
    this.tail = new Promise<void>(resolve => { release = resolve })
    await previous
    try {
      return await action()
    } finally {
      release()
    }
  }
}

/** Owns one build-time compiler; no identity checks enter generated requests. */
export class CallIdentity {
  private api?: API
  private readonly sources = new Map<string, string>()
  private readonly owner = new BuildOwner()
  private closed = false
  private activeFile?: string
  private clientOpen = false

  constructor(private readonly root: string, private readonly clientFile: string) {}

  async identify(file: string, code: string, calls: readonly SelectionCandidate[]): Promise<ReadonlySet<number>> {
    return this.owner.run(async () => {
      if (this.closed) throw new Error('[luxo] The selection compiler is closed')
      try {
        const api = await this.compiler()
        await this.releaseFile()
        this.sources.set(file, code)
        const snapshot = await api.updateSnapshot({
          openFiles: this.clientOpen ? [file] : [file, this.clientFile], fileChanges: { changed: [file] },
        })
        this.activeFile = file
        this.clientOpen = true
        try {
          const project = await snapshot.getDefaultProjectForFile(file)
          if (!project) throw new Error('TypeScript could not create a source project')
          return await this.verify(project, file, calls)
        } finally {
          await snapshot.dispose()
        }
      } catch (cause) {
        const detail = cause instanceof Error ? cause.message : String(cause)
        throw new Error(`[luxo] Cannot resolve SDK call identity in ${file}: ${detail}`, { cause })
      }
    })
  }

  async invalidate(file: string, deleted: boolean): Promise<void> {
    await this.owner.run(async () => {
      this.sources.delete(file)
      if (this.closed || !this.api) return
      await this.releaseFile()
      if (file === this.clientFile && this.clientOpen) {
        const closed = await this.api.updateSnapshot({ closeFiles: [this.clientFile] })
        this.clientOpen = false
        await closed.dispose()
      }
      const snapshot = await this.api.updateSnapshot({
        fileChanges: deleted ? { deleted: [file] } : { changed: [file] },
      })
      await snapshot.dispose()
    })
  }

  async close(): Promise<void> {
    await this.owner.run(async () => {
      this.closed = true
      this.sources.clear()
      const api = this.api
      this.activeFile = undefined
      this.clientOpen = false
      if (api) await this.closeCompiler(api)
    })
  }

  private async closeCompiler(api: API): Promise<void> {
    try {
      await api.close()
    } catch (cause) {
      // Retry cleanup without abandoning the API when its first close rejects.
      try {
        await api.close()
      } catch (cleanup) {
        throw new AggregateError([cause, cleanup], '[luxo] Cannot close the selection compiler')
      }
      this.api = undefined
      throw cause
    }
    this.api = undefined
  }

  private async releaseFile(): Promise<void> {
    if (!this.api || !this.activeFile) return
    const file = this.activeFile
    // Open documents retain their text; close before re-opening with new input.
    const snapshot = await this.api.updateSnapshot({ closeFiles: [file] })
    this.activeFile = undefined
    await snapshot.dispose()
  }

  private async compiler(): Promise<API> {
    if (this.api) return this.api
    if (typescriptVersion !== '7.0.2') {
      throw new Error(`Automatic selections require validated TypeScript 7.0.2; received ${typescriptVersion}`)
    }
    const { API: NativeAPI } = await import('typescript/unstable/async')
    this.api = new NativeAPI({ cwd: this.root, fs: {
      readFile: file => this.sources.get(file),
      fileExists: file => this.sources.has(file) ? true : undefined,
    } })
    return this.api
  }

  private async verify(project: Project, file: string, calls: readonly SelectionCandidate[]): Promise<ReadonlySet<number>> {
    const source = await project.program.getSourceFile(this.clientFile)
    // A source project with no generated client cannot contain its resolved calls.
    if (!source) return new Set()
    const module = await project.checker.getSymbolAtLocation(source)
    if (!module) throw new Error(`Generated client is not a module: ${this.clientFile}`)
    const exports = await module.getExports()
    const client = [...exports.values()].find(symbol => symbol.name === 'LuxoClient')
    if (!client) throw new Error(`Generated LuxoClient declaration is missing: ${this.clientFile}`)
    const declarations = new Set([...(await client.getMembers()).values()]
      .flatMap(symbol => symbol.declarations.map(declarationKey)))
    const symbols = await project.checker.getSymbolAtPosition(file, calls.map(call => call.memberStart))
    return new Set(calls.filter((_call, index) => {
      const symbol = symbols[index]
      return symbol && symbol.declarations.length > 0 &&
        symbol.declarations.every(node => declarations.has(declarationKey(node)))
    }).map(call => call.memberStart))
  }
}
