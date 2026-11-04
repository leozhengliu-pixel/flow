import { createRoot } from 'react-dom/client'
import { UserAvatar } from '../src/components/ui/user-avatar'
import { WorkspaceInitials } from '../src/components/workspace/workspace-initials'

const styles = import.meta.glob('../src/**/*.css', { eager: true, import: 'default', query: '?inline' })
const modules = import.meta.glob('../src/**/*.module.css', { eager: true, import: 'default' }) as Record<string, Record<string, string>>

export function mount(consumers: Array<{ label: string; classes: Array<string | { file: string; key: string }>; parent: string }>) {
  for (const css of Object.values(styles)) {
    const style = document.createElement('style')
    style.textContent = css as string
    document.head.append(style)
  }
  const host = document.createElement('div')
  host.id = 'avatar-consumer-fixture'
  Object.assign(host.style, { position: 'fixed', inset: '0', overflow: 'auto', zIndex: '9999', background: 'var(--bg-panel)' })
  document.body.append(host)
  createRoot(host).render(<>
    {consumers.flatMap(consumer => ['W W', '中 文'].map(name => <div key={`${consumer.label}-${name}`} data-consumer={consumer.label} style={{ display: 'inline-flex', width: 180, height: 60, alignItems: 'center', justifyContent: 'center' }}>
      <div className={consumer.parent}><UserAvatar name={name} className={consumer.classes.map(value => typeof value === 'string' ? value : modules[value.file]?.[value.key] ?? '').join(' ')}/></div>
    </div>))}
    {['WW', 'MM', '中文', '中W', 'i', 'ÅÉ'].flatMap(name => [1, .8].map(scale => <div key={`${name}-${scale}`} data-fit={name} style={{ display: 'inline-grid', width: 100, height: 60, placeItems: 'center', transform: `scale(${scale})` }}>
      <span className="workspace-avatar" style={{ width: 20, height: 20, display: 'grid', placeItems: 'center', fontSize: 11 }}><WorkspaceInitials name={name} icon={name}/></span>
      <UserAvatar name={name.length === 2 ? `${name[0]} ${name[1]}` : name}/>
    </div>))}
  </>)
}
