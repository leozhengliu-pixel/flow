import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeIssue, viewer } from '@/test/fixtures'
import { fetchIssueRecordContext, updateComment } from '@/lib/api'
import { PagedActivityTimeline } from './paged-activity-timeline'

vi.mock('@/lib/api', () => ({fetchIssueRecordContext:vi.fn(),updateComment:vi.fn(),createComment:vi.fn(),deleteComment:vi.fn(),toggleCommentReaction:vi.fn()}))
vi.mock('./activity-timeline', () => ({ ActivityTimeline: ({comments,onEdit}: {comments:{id:string;body:string}[];onEdit:(id:string,body:string)=>Promise<void>}) => <div>{comments.map(comment=><button key={comment.id} onClick={()=>void onEdit(comment.id,'Edited history comment')}>{comment.body}</button>)}</div> }))

it('loads older pages without accumulating history and edits the displayed page', async () => {
  const recent = {id:'recent',version:1,user:viewer,body:'Recent comment',createdAt:'2026-09-09',reactions:{}}
  const old = {...recent,id:'old',body:'Older comment'}
  vi.mocked(fetchIssueRecordContext).mockResolvedValueOnce({issue:makeIssue(),relatedIssues:[],comments:[old],activities:[]})
    .mockResolvedValueOnce({issue:makeIssue(),relatedIssues:[],comments:[recent],activities:[],commentsCursor:'older'})
  vi.mocked(updateComment).mockResolvedValue({...old,body:'Edited history comment',version:2})
  const onEdit=vi.fn()
  render(<I18nProvider><PagedActivityTimeline issueId="issue-1" cursors={{commentsCursor:'older'}} comments={[recent]} events={[]} viewerId={viewer.id} onEdit={onEdit} onReply={vi.fn()} onDelete={vi.fn()} onReaction={vi.fn()}/></I18nProvider>)
  await userEvent.click(screen.getByRole('button',{name:'Older activity'}))
  await screen.findByText('Older comment')
  expect(screen.queryByText('Recent comment')).not.toBeInTheDocument()
  expect(fetchIssueRecordContext).toHaveBeenCalledWith('issue-1',expect.any(AbortSignal),{commentsCursor:'older',activitiesCursor:undefined})
  await userEvent.click(screen.getByText('Older comment'))
  await screen.findByText('Edited history comment')
  expect(updateComment).toHaveBeenCalledWith('issue-1','old','Edited history comment',undefined,1)
  expect(onEdit).not.toHaveBeenCalled()
  await userEvent.click(screen.getByRole('button',{name:'Newer activity'}))
  await waitFor(()=>expect(screen.getByText('Recent comment')).toBeInTheDocument())
  expect(screen.queryByText('Edited history comment')).not.toBeInTheDocument()
})
