import type { ReactNode } from 'react'
import { AgentInlineFile } from './agent-inline-file'
import { AgentInlineImage } from './agent-inline-image'
import { AgentInlineVideo } from './agent-inline-video'
import styles from './agent-content-blocks.module.css'
import { splitAgentBlocks } from './agent-content-model'

/** A user message with its inline blocks: text through `renderText`, blocks as in the reference app's sent bubble. */
export function AgentContentWithBlocks({ content, renderText }: { content: string; renderText: (text: string) => ReactNode }) {
  const parts = splitAgentBlocks(content)
  if (!parts.some(part => part.type !== 'text')) return <>{renderText(content)}</>
  return <div className={styles.sent}>
    {parts.map((part, index) => {
      if (part.type === 'text') return <div className={styles.text} key={index}>{renderText(part.text)}</div>
      const key = `${part.src}-${index}`
      return <div className={styles.block} data-kind={part.type} key={key}>
        {part.type === 'image' ? <AgentInlineImage alt={part.alt} src={part.src}/>
          : part.type === 'video' ? <AgentInlineVideo name={part.name} src={part.src}/>
          : <AgentInlineFile contentType={part.contentType} name={part.name} size={part.size} src={part.src}/>}
      </div>
    })}
  </div>
}
