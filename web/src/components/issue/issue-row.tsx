import { UserAvatar } from '@/components/ui/user-avatar'
import { avatarColor } from './avatar-color'

export function Avatar({id, name, avatarUrl}:{id?:string; name:string;avatarUrl?:string}) {
  return <UserAvatar className="avatar" name={name} avatarUrl={avatarUrl} color={avatarColor(id ?? name)}/>
}
