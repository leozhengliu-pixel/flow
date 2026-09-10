import { expect, it } from 'vitest'
import { makeIssue } from '@/test/fixtures'
import { issueMayMatchQuery } from './paged-issue-invalidation'

it('recognizes workflow type filters as well as concrete status IDs',()=>{
  const issue=makeIssue()
  expect(issueMayMatchQuery(issue,{filter:{field:'status',operator:'in',values:['unstarted','started']}},{teamSettings:{}})).toBe(true)
  expect(issueMayMatchQuery(issue,{filter:{field:'status',values:[issue.state.id]}},{teamSettings:{}})).toBe(true)
  expect(issueMayMatchQuery(issue,{filter:{field:'status',operator:'notIn',values:['started']}},{teamSettings:{}})).toBe(false)
})

it('treats empty top-level ID arrays as unrestricted like the records API',()=>{
  expect(issueMayMatchQuery(makeIssue(),{teamId:[],projectId:[],stateId:[]},{teamSettings:{}})).toBe(true)
})
