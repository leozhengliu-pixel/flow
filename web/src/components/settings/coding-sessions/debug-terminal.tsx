import { useCallback, useEffect, useRef, useState } from "react";
import { Maximize2, Minus, Play, RotateCcw, Square } from "lucide-react";
import { confirmAction } from "@/components/ui/action-dialog-service";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { useI18n } from "@/i18n/i18n";
import { codingTerminalURL } from "@/lib/api";
import { CodingButton } from "./coding-settings-primitives";
import { createXTerm, observeResize, terminalFont, terminalTheme, type XTerm } from "./coding-xterm";
import "./debug-terminal.css";

/** FLOW_CODING_DEBUG_TERMINAL_TTL default; the server may send the real deadline as `expiresAt`. */
export const DEBUG_TERMINAL_TTL_MS = 20 * 60 * 1000;

/** The reference app's terminal states: each has its own status line (dot colour + copy). */
export type TerminalState = "idle" | "connecting" | "starting" | "preparing" | "connected" | "exited" | "error";

/** Text frames from the server: progress while the sandbox starts, then status or errors. */
type ServerFrame = { type: "status" | "progress" | "ready"; message?: string; expiresAt?: string } | { type: "error"; message?: string } | { type: "exit"; code?: number; exitCode?: number; message?: string };

function remaining(ms: number) {
  const total = Math.max(0, Math.round(ms / 1000));
  return `${Math.floor(total / 60)}:${String(total % 60).padStart(2, "0")}`;
}

const STATE_COPY: Record<Exclude<TerminalState, "error">, string> = {
  idle: "No terminal is running.",
  connecting: "Connecting to the sandbox…",
  starting: "Preparing a sandbox. This can take a minute.",
  preparing: "Preparing the repository…",
  connected: "Connected",
  exited: "The shell exited. Restart it to continue.",
};

/** The 6px dot and copy of a terminal state (no dot while idle). */
export function TerminalStatusLine({ state, message }: { state: TerminalState; message?: string }) {
  const { t } = useI18n();
  const text = state === "error" ? message || t("The terminal is unavailable.") : t(STATE_COPY[state]);
  return (
    <span className="cs-terminal-status" data-state={state} role="status">
      {state !== "idle" && <span className="cs-terminal-dot" aria-hidden="true" />}
      <span className="cs-terminal-text" data-i18n-ignore={state === "error" && message ? true : undefined}>{text}</span>
    </span>
  );
}

/**
 * Debug terminal (§18): a temporary sandbox restored from the latest snapshot, attached over a WebSocket. Binary frames
 * carry TTY bytes both ways; text frames carry JSON control messages. The card shows the status line; the terminal
 * itself opens in a 960px dialog that can be minimised while the shell keeps running. Stopping closes the socket,
 * which stops the sandbox.
 */
