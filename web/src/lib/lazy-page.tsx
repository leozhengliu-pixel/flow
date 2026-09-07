import { lazy, type ComponentType, type LazyExoticComponent } from 'react'

/** Share the module request between navigation and speculative preloading. */
export function lazyPage<Module, Name extends keyof Module>(
  loader: () => Promise<Module>,
  exportName: Name,
): Module[Name] extends ComponentType<infer Props> ? LazyExoticComponent<ComponentType<Props>> & { preload: () => Promise<void> } : never {
  let pending: Promise<{ default: ComponentType<any> }> | undefined
  const load = () => pending ??= (async () => {
    const module = await loader()
    const component = module[exportName]
    if (typeof component !== 'function' && typeof component !== 'object') {
      throw new Error(`Missing lazy page export: ${String(exportName)}`)
    }
    return { default: component as ComponentType<any> }
  })().catch(error => {
    pending = undefined
    throw error
  })
  return Object.assign(lazy(load), { preload: () => load().then(() => undefined) }) as never
}
