import * as P from '@radix-ui/react-dropdown-menu'
import { ChevronRight } from 'lucide-react'
import { cn } from '@/lib/utils'
import { CheckboxMark } from './checkbox-mark'
export const DropdownMenu=P.Root; export const DropdownMenuTrigger=P.Trigger
export function DropdownMenuContent({className,sideOffset=6,...props}:P.DropdownMenuContentProps){return <P.Portal><P.Content sideOffset={sideOffset} className={cn('menu-content',className)} {...props}/></P.Portal>}
export function DropdownMenuItem({className,...props}:P.DropdownMenuItemProps){return <P.Item className={cn('menu-item',className)} {...props}/>}
export function DropdownMenuCheckboxItem({children,checked,...props}:P.DropdownMenuCheckboxItemProps){return <P.CheckboxItem className="menu-item" checked={checked} {...props}><span className="menu-indicator"><P.ItemIndicator><CheckboxMark/></P.ItemIndicator></span>{children}</P.CheckboxItem>}
export const DropdownMenuSeparator=(props:P.DropdownMenuSeparatorProps)=><P.Separator className="menu-separator" {...props}/>
export const DropdownMenuLabel=(props:P.DropdownMenuLabelProps)=><P.Label className="menu-label" {...props}/>
export const DropdownMenuSub=P.Sub
export const DropdownMenuSubTrigger=({children,...props}:P.DropdownMenuSubTriggerProps)=><P.SubTrigger className="menu-item" {...props}>{children}<ChevronRight size={13} className="menu-chevron"/></P.SubTrigger>
export const DropdownMenuSubContent=(props:P.DropdownMenuSubContentProps)=><P.Portal><P.SubContent className="menu-content" {...props}/></P.Portal>
