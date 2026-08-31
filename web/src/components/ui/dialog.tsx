import * as P from '@radix-ui/react-dialog'
import { X } from 'lucide-react'
import { cn } from '@/lib/utils'
export const Dialog=P.Root; export const DialogTrigger=P.Trigger; export const DialogClose=P.Close; export const DialogTitle=P.Title
export function DialogContent({className,children,closeLabel='Close',overlayClassName,...props}:P.DialogContentProps&{closeLabel?:string;overlayClassName?:string}){return <P.Portal><P.Overlay className={cn('dialog-overlay',overlayClassName)}/><P.Content className={cn('dialog-content',className)} {...props}>{children}<P.Close className="dialog-close" aria-label={closeLabel}><X size={15}/></P.Close></P.Content></P.Portal>}
