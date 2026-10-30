import type { SVGProps } from 'react'

type IconProps = SVGProps<SVGSVGElement>

export { FilterIcon, DisplayIcon, ChevronRightIcon } from '@/components/ui/view-action-icons'

export function DetailsIcon({ open = false, ...props }: IconProps & { open?: boolean }) {
  return <svg width="16" height="16" viewBox="0 0 16 16" aria-hidden="true" {...props}><path fill="currentColor" fillRule="evenodd" d="M4.25 2A3.25 3.25 0 0 0 1 5.25v5.5A3.25 3.25 0 0 0 4.25 14h7.5A3.25 3.25 0 0 0 15 10.75v-5.5A3.25 3.25 0 0 0 11.75 2h-7.5ZM2.5 5.5a2 2 0 0 1 2-2h7a2 2 0 0 1 2 2v5a2 2 0 0 1-2 2h-7a2 2 0 0 1-2-2v-5Z"/><rect x={open ? 8 : 10} y="5" width={open ? 3.5 : 1.5} height="6" rx=".75" fill="currentColor" style={{ transition: 'x 250ms, width 250ms' }}/></svg>
}

export function SearchIcon(props: IconProps) {
  return <svg width="14" height="14" viewBox="0 0 16 16" aria-hidden="true" {...props}><circle cx="7" cy="7" r="4.75" fill="none" stroke="currentColor" strokeWidth="1.5"/><path d="m10.5 10.5 3 3" fill="none" stroke="currentColor" strokeLinecap="round" strokeWidth="1.5"/></svg>
}

/** The reference app's view-tab overflow chevron (13×9, drawn at 8×9 on phones). */
export function TabsOverflowIcon(props: IconProps) {
  return <svg width="13" height="9" viewBox="0 0 13 9" aria-hidden="true" fill="currentColor" {...props}><path d="M10.1611 0.314094L5.99463 4.48054L1.82819 0.314094C1.4094 -0.104698 0.732886 -0.104698 0.314094 0.314094C-0.104698 0.732886 -0.104698 1.4094 0.314094 1.82819L5.24295 6.75705C5.66175 7.17584 6.33825 7.17584 6.75705 6.75705L11.6859 1.82819C12.1047 1.4094 12.1047 0.732886 11.6859 0.314094C11.2671 -0.0939598 10.5799 -0.104698 10.1611 0.314094Z"/></svg>
}
