import * as Dialog from '@radix-ui/react-dialog'
import { NodeSelection, TextSelection } from '@tiptap/pm/state'
import { NodeViewContent, NodeViewWrapper, useEditorState, type ReactNodeViewProps } from '@tiptap/react'
import { useEffect, useState, type MouseEvent, type ReactNode } from 'react'
import { FlowTooltip, TooltipProvider } from '@/components/ui/tooltip'
import { FlowGlyph, type FlowGlyphName } from '@/components/ui/menu-glyphs'
import { useI18n } from '@/i18n/i18n'
import { toast } from '@/lib/toast'
import { copyDiagramImage, renderDiagram, type DiagramResult } from './diagram-render'
import './diagram-block.css'

/** Keeps the editor's focus and selection while a toolbar control is pressed. */
const keepFocus = (event: MouseEvent) => event.preventDefault()

/** A comment on the whole diagram, handed to the editor's inline-comment flow (set by the description editor). */
export type DiagramComment = (selection: { from: number; to: number; text: string }) => void

const RENDER_DELAY = 250

function ToolbarButton({ label, glyph, disabled, pressed, onPress }: { label: string; glyph: FlowGlyphName; disabled?: boolean; pressed?: boolean; onPress?: () => void }) {
  return (
    <FlowTooltip label={label}>
      <button aria-disabled={disabled || undefined} aria-label={label} aria-pressed={pressed} className="flow-diagram__button" data-disabled={disabled || undefined} onClick={() => { if (!disabled) onPress?.() }} onMouseDown={keepFocus} type="button">
        <FlowGlyph name={glyph} size={14}/>
      </button>
    </FlowTooltip>
  )
}

function Toolbar({ children, className = '' }: { children: ReactNode; className?: string }) {
  return <div className={`flow-diagram__toolbar ${className}`} contentEditable={false} data-i18n-ignore role="toolbar">{children}</div>
}

/** The empty diagram's prompt: three linked boxes over "Your diagram will display here". */
function Placeholder({ label, onActivate }: { label: string; onActivate?: () => void }) {
  return (
    <div className="flow-diagram__placeholder" contentEditable={false} onMouseDown={event => { if (onActivate) { event.preventDefault(); onActivate() } }}>
      <svg aria-hidden fill="none" height="37" viewBox="0 0 65 37" width="65">
        <rect fill="var(--flow-diagram-node)" height="10" rx="2" stroke="var(--flow-diagram-node-border)" strokeWidth=".75" width="17" x="24" y=".5"/>
        <rect fill="var(--flow-diagram-node)" height="10" rx="2" stroke="var(--flow-diagram-node-border)" strokeWidth=".75" width="17" x=".5" y="26.5"/>
        <rect fill="var(--flow-diagram-node)" height="10" rx="2" stroke="var(--flow-diagram-node-border)" strokeWidth=".75" width="17" x="47.5" y="26.5"/>
        <path d="M24 6C14 6 9 14 9 26M41 6c10 0 15 8 15 20" stroke="var(--flow-diagram-line)" strokeDasharray="1.5 2" strokeWidth=".75"/>
      </svg>
      <p>{label}</p>
    </div>
  )
}

function DiagramSvg({ result }: { result: DiagramResult | null }) {
  if (!result || 'error' in result) return null
  // Mermaid's output is sanitized (securityLevel "strict").
  return <div className="flow-diagram__svg" dangerouslySetInnerHTML={{ __html: result.svg }}/>
}

/**
 * A Mermaid diagram, the reference app's way: a code block whose language is Mermaid, shown as the rendered diagram with its source
 * one toggle away. Toolbar (on hover / selection): Add comment, Copy diagram, Show source | Fullscreen; while the source
 * shows: Copy source, Show diagram (or Show error) | Fullscreen, with an error console under the source.
 */
