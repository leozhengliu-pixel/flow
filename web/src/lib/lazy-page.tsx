import { lazy, type ComponentType, type LazyExoticComponent } from 'react'

/** Load route-level components only when their page is first visited. */
export function lazyPage<Module, Name extends keyof Module>(
  loader: () => Promise<Module>,
  exportName: Name,
): Module[Name] extends ComponentType<infer Props> ? LazyExoticComponent<ComponentType<Props>> : never {
  return lazy(async () => {
    const module = await loader()
    const component = module[exportName]
    if (typeof component !== 'function' && typeof component !== 'object') {
      throw new Error(`Missing lazy page export: ${String(exportName)}`)
    }
    return { default: component as ComponentType<any> }
  }) as never
}
