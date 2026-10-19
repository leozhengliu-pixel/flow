import { useEffect, useRef, useState } from "react";
import { useResolvedTheme } from "@/hooks/use-resolved-theme";
import { SNAPSHOT_LOG_SCROLLBACK, createXTerm, observeResize, stripAnsi, xtermTheme, type XTerm } from "./coding-xterm";

const HIDE_CURSOR = "\x1b[?25l";

/**
 * Snapshot build log rendered like the reference app: a read-only xterm.js view (ANSI colours,
 * scrollback, follows the output) themed from the design tokens. Polled log text is appended
 * when it extends what was written, otherwise the terminal is reset and redrawn. A plain-text copy
 * stays in the DOM for screen readers and is shown until (or instead of, on failure) xterm loads.
 */
export function SnapshotLogTerminal({ text, placeholder }: { text: string; placeholder: string }) {
  const host = useRef<HTMLDivElement>(null);
  const terminal = useRef<XTerm | null>(null);
  const written = useRef("");
  const [ready, setReady] = useState(false);
  const theme = useResolvedTheme();

  useEffect(() => {
    let active = true;
    const cleanup: (() => void)[] = [];
    void createXTerm({ disableStdin: true, convertEol: true, cursorBlink: false, cursorInactiveStyle: "none", scrollback: SNAPSHOT_LOG_SCROLLBACK, fontSize: 13, lineHeight: 1.5, theme: xtermTheme({ readOnly: true }) })
      .then(({ terminal: term, fit }) => {
        if (!active || !host.current) {
          term.dispose();
          return;
        }
        term.open(host.current);
        fit();
        cleanup.push(observeResize(host.current, fit));
        term.write(HIDE_CURSOR);
        written.current = "";
        terminal.current = term;
        setReady(true);
      })
      .catch(() => {
        // Keep the plain-text fallback.
      });
    return () => {
      active = false;
      for (const fn of cleanup) fn();
      terminal.current?.dispose();
      terminal.current = null;
    };
  }, []);

  useEffect(() => {
    const term = terminal.current;
    if (!ready || !term) return;
    if (text.startsWith(written.current)) {
      const delta = text.slice(written.current.length);
      if (delta) term.write(delta);
    } else {
      term.reset();
      term.write(HIDE_CURSOR + text);
    }
    written.current = text;
  }, [ready, text]);

  // Token values change with the interface theme; repaint once the new variables are applied.
  useEffect(() => {
    if (!ready) return;
    const frame = requestAnimationFrame(() => {
      if (terminal.current) terminal.current.options.theme = xtermTheme({ readOnly: true });
    });
    return () => cancelAnimationFrame(frame);
  }, [ready, theme]);

  const plain = stripAnsi(text);
  return (
    <div className="cs-logs" data-testid="snapshot-logs" data-ready={ready || undefined}>
      <div ref={host} className="cs-logs-screen" aria-hidden="true" />
      <pre className={ready && plain ? "sr-only" : "cs-logs-fallback"} aria-live="polite" data-i18n-ignore={plain ? true : undefined}>
        {plain || placeholder}
      </pre>
    </div>
  );
}
