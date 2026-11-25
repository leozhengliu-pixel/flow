import type { ReactElement } from 'react'
import { createRoot, type Root } from 'react-dom/client'

/** Renders a React element into a node view's DOM and hands back what the node view needs to update and tear it down. */
export function mountReact(container: HTMLElement) {
  const root: Root = createRoot(container)
  return { render: (element: ReactElement) => root.render(element), unmount: () => queueMicrotask(() => root.unmount()) }
}
