import * as Dialog from '@radix-ui/react-dialog'
import { useState } from 'react'
import { toast } from 'sonner'
import { AppLink } from '@/components/ui/app-link'
import { PropertyMenu } from '@/components/property/property-menu'
import { SlackIcon } from '@/components/issue/issue-icons'
import { useI18n } from '@/i18n/i18n'
import type { IntegrationConnection, Project } from '@/types/flow'
import type { ProjectMutationInput } from '@/components/projects-page/projects-page'

export function ProjectSlackDialog({open,onOpenChange,project,connections,onSave}:{open:boolean;onOpenChange:(open:boolean)=>void;project:Project;connections:IntegrationConnection[];onSave:(input:ProjectMutationInput)=>Promise<void>}) {
  const {t} = useI18n()
  const [saving,setSaving] = useState(false)
  const channels = [...new Set(connections.filter(item => item.provider.toLowerCase() === 'slack' && item.status === 'connected').flatMap(item => item.channels))]
  const save = async (channel:string) => {setSaving(true);try{await onSave({slackChannelId:channel,slackChannelName:channel});onOpenChange(false)}catch(error){toast.error(error instanceof Error ? error.message : t('Could not update Slack channel'))}finally{setSaving(false)}}
  return <Dialog.Root open={open} onOpenChange={onOpenChange}><Dialog.Portal><Dialog.Overlay data-flow-motion="backdrop" className="project-detail-page__dialog-overlay"/><Dialog.Content data-flow-motion="dialog" className="project-detail-page__form-dialog project-slack-dialog"><Dialog.Title><SlackIcon size={16}/>{t('Slack notifications for')} <span data-i18n-ignore>{project.name}</span></Dialog.Title><Dialog.Description>{t('Connect a Slack channel to receive notifications about this project.')}</Dialog.Description>{channels.length ? <PropertyMenu label="Slack channel" value={project.slackChannelName || t('Select channel…')} selectedId={project.slackChannelId} options={channels.map(id=>({id,label:id,icon:<SlackIcon size={16}/>,i18nIgnore:true,disabled:saving}))} onChange={save}/> : <AppLink className="project-slack-dialog__connect" href={`/${location.pathname.split('/').filter(Boolean)[0]}/settings/integrations`}>{t('Connect Slack')}</AppLink>}<footer>{project.slackChannelId && <button type="button" disabled={saving} onClick={()=>void save('')}>{t('Disconnect channel')}</button>}<Dialog.Close asChild><button type="button">{t('Close')}</button></Dialog.Close></footer></Dialog.Content></Dialog.Portal></Dialog.Root>
}
