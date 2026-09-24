import { UserAvatar } from '@/components/ui/user-avatar'

export function Avatar({name}:{name:string}) {
  return <UserAvatar className="avatar" name={name}/>
}
