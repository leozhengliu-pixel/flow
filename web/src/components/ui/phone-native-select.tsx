import { ChevronDown } from 'lucide-react'
import type { SelectHTMLAttributes } from 'react'
import { cn } from '@/lib/utils'

/** Native select with the compact phone chevron used by phone settings controls. */
export function PhoneNativeSelect({ className, children, ...props }: SelectHTMLAttributes<HTMLSelectElement>) {
  return <span className="phone-native-select-wrap">
    <select {...props} className={cn('phone-native-select', className)}>{children}</select>
    <ChevronDown aria-hidden="true" className="phone-native-select-chevron"/>
  </span>
}
