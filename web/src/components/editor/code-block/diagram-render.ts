/** Renders Mermaid source to SVG in the reference app's monochrome diagram style, derived from the current theme's colours. */

export type DiagramResult = { svg: string; width: number } | { error: string }

let sequence = 0
let probe: HTMLCanvasElement | null = null
let warmedUp: Promise<void> | undefined

/** Resolves any CSS colour (lch(), color-mix(), var()) to #rrggbb, which Mermaid's colour maths understands. */
function hexColor(css: string, fallback: string) {
  try {
    probe ??= document.createElement('canvas')
    probe.width = probe.height = 1
    const context = probe.getContext('2d', { willReadFrequently: true })
    if (!context) return fallback
    context.clearRect(0, 0, 1, 1)
    context.fillStyle = fallback
    context.fillStyle = css
    context.fillRect(0, 0, 1, 1)
    const [r, g, b, a] = context.getImageData(0, 0, 1, 1).data
    if (!a) return fallback
    return `#${[r, g, b].map(value => value.toString(16).padStart(2, '0')).join('')}`
  } catch {
    return fallback
  }
}

function mix(foreground: string, background: string, amount: number) {
  const parse = (hex: string) => [1, 3, 5].map(index => parseInt(hex.slice(index, index + 2), 16))
  const [f, b] = [parse(foreground), parse(background)]
  return `#${f.map((value, index) => Math.round(b[index] + (value - b[index]) * amount).toString(16).padStart(2, '0')).join('')}`
}

function cssValue(element: Element, name: string) {
  return getComputedStyle(element).getPropertyValue(name).trim()
}

/**
 * The reference app draws diagrams in greys taken from the page: nodes 8% of the text colour over the background, borders 58%,
 * lines 46%, cluster borders 19%, labels in the text colour, all in the monospace font at 13px.
 */
export function diagramTheme(element: Element = document.documentElement) {
  const light = document.documentElement.dataset.theme === 'light'
  const background = hexColor(cssValue(element, '--description-diagram-bg') || cssValue(element, '--theme-background'), light ? '#f8f8f9' : '#141516')
  const text = hexColor(cssValue(element, '--theme-text-primary'), light ? '#2f2f31' : '#e2e3e5')
  const font = cssValue(element, '--font-monospace') || 'ui-monospace, monospace'
  return {
    theme: 'base' as const,
    darkMode: !light,
    fontFamily: font,
    themeVariables: {
      darkMode: !light,
      fontFamily: font,
      fontSize: '13px',
      background,
      mainBkg: mix(text, background, .08),
      primaryColor: mix(text, background, .08),
      secondaryColor: mix(text, background, .05),
      tertiaryColor: mix(text, background, .03),
      primaryBorderColor: mix(text, background, .58),
      secondaryBorderColor: mix(text, background, .58),
      tertiaryBorderColor: mix(text, background, .58),
      nodeBorder: mix(text, background, .58),
      primaryTextColor: text,
      secondaryTextColor: text,
      tertiaryTextColor: text,
      textColor: text,
      titleColor: text,
      lineColor: mix(text, background, .46),
      edgeLabelBackground: background,
      clusterBkg: background,
      clusterBorder: mix(text, background, .19),
      noteBkgColor: mix(text, background, .05),
      noteBorderColor: mix(text, background, .19),
      noteTextColor: text,
      actorBkg: mix(text, background, .08),
      actorBorder: mix(text, background, .58),
      actorTextColor: text,
      signalColor: mix(text, background, .46),
      signalTextColor: text,
    },
  }
}

/** Mermaid's first error line reads's console ("No diagram type detected …", "Parse error on line 2: …"). */
function errorMessage(error: unknown) {
  const message = error instanceof Error ? error.message : String((error as { str?: string })?.str ?? error)
  return message.trim() || 'Invalid diagram'
}

export async function renderDiagram(source: string, element?: Element): Promise<DiagramResult> {
  const mermaid = (await import('mermaid')).default
  const theme = diagramTheme(element)
  mermaid.initialize({
    startOnLoad: false,
    securityLevel: 'strict',
    suppressErrorRendering: true,
    ...theme,
    // Mermaid 12 widens short labels to 120px; the reference app's boxes fit their text.
    flowchart: { minNodeWidth: 0 },
    themeCSS: 'text, tspan { font-weight: 300; }',
  })
  const id = `flow-diagram-${++sequence}`
  // Mermaid measures labels in a scratch element; the UI translator must not rewrite them ("Start" → "开始") meanwhile.
  const scratch = document.createElement('div')
  scratch.setAttribute('data-i18n-ignore', '')
  scratch.setAttribute('aria-hidden', 'true')
  scratch.style.cssText = 'position:fixed;left:-10000px;top:0;visibility:hidden;pointer-events:none'
  document.body.append(scratch)
  try {
    // The page's first render measures labels too narrow; one throwaway render settles Mermaid's text measuring.
    warmedUp ??= mermaid.render(`${id}-warmup`, 'flowchart LR\n  a[Start] --> b[Done]', scratch).then(() => undefined, () => undefined)
    await warmedUp
    const { svg } = await mermaid.render(id, source, scratch)
    const width = Number(/max-width:\s*([\d.]+)px/.exec(svg)?.[1] ?? 0)
    return { svg, width }
  } catch (error) {
    return { error: errorMessage(error) }
  } finally {
    scratch.remove()
  }
}

/** Copies the rendered diagram as a PNG (2x), falling back to the SVG markup where images can't go to the clipboard. */
export async function copyDiagramImage(svg: string, width: number) {
  const parsed = new DOMParser().parseFromString(svg, 'image/svg+xml').documentElement
  const viewBox = parsed.getAttribute('viewBox')?.split(/[\s,]+/).map(Number) ?? []
  const naturalWidth = width || viewBox[2] || 600
  const naturalHeight = viewBox[3] && viewBox[2] ? naturalWidth * viewBox[3] / viewBox[2] : 400
  parsed.setAttribute('width', String(naturalWidth))
  parsed.setAttribute('height', String(naturalHeight))
  const markup = new XMLSerializer().serializeToString(parsed)
  try {
    if (typeof ClipboardItem === 'undefined') throw new Error('no ClipboardItem')
    const png = new Promise<Blob>((resolve, reject) => {
      const image = new Image()
      image.onload = () => {
        const canvas = document.createElement('canvas')
        canvas.width = Math.ceil(naturalWidth * 2)
        canvas.height = Math.ceil(naturalHeight * 2)
        const context = canvas.getContext('2d')
        if (!context) return reject(new Error('no canvas'))
        context.scale(2, 2)
        context.drawImage(image, 0, 0, naturalWidth, naturalHeight)
        canvas.toBlob(blob => blob ? resolve(blob) : reject(new Error('no image')), 'image/png')
      }
      image.onerror = () => reject(new Error('image failed'))
      image.src = `data:image/svg+xml;charset=utf-8,${encodeURIComponent(markup)}`
    })
    await navigator.clipboard.write([new ClipboardItem({ 'image/png': png })])
  } catch {
    await navigator.clipboard.writeText(markup)
  }
}
