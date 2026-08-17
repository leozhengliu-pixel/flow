import { AlertCircle, FileQuestion, LoaderCircle, RotateCcw } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
export function LoadingState(){return <div className="state-fill"><LoaderCircle className="spin" size={18}/><span>Loading...</span></div>}
export function SkeletonRows({count=6}:{count?:number}){return <div className="skeleton-rows">{Array.from({length:count}).map((_,i)=><div className="skeleton-row" key={i}><Skeleton className="sk-icon"/><Skeleton className="sk-id"/><Skeleton className="sk-title"/><Skeleton className="sk-avatar"/></div>)}</div>}
export function EmptyState({title='No issues',description='Issues matching this view will appear here.'}:{title?:string;description?:string}){return <div className="state-fill"><FileQuestion size={24}/><strong>{title}</strong><span>{description}</span></div>}
export function ErrorState({retry}:{retry:()=>void}){return <div className="state-fill"><AlertCircle size={24}/><strong>Unable to load workspace</strong><span>The API did not return Flow data.</span><Button variant="outline" onClick={retry}><RotateCcw size={13}/>Retry</Button></div>}
