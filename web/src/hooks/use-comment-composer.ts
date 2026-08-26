import { useState } from 'react'
import { toast } from 'sonner'

export function useCommentComposer(onSubmit: (body: string) => Promise<unknown>, errorMessage = 'Could not post comment') {
  const [comment,setComment]=useState(''),[posting,setPosting]=useState(false)
  const submitComment=async()=>{const body=comment.trim();if(!body||posting)return;setPosting(true);try{await onSubmit(body);setComment('')}catch(error){toast.error(errorMessage,{description:error instanceof Error?error.message:undefined})}finally{setPosting(false)}}
  return {comment,posting,setComment,submitComment}
}
