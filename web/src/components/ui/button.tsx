import * as React from 'react'
import { Slot } from '@radix-ui/react-slot'
import { cva, type VariantProps } from 'class-variance-authority'
import { cn } from '@/lib/utils'

const variants = cva('flow-button', { variants: { variant: { default:'flow-button-default', ghost:'flow-button-ghost', outline:'flow-button-outline' }, size: { default:'h-7 px-2.5', icon:'size-7 p-0', sm:'h-6 px-2' } }, defaultVariants:{variant:'default',size:'default'} })
export interface ButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement>, VariantProps<typeof variants> { asChild?: boolean }
export function Button({className,variant,size,asChild,...props}:ButtonProps){const Comp=asChild?Slot:'button';return <Comp className={cn(variants({variant,size}),className)} {...props}/>}
