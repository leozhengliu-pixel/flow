import * as P from '@radix-ui/react-context-menu'
import { cn } from '@/lib/utils'
export const ContextMenu=P.Root; export const ContextMenuTrigger=P.Trigger
export function ContextMenuContent({className,...props}:P.ContextMenuContentProps){return <P.Portal><P.Content className={cn('menu-content',className)} {...props}/></P.Portal>}
export function ContextMenuItem({className,...props}:P.ContextMenuItemProps){return <P.Item className={cn('menu-item',className)} {...props}/>}
export const ContextMenuSeparator=(props:P.ContextMenuSeparatorProps)=><P.Separator className="menu-separator" {...props}/>
