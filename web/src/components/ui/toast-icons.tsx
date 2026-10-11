const base = { width: 16, height: 16, viewBox: '0 0 16 16', role: 'img', focusable: false, 'aria-hidden': true } as const

/** 16px notification glyphs. Neutral toasts use the muted icon colour; the others carry their semantic colour. */
export function ToastInfoIcon() {
  return <svg {...base} fill="currentColor"><path fillRule="evenodd" clipRule="evenodd" d="M8 15C11.866 15 15 11.866 15 8C15 4.13401 11.866 1 8 1C4.13401 1 1 4.13401 1 8C1 11.866 4.13401 15 8 15ZM8 4C7.44772 4 7 4.44772 7 5C7 5.55228 7.44772 6 8 6C8.55228 6 9 5.55228 9 5C9 4.44772 8.55228 4 8 4ZM8 7C7.44772 7 7 7.44772 7 8V11C7 11.5523 7.44772 12 8 12C8.55228 12 9 11.5523 9 11V8C9 7.44772 8.55228 7 8 7Z"/></svg>
}

export function ToastCopyIcon() {
  return <svg {...base} fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinejoin="round"><rect x="5.25" y="5.25" width="8.5" height="8.5" rx="2.5"/><path d="M10.75 3.6A2.35 2.35 0 0 0 8.4 1.25H5.1a2.85 2.85 0 0 0-2.85 2.85V7.4a2.35 2.35 0 0 0 2.35 2.35" strokeLinecap="round"/></svg>
}

export function ToastSuccessIcon() {
  return <svg {...base}><circle cx="8" cy="8" r="7" fill="currentColor"/><path d="M5 8.2 7.1 10.3 11.1 6.1" fill="none" stroke="var(--toast-icon-cutout)" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"/></svg>
}

export function ToastErrorIcon() {
  return <svg {...base}><circle cx="8" cy="8" r="7" fill="currentColor"/><path d="M8 4.6V8.6" fill="none" stroke="var(--toast-icon-cutout)" strokeWidth="1.5" strokeLinecap="round"/><circle cx="8" cy="11.1" r=".95" fill="var(--toast-icon-cutout)"/></svg>
}

export function ToastWarningIcon() {
  return <svg {...base}><path d="M6.7 2.3a1.5 1.5 0 0 1 2.6 0l5.1 8.9A1.5 1.5 0 0 1 13.1 13.5H2.9a1.5 1.5 0 0 1-1.3-2.3L6.7 2.3Z" fill="currentColor"/><path d="M8 5.8v2.8" fill="none" stroke="var(--toast-icon-cutout)" strokeWidth="1.5" strokeLinecap="round"/><circle cx="8" cy="11" r=".9" fill="var(--toast-icon-cutout)"/></svg>
}

export function ToastSpinnerIcon() {
  return <svg {...base} className="flow-toast__spinner" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round"><circle cx="8" cy="8" r="6" opacity=".25"/><path d="M8 2a6 6 0 0 1 6 6"/></svg>
}

export function ToastCloseIcon() {
  return <svg width="12" height="12" viewBox="0 0 12 12" fill="currentColor" aria-hidden="true" focusable="false"><path d="M2.22 2.22a.75.75 0 0 1 1.06 0L6 4.94l2.72-2.72a.75.75 0 1 1 1.06 1.06L7.06 6l2.72 2.72a.75.75 0 1 1-1.06 1.06L6 7.06 3.28 9.78a.75.75 0 0 1-1.06-1.06L4.94 6 2.22 3.28a.75.75 0 0 1 0-1.06Z"/></svg>
}
