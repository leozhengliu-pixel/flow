import { expect,it,vi } from 'vitest'
import { updateUserSettings } from './api'
import { persistUserSettings } from './settings-persistence'
import type { UserSettings } from '@/types/flow'
vi.mock('./api',()=>({updateUserSettings:vi.fn()}))

it('serializes edits per user and preserves the workspace after navigation',async()=>{
  let resolve!: (settings:UserSettings)=>void
  vi.mocked(updateUserSettings).mockImplementationOnce(()=>new Promise<UserSettings>(done=>{resolve=done})).mockResolvedValueOnce({userId:'u',firstDay:'Sunday',autoAssign:true} as UserSettings)
  const first=persistUserSettings('workspace-a','u',{firstDay:'Sunday'})
  const second=persistUserSettings('workspace-a','u',{autoAssign:true})
  expect(updateUserSettings).toHaveBeenCalledTimes(1)
  resolve({userId:'u',firstDay:'Sunday'} as UserSettings)
  await first;await second
  expect(updateUserSettings).toHaveBeenNthCalledWith(2,{autoAssign:true},'workspace-a')
})

it('a failed save does not discard the next queued change',async()=>{
  vi.mocked(updateUserSettings).mockRejectedValueOnce(new Error('offline')).mockResolvedValueOnce({userId:'v'} as UserSettings)
  const first=persistUserSettings('workspace-b','v',{firstDay:'Sunday'})
  const second=persistUserSettings('workspace-b','v',{autoAssign:true})
  await expect(first).rejects.toThrow('offline')
  await expect(second).resolves.toMatchObject({userId:'v'})
})
