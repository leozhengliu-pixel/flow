import * as P from '@radix-ui/react-dialog'
import { X } from 'lucide-react'
import { cn } from '@/lib/utils'
export const Dialog=P.Root; export const DialogTrigger=P.Trigger; export const DialogClose=P.Close; export const DialogTitle=P.Title
export function DialogContent({className,children,...props}:P.DialogContentProps){return <P.Portal><P.Overlay className="dialog-overlay"/><P.Content className={cn('dialog-content',className)} {...props}>{children}<P.Close className="dialog-close" aria-label="Close"><X size={15}/></P.Close></P.Content></P.Portal>}
