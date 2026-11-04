import { useId } from 'react'

/** The reference app's Pulse glyph (sidebar item, inbox summary avatar, empty states). */
export function PulseIcon({ size }: { size?: number } = {}) {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true" width={size} height={size} fill="currentColor">
      <path
        fillRule="evenodd"
        clipRule="evenodd"
        d="M11.3354 1.08228C11.7059 1.27438 11.8561 1.74156 11.6708 2.12576L9.5593 6.50508C9.39923 6.83707 9.64113 7.22224 10.0097 7.22224H14C14 7.22224 14.2275 7.22219 14.25 7.22219C14.6642 7.22219 15 7.57041 15 7.99996C15 8.4295 14.6642 8.77772 14.25 8.77772C10.4465 8.77772 7.32888 11.131 5.67083 14.5699C5.48559 14.9541 5.03507 15.1098 4.66459 14.9177C4.29411 14.7256 4.14394 14.2584 4.32918 13.8742L6.44081 9.49467C6.60085 9.16275 6.35907 8.77764 5.99059 8.77759C4.57706 8.7774 3.16352 8.77781 1.75 8.77781C1.33579 8.77781 1 8.42959 1 8.00004C1 7.5705 1.33579 7.22228 1.75 7.22228C5.55362 7.22228 8.67116 4.86885 10.3292 1.43003C10.5145 1.04588 10.965 0.890196 11.3354 1.08228Z"
      />
    </svg>
  );
}

const SPARK_PATH = 'M28.6708 8.51611C29.4118 8.8866 29.7121 9.78761 29.3416 10.5286L24.427 20.3577H34C34 20.3604 34.455 20.3576 34.5 20.3576C35.3284 20.3576 36 21.0292 36 21.8576C36 22.686 35.3284 23.3576 34.5 23.3576C26.8929 23.3576 20.6578 27.8962 17.3417 34.5284C16.9712 35.2694 16.0701 35.5697 15.3292 35.1992C14.5882 34.8287 14.2879 33.9277 14.6584 33.1868L19.5729 23.3577C16.2153 23.3566 12.8576 23.3578 9.5 23.3578C8.67157 23.3578 8 22.6862 8 21.8578C8 21.0293 8.67157 20.3578 9.5 20.3578C17.1072 20.3578 23.3423 15.8189 26.6584 9.18677C27.0289 8.44591 27.9299 8.14565 28.6708 8.51611Z'

/** The 44px Pulse mark of the welcome banner: a top-to-bottom gradient spark over two blurred glows. */
export function PulseSparkLogo() {
  const id = useId().replace(/:/g, '')
  const fill = `url(#${id}-fill)`
  return (
    <svg aria-hidden="true" fill="none" focusable="false" height="44" style={{ overflow: 'visible' }} viewBox="0 0 44 44" width="44">
      <g filter={`url(#${id}-glow-near)`} opacity=".3"><path d={SPARK_PATH} fill={fill}/></g>
      <g filter={`url(#${id}-glow-far)`} opacity=".4"><path d={SPARK_PATH} fill={fill}/></g>
      <path d={SPARK_PATH} fill={fill}/>
      <defs>
        <filter colorInterpolationFilters="sRGB" filterUnits="userSpaceOnUse" height="35.001" id={`${id}-glow-near`} width="36" x="4" y="4.35742"><feGaussianBlur stdDeviation="2"/></filter>
        <filter colorInterpolationFilters="sRGB" filterUnits="userSpaceOnUse" height="83.001" id={`${id}-glow-far`} width="84" x="-20" y="-19.6426"><feGaussianBlur stdDeviation="10"/></filter>
        <linearGradient gradientUnits="userSpaceOnUse" id={`${id}-fill`} x1="22" x2="22" y1="8.35742" y2="35.3579">
          <stop style={{ stopColor: 'var(--pulse-logo-fill-top)' }}/>
          <stop offset="1" style={{ stopColor: 'var(--pulse-logo-fill-bottom)' }}/>
        </linearGradient>
      </defs>
    </svg>
  )
}
