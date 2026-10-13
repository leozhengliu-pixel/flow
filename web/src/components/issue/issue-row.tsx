import { UserAvatar } from '@/components/ui/user-avatar'

export function Avatar({name,avatarUrl}:{name:string;avatarUrl?:string}) {
  return <UserAvatar className="avatar" name={name} avatarUrl={avatarUrl}/>
}
