import { ChevronRight } from 'lucide-react'
import './submenu-chevron.css'

export function SubmenuChevron() {
  return <span className="flow-submenu-chevron" aria-hidden="true"><span>▶</span><ChevronRight size={14} strokeWidth={1.5}/></span>
}
