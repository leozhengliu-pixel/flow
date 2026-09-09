import {afterEach,expect,it,vi} from 'vitest'
import {makeBootstrap,makeIssue,backlog,started} from '@/test/fixtures'
import type {UserSettings} from '@/types/flow'
import {configuredIssueBranch,copyIssueForWork} from './issue-work-actions'
afterEach(()=>vi.unstubAllGlobals())
it('uses the configured branch format and an employee identifier without email',()=>{
  const issue=makeIssue({assignee:{id:'internal',userId:'EMP-42',name:'张三',displayName:'张三',email:'',active:true,emailVerified:true}})
  const data=makeBootstrap({integrationConnections:[{provider:'github',config:{branchFormat:'username/identifier-title'}}] as never})
  expect(configuredIssueBranch(issue,data)).toBe('emp-42/tst-1-test-issue')
})
it('moves an unstarted issue only after a successful copy and respects the team',async()=>{
  const writeText=vi.fn().mockResolvedValue(undefined)
  vi.stubGlobal('navigator',{clipboard:{writeText}})
  const issue=makeIssue({state:backlog})
  const data=makeBootstrap({userSettings:{'user-1':{gitBranchMoveStarted:true} as UserSettings},states:[{...started,id:'foreign',teamId:'other'},{...started,id:'local',teamId:issue.team.id}]})
  const update=vi.fn().mockResolvedValue(undefined)
  await copyIssueForWork('branch','branch',issue,data,update)
  expect(update).toHaveBeenCalledWith({stateId:'local'})
  writeText.mockRejectedValueOnce(new Error('clipboard denied'));update.mockClear()
  await expect(copyIssueForWork('branch','branch',issue,data,update)).rejects.toThrow('clipboard denied')
  expect(update).not.toHaveBeenCalled()
})
