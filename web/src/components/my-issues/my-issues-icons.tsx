import type { SVGProps } from 'react'

type IconProps = SVGProps<SVGSVGElement>

export { FilterIcon, DisplayIcon, ChevronRightIcon } from '@/components/ui/view-action-icons'

export function DetailsIcon({ open = false, ...props }: IconProps & { open?: boolean }) {
  return <svg width="16" height="16" viewBox="0 0 16 16" aria-hidden="true" {...props}><path fill="currentColor" fillRule="evenodd" d="M4.25 2C2.45508 2 1 3.45508 1 5.25V10.75C1 12.5449 2.45508 14 4.25 14H11.75C13.5449 14 15 12.5449 15 10.75V5.25C15 3.45508 13.5449 2 11.75 2H4.25ZM2.5 5.5C2.5 4.39543 3.39543 3.5 4.5 3.5H11.5C12.6046 3.5 13.5 4.39543 13.5 5.5V10.5C13.5 11.6046 12.6046 12.5 11.5 12.5H4.5C3.39543 12.5 2.5 11.6046 2.5 10.5V5.5Z"/><rect x={open ? 8 : 10} y="5" width={open ? 3.5 : 1.5} height="6" rx=".75" fill="currentColor" style={{ transition: 'x 250ms, width 250ms' }}/></svg>
}

export function SearchIcon(props: IconProps) {
  return <svg width="14" height="14" viewBox="0 0 16 16" aria-hidden="true" {...props}><circle cx="7" cy="7" r="4.75" fill="none" stroke="currentColor" strokeWidth="1.5"/><path d="m10.5 10.5 3 3" fill="none" stroke="currentColor" strokeLinecap="round" strokeWidth="1.5"/></svg>
}

/** The reference app's view-tab overflow chevron (13×9, drawn at 8×9 on phones). */
export function TabsOverflowIcon(props: IconProps) {
  return <svg width="13" height="9" viewBox="0 0 13 9" aria-hidden="true" fill="currentColor" {...props}><path d="M10.1611 0.314094L5.99463 4.48054L1.82819 0.314094C1.4094 -0.104698 0.732886 -0.104698 0.314094 0.314094C-0.104698 0.732886 -0.104698 1.4094 0.314094 1.82819L5.24295 6.75705C5.66175 7.17584 6.33825 7.17584 6.75705 6.75705L11.6859 1.82819C12.1047 1.4094 12.1047 0.732886 11.6859 0.314094C11.2671 -0.0939598 10.5799 -0.104698 10.1611 0.314094Z"/></svg>
}