export function DebugTerminal({ environmentId, disabled, available = true }: { environmentId: string; disabled?: boolean; available?: boolean }) {
  const { t } = useI18n();
  const [state, setState] = useState<TerminalState>("idle");
  const [message, setMessage] = useState<string>();
  const [open, setOpen] = useState(false);
  const [expiresAt, setExpiresAt] = useState<number>();
  const [now, setNow] = useState(() => Date.now());
  const host = useRef<HTMLDivElement | null>(null);
  const socket = useRef<WebSocket | null>(null);
  const term = useRef<XTerm | null>(null);
  const fit = useRef<(() => void) | undefined>(undefined);
  const cleanup = useRef<(() => void)[]>([]);
  const stopping = useRef(false);

  // xterm renders into one host element that moves into the dialog while it is open, so minimising keeps the session.
  if (!host.current && typeof document !== "undefined") {
    host.current = document.createElement("div");
    host.current.className = "cs-terminal-host";
  }
  const mountHost = useCallback((region: HTMLDivElement | null) => {
    if (!region || !host.current) return;
    region.appendChild(host.current);
    fit.current?.();
    term.current?.focus();
  }, []);

  const teardown = useCallback(() => {
    for (const fn of cleanup.current.splice(0)) {
      try {
        fn();
      } catch {
        // already disposed
      }
    }
    socket.current = null;
    term.current = null;
    fit.current = undefined;
  }, []);
  useEffect(() => teardown, [teardown]);

  useEffect(() => {
    if (state !== "connected") return;
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, [state]);

  const sendJSON = (value: unknown) => {
    const ws = socket.current;
    if (ws && ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify(value));
  };

  const connect = async () => {
    teardown();
    stopping.current = false;
    setOpen(true);
    setState("connecting");
    setMessage(undefined);
    setExpiresAt(undefined);
    let terminal: XTerm;
    try {
      const created = await createXTerm({ cursorBlink: true, convertEol: false, fontSize: 13, lineHeight: 1.2, fontFamily: terminalFont("--cs-terminal-font"), theme: terminalTheme() });
      terminal = created.terminal;
      fit.current = created.fit;
    } catch {
      setState("error");
      setMessage(t("The terminal is unavailable."));
      return;
    }
    term.current = terminal;
    cleanup.current.push(() => terminal.dispose());
    if (host.current) {
      host.current.replaceChildren();
      terminal.open(host.current);
      fit.current?.();
      cleanup.current.push(observeResize(host.current, () => fit.current?.()));
    }

    const ws = new WebSocket(codingTerminalURL(environmentId));
    ws.binaryType = "arraybuffer";
    socket.current = ws;
    let ready = false;
    let ended = false;
    // A restarted or stopped connection's late events never touch the new one.
    let current = true;
    cleanup.current.push(() => {
      current = false;
    });
    cleanup.current.push(() => {
      if (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING) ws.close(1000);
    });
    const encoder = new TextEncoder();
    const data = terminal.onData((input) => {
      if (ws.readyState === WebSocket.OPEN) ws.send(encoder.encode(input));
    });
    const resize = terminal.onResize(({ cols, rows }) => {
      if (ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ type: "resize", cols, rows }));
    });
    cleanup.current.push(() => data.dispose(), () => resize.dispose());
    const note = (text: string) => terminal.write(`\r\n\x1b[2m[${text}]\x1b[0m\r\n`);
    const attached = () => {
      ready = true;
      setState("connected");
      setMessage(undefined);
    };

    ws.onopen = () => {
      if (!current) return;
      setState("starting");
      ws.send(JSON.stringify({ type: "resize", cols: terminal.cols, rows: terminal.rows }));
    };
    ws.onmessage = (event: MessageEvent) => {
      if (!current) return;
      if (typeof event.data === "string") {
        let frame: ServerFrame;
        try {
          frame = JSON.parse(event.data) as ServerFrame;
        } catch {
          terminal.write(event.data);
          return;
        }
        if (frame.type === "error") {
          ended = true;
          setState("error");
          setMessage(frame.message || t("The terminal is unavailable."));
        } else if (frame.type === "exit") {
          ended = true;
          setState("exited");
          const code = frame.exitCode ?? frame.code;
          note(code === undefined ? frame.message || t("Shell exited") : t("Shell exited with code {code}").replace("{code}", String(code)));
        } else if (frame.type === "ready") {
          attached();
          if (frame.expiresAt) {
            const deadline = Date.parse(frame.expiresAt);
            if (!Number.isNaN(deadline)) setExpiresAt(deadline);
          }
        } else if (frame.type === "progress") {
          if (!ready) setState("starting");
        } else if (frame.message) {
          // Before the shell attaches a status means the repository is being prepared; afterwards it reports the prepare script.
          if (!ready) setState("preparing");
          else note(frame.message);
        }
        return;
      }
      // First TTY bytes mean the shell is attached.
      if (!ready) attached();
      setExpiresAt((current) => current ?? Date.now() + DEBUG_TERMINAL_TTL_MS);
      terminal.write(event.data instanceof ArrayBuffer ? new Uint8Array(event.data) : String(event.data));
    };
    ws.onerror = () => {
      if (!current) return;
      ended = true;
      setState("error");
      setMessage(t("The terminal connection failed."));
    };
    ws.onclose = (event: CloseEvent) => {
      if (!current) return;
      socket.current = null;
      if (stopping.current) return;
      note(t("Terminal connection closed"));
      if (ended) return;
      setState("error");
      setMessage(event.reason || t("The terminal connection closed."));
    };
    terminal.focus();
  };

  const stop = () => {
    stopping.current = true;
    const ws = socket.current;
    if (ws) ws.close(1000, "closed by user");
    teardown();
    setState("idle");
    setMessage(undefined);
    setExpiresAt(undefined);
  };

  const runPrepare = async () => {
    const confirmed = await confirmAction(t("Run the prepare script with secrets?"), {
      description: t("Secrets are injected into this sandbox, so anyone using this terminal can read them."),
      confirmLabel: t("Run prepare script"),
      danger: false,
    });
    if (confirmed) sendJSON({ type: "runPrepare" });
  };

  const running = state !== "idle";
  const live = state === "connecting" || state === "starting" || state === "preparing" || state === "connected";
  const startLabel = running ? t("Restart terminal") : t("Start terminal");
  return (
    <div className="cs-terminal" data-testid="debug-terminal" data-state={state}>
      <div className="cs-terminal-bar">
        {available ? <TerminalStatusLine state={state} message={message} /> : <span className="cs-terminal-status" role="status"><span className="cs-terminal-text">{t("The debug terminal is turned off on this server.")}</span></span>}
        {running ? (
          <button type="button" className="cs-icon-button" aria-label={t("Show terminal")} title={t("Show terminal")} onClick={() => setOpen(true)}>
            <Maximize2 size={14} />
          </button>
        ) : (
          <button type="button" className="cs-icon-button" aria-label={t("Start terminal")} title={t("Start terminal")} disabled={disabled || !available} onClick={() => void connect()}>
            <Play size={14} />
          </button>
        )}
      </div>
      {state === "error" && !open && (
        <div className="cs-terminal-actions">
          <CodingButton small disabled={disabled || !available} onClick={() => void connect()}>Retry terminal</CodingButton>
        </div>
      )}
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="cs-terminal-dialog" aria-describedby={undefined} closeLabel={t("Close")} onOpenAutoFocus={(event) => event.preventDefault()}>
          <header className="cs-terminal-dialog-header">
            <DialogTitle className="sr-only">{t("Terminal")}</DialogTitle>
            <TerminalStatusLine state={state} message={message} />
            {state === "connected" && expiresAt && <span className="cs-terminal-countdown" data-testid="terminal-countdown">{t("Expires in {time}").replace("{time}", remaining(expiresAt - now))}</span>}
            <span className="cs-terminal-dialog-actions">
              {state === "connected" && <CodingButton small onClick={() => void runPrepare()}>Run prepare script</CodingButton>}
              <button type="button" className="cs-icon-button" aria-label={startLabel} title={startLabel} disabled={disabled || !available || (live && state !== "connected")} onClick={() => void connect()}>
                {running ? <RotateCcw size={14} /> : <Play size={14} />}
              </button>
              <button type="button" className="cs-icon-button" aria-label={t("Minimize terminal")} title={t("Minimize terminal")} onClick={() => setOpen(false)}>
                <Minus size={14} />
              </button>
              <button type="button" className="cs-icon-button" aria-label={t("Stop terminal")} title={t("Stop terminal")} disabled={!running} onClick={stop}>
                <Square size={12} />
              </button>
            </span>
          </header>
          <div className="cs-terminal-dialog-body">
            <div className="cs-terminal-screen" role="region" aria-label={t("Coding environment terminal")} ref={mountHost} />
          </div>
        </DialogContent>
      </Dialog>
    </div>
  );
}
