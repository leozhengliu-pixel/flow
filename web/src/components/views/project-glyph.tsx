import type { CSSProperties } from 'react'
import { ViewGlyph } from './view-icon-picker'
import { normalizeProjectIcon } from './project-icon'

/**
 * A project's own icon and colour (what the icon picker saves). Use this
 * wherever a project is shown so a changed icon appears everywhere, not just
 * on the project page.
 */
export function ProjectGlyph({ className, project, size = 16, style }: { className?: string; project?: { icon?: string; color?: string }; size?: number; style?: CSSProperties }) {
  return <ViewGlyph className={className} color={project?.color} icon={normalizeProjectIcon(project?.icon)} style={size === 16 ? style : { width: size, height: size, flexBasis: size, ...style }}/>
}
