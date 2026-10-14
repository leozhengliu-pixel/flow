import {afterEach,expect,it,vi} from 'vitest'
import {makeBootstrap,makeIssue,backlog,started} from '@/test/fixtures'
import type {UserSettings} from '@/types/flow'
import {configuredIssueBranch,copyIssueForWork,issueWorkPrompt} from './issue-work-actions'
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
it('copies a prompt, moves the issue to started with the coding-tool preference, and skips the move with Option',async()=>{
  const writeText=vi.fn().mockResolvedValue(undefined)
  vi.stubGlobal('navigator',{clipboard:{writeText}})
  const issue=makeIssue({state:backlog})
  const data=makeBootstrap({userSettings:{'user-1':{codingToolMoveStarted:true} as UserSettings},states:[{...started,id:'local',teamId:issue.team.id}]})
  const update=vi.fn().mockResolvedValue(undefined)
  await expect(copyIssueForWork('prompt','prompt',issue,data,update)).resolves.toMatchObject({id:'local'})
  expect(update).toHaveBeenCalledWith({stateId:'local'})
  update.mockClear()
  await expect(copyIssueForWork('prompt','prompt',issue,data,update,{skipMove:true})).resolves.toBeUndefined()
  expect(update).not.toHaveBeenCalled()
  expect(writeText).toHaveBeenCalledTimes(2)
})
it('describes the issue with its team and parent in the prompt',()=>{
  const parent=makeIssue({id:'parent-1',identifier:'TST-9',title:'Parent work',description:'Line one\n\nLine two'})
  const issue=makeIssue({parentId:'parent-1',description:'Body'})
  const data=makeBootstrap({issues:[parent,issue]})
  const prompt=issueWorkPrompt(issue,data,'https://flow.test/i/TST-1')
  expect(prompt.startsWith(`Work on Flow issue ${issue.identifier}:\n\nSuggested branch name: ${configuredIssueBranch(issue,data)}\n\n<issue identifier="${issue.identifier}">`)).toBe(true)
  expect(prompt).toContain(`<team name="${issue.team.name}"/>\n<parent-issue identifier="TST-9">\n<id>parent-1</id>\n<title>Parent work</title>\n<description>\nLine one\n\nLine two\n</description>\n</parent-issue>\n</issue>`)
})
