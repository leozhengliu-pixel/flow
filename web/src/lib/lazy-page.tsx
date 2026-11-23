import { lazy, useState, type ComponentType } from 'react'
import { reloadForStaleChunk } from './stale-chunk-reload'

/** Share the module request between navigation and speculative preloading. */
export function lazyPage<Module, Name extends keyof Module>(
  loader: () => Promise<Module>,
  exportName: Name,
): Module[Name] extends ComponentType<infer Props> ? ComponentType<Props> & { preload: () => Promise<void> } : never {
  let pending: Promise<{ default: ComponentType<any> }> | undefined
  let ready: ComponentType<any> | undefined
  const load = () => pending ??= (async () => {
    const module = await loader()
    const component = module[exportName]
    if (typeof component !== 'function' && typeof component !== 'object') {
      throw new Error(`Missing lazy page export: ${String(exportName)}`)
    }
    ready = component as ComponentType<any>
    return { default: ready }
  })().catch(error => {
    pending = undefined
    reloadForStaleChunk(error)
    throw error
  })
  const Lazy = lazy(load)
  // A preloaded route mounts directly. An instance that suspended keeps the
  // same lazy component for its lifetime.
  const Page = (props: Record<string, unknown>) => {
    const [Component] = useState(() => ready ?? Lazy)
    return <Component {...props} />
  }
  return Object.assign(Page, { preload: () => load().then(() => undefined) }) as never
}
