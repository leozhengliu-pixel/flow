import type { ComponentProps } from 'react'
import { Link, useInRouterContext } from 'react-router-dom'

/** Also usable by isolated previews that do not mount an application router. */
export function AppLink({ href = '', ...props }: ComponentProps<'a'>) {
  const routed = useInRouterContext()
  return routed ? <Link to={href} {...props}/> : <a href={href} {...props}/>
}