export function DiagramBlockView({ editor, getPos, node }: ReactNodeViewProps) {
  const { t } = useI18n()
  const text = node.textContent
  const empty = !text.trim()
  const editable = editor.isEditable
  const [sourceVisible, setSourceVisible] = useState(() => editable && empty)
  const [consoleOpen, setConsoleOpen] = useState(false)
  const [fullscreen, setFullscreen] = useState(false)
  const [result, setResult] = useState<DiagramResult | null>(null)
  const caretInside = useEditorState({
    editor,
    selector: ({ editor: current }) => {
      const pos = getPos()
      const { selection } = current.state
      if (typeof pos !== 'number' || !current.isFocused || !(selection instanceof TextSelection)) return false
      return selection.from > pos && selection.to < pos + node.nodeSize
    },
  })
  const error = result && 'error' in result ? result.error : null
  const diagram = result && !('error' in result) ? result : null

  useEffect(() => {
    if (empty) {
      setResult(null)
      return
    }
    let cancelled = false
    const timer = window.setTimeout(() => {
      void renderDiagram(text).then(next => { if (!cancelled) setResult(next) })
    }, result ? RENDER_DELAY : 0)
    return () => { cancelled = true; window.clearTimeout(timer) }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- re-render on source changes only
  }, [text, empty])

  // Arrowing into the hidden source shows it; leaving an empty diagram shows its placeholder again.
  useEffect(() => {
    if (caretInside && !sourceVisible) setSourceVisible(true)
    else if (!caretInside && empty && sourceVisible) setSourceVisible(false)
    // eslint-disable-next-line react-hooks/exhaustive-deps -- reacts to the caret, not to the toggle itself
  }, [caretInside, empty])
  useEffect(() => { if (!error) setConsoleOpen(false) }, [error])

  const focusSource = () => {
    setSourceVisible(true)
    editor.chain().command(({ tr }) => {
      const pos = getPos()
      if (typeof pos !== 'number') return false
      tr.setSelection(TextSelection.create(tr.doc, pos + node.nodeSize - 1))
      return true
    }).focus().run()
  }
  const selectBlock = () => {
    const pos = getPos()
    if (typeof pos !== 'number') return
    editor.chain().command(({ tr }) => { tr.setSelection(NodeSelection.create(tr.doc, pos)); return true }).focus().run()
  }
  const showDiagram = () => {
    setSourceVisible(false)
    if (caretInside) selectBlock()
  }
  const copySource = async () => {
    try {
      await navigator.clipboard.writeText(text)
      toast.success(t('Copied to clipboard'))
    } catch {
      toast.error(t('Could not copy'))
    }
  }
  const copyDiagram = async () => {
    if (!diagram) return
    try {
      await copyDiagramImage(diagram.svg, diagram.width)
      toast.success(t('Diagram copied to clipboard'))
    } catch {
      toast.error(t('Could not copy'))
    }
  }
  const onComment = editor.storage.codeBlock?.onComment
  const comment = () => {
    const pos = getPos()
    if (typeof pos !== 'number' || !onComment) return
    onComment({ from: pos + 1, to: pos + node.nodeSize - 1, text })
  }

  const fullscreenButton = <ToolbarButton glyph="openFullPage" label={t('Fullscreen')} onPress={() => setFullscreen(true)}/>
  const toggle = error
    ? <ToolbarButton glyph="diagramError" label={t('Show error')} pressed={consoleOpen} onPress={() => setConsoleOpen(open => !open)}/>
    : <ToolbarButton disabled={empty || !diagram} glyph="diagram" label={empty ? t('No diagram to show') : t('Show diagram')} onPress={showDiagram}/>
  const showSource = !editable ? false : sourceVisible

  return (
    <NodeViewWrapper className="flow-diagram" data-diagram="" data-empty={empty || undefined} data-error={error ? '' : undefined} data-i18n-ignore data-source-visible={showSource || undefined}>
      <TooltipProvider delayDuration={300}>
        {showSource ? (
          <Toolbar>
            <ToolbarButton disabled={empty} glyph="copyMessage" label={empty ? t('No source code to copy') : t('Copy source')} onPress={() => void copySource()}/>
            {toggle}
            <span aria-hidden className="flow-diagram__separator"/>
            {fullscreenButton}
          </Toolbar>
        ) : (
          <Toolbar>
            {onComment && <ToolbarButton disabled={empty} glyph="newComment" label={empty ? t('No source code to comment') : t('Add comment')} onPress={comment}/>}
            <ToolbarButton disabled={!diagram} glyph="copyMessage" label={diagram ? t('Copy diagram') : t('No diagram to copy')} onPress={() => void copyDiagram()}/>
            {editable && <ToolbarButton glyph="diagramSource" label={t('Show source')} onPress={focusSource}/>}
            <span aria-hidden className="flow-diagram__separator"/>
            {fullscreenButton}
          </Toolbar>
        )}
      </TooltipProvider>
      <pre className="flow-diagram__source" spellCheck={false}>
        <NodeViewContent<'code'> as="code"/>
      </pre>
      {showSource && error && (
        <>
          <TooltipProvider delayDuration={300}>
            <FlowTooltip label={t('Error in diagram. Click to view')}>
              <button aria-label={t('Error in diagram. Click to view')} className="flow-diagram__error-dot" contentEditable={false} onClick={() => setConsoleOpen(open => !open)} onMouseDown={keepFocus} type="button">
                <FlowGlyph name="diagramError" size={14}/>
              </button>
            </FlowTooltip>
          </TooltipProvider>
          {consoleOpen && <div className="flow-diagram__console" contentEditable={false}><span>{error}</span></div>}
        </>
      )}
      {!showSource && (
        empty
          ? <Placeholder label={t('Your diagram will display here')} onActivate={editable ? focusSource : undefined}/>
          : (
            <div className="flow-diagram__preview" contentEditable={false} onMouseDown={event => { if (editable) { event.preventDefault(); selectBlock() } }}>
              {error ? <div className="flow-diagram__console flow-diagram__console--inline"><span>{error}</span></div> : <DiagramSvg result={result}/>}
            </div>
          )
      )}
      <Dialog.Root open={fullscreen} onOpenChange={setFullscreen}>
        <Dialog.Portal>
          <Dialog.Overlay className="flow-diagram-fullscreen__overlay" data-flow-motion="backdrop"/>
          <Dialog.Content aria-describedby={undefined} className="flow-diagram-fullscreen" data-flow-motion="dialog" onOpenAutoFocus={event => { event.preventDefault(); (event.currentTarget as HTMLElement).focus() }} tabIndex={-1} data-i18n-ignore data-tooltip-raised="">
            <Dialog.Title className="sr-only">{t('Diagram')}</Dialog.Title>
            <TooltipProvider delayDuration={300}>
              <Toolbar className="flow-diagram__toolbar--fullscreen">
                <ToolbarButton disabled={!diagram} glyph="copyMessage" label={diagram ? t('Copy diagram to clipboard') : t('No diagram to copy')} onPress={() => void copyDiagram()}/>
                <ToolbarButton glyph={sourceVisible ? 'diagram' : 'diagramSource'} label={sourceVisible ? t('Show diagram') : t('Show source')} onPress={() => setSourceVisible(visible => !visible)}/>
                <span aria-hidden className="flow-diagram__separator"/>
                <Dialog.Close asChild><button aria-label={t('Close')} className="flow-diagram__button" onMouseDown={keepFocus} type="button"><FlowGlyph name="closeChat" size={14}/></button></Dialog.Close>
              </Toolbar>
            </TooltipProvider>
            <div className="flow-diagram-fullscreen__body">
              {sourceVisible || !diagram ? <pre className="flow-diagram-fullscreen__source">{error ? `${text}\n\n${error}` : text}</pre> : <DiagramSvg result={diagram}/>}
            </div>
          </Dialog.Content>
        </Dialog.Portal>
      </Dialog.Root>
    </NodeViewWrapper>
  )
}
