import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest'
import { Composer } from './composer'
import { I18nProvider } from '@/i18n/i18n'
import { TooltipProvider } from '@/components/ui/tooltip'
import { viewer } from '@/test/fixtures'

const originalRects=Object.getOwnPropertyDescriptor(Range.prototype,'getClientRects')
const originalRect=Object.getOwnPropertyDescriptor(Range.prototype,'getBoundingClientRect')
beforeAll(()=>{
  Object.defineProperty(Range.prototype,'getClientRects',{configurable:true,value:()=>[]})
  Object.defineProperty(Range.prototype,'getBoundingClientRect',{configurable:true,value:()=>new DOMRect(0,0,1,1)})
})
afterAll(()=>{
  if(originalRects)Object.defineProperty(Range.prototype,'getClientRects',originalRects)
  else Reflect.deleteProperty(Range.prototype,'getClientRects')
  if(originalRect)Object.defineProperty(Range.prototype,'getBoundingClientRect',originalRect)
  else Reflect.deleteProperty(Range.prototype,'getBoundingClientRect')
})

describe('comment application mentions',()=>{
  it('submits a stable application ID in a structured mention node',async()=>{
    const user=userEvent.setup(),submit=vi.fn().mockResolvedValue(undefined)
    const app={...viewer,id:'app-fixture',name:'worker',displayName:'Build agent',app:true,appScopes:['app:mentionable']}
    render(<I18nProvider><TooltipProvider><Composer users={[app]} onSubmit={submit}/></TooltipProvider></I18nProvider>)
    await user.click(screen.getByRole('button',{name:'Mention'}))
    await user.click(await screen.findByRole('option',{name:/Build agent/}))
    await user.click(screen.getByRole('button',{name:'Submit comment'}))
    await waitFor(()=>expect(submit).toHaveBeenCalledWith('@Build agent',expect.objectContaining({type:'doc'})))
    expect(JSON.stringify(submit.mock.calls[0][1])).toContain('app-fixture')
    expect(JSON.stringify(submit.mock.calls[0][1])).toContain('"type":"mention"')
  })
  it('does not suggest applications without mention capability',async()=>{
    const user=userEvent.setup()
    render(<I18nProvider><TooltipProvider><Composer users={[{...viewer,app:true,appScopes:['read']}]} onSubmit={vi.fn()}/></TooltipProvider></I18nProvider>)
    await user.click(screen.getByRole('button',{name:'Mention'}))
    expect(screen.queryByRole('option')).toBeNull()
  })
  it('restores the stable mention identity from a saved draft',async()=>{
    const submit=vi.fn().mockResolvedValue(undefined)
    const bodyData={type:'doc',content:[{type:'paragraph',content:[{type:'mention',attrs:{id:'app-restored',label:'Build agent'}}]}]}
    const draft={id:'local:mention',userId:viewer.id,type:'comment' as const,resourceId:'issue-restored',title:'Draft',body:'@Build agent',metadata:{bodyData},createdAt:new Date().toISOString(),updatedAt:new Date().toISOString()}
    render(<I18nProvider><TooltipProvider><Composer draftType="comment" draftResourceId="issue-restored" drafts={[draft]} onSubmit={submit}/></TooltipProvider></I18nProvider>)
    await userEvent.click(screen.getByRole('button',{name:'Submit comment'}))
    await waitFor(()=>expect(submit).toHaveBeenCalledWith('@Build agent',expect.objectContaining({type:'doc'})))
    expect(JSON.stringify(submit.mock.calls[0][1])).toContain('app-restored')
  })
})
