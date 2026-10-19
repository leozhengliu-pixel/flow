import { useCallback, useEffect, useRef, useState } from "react";
import { Play, Square } from "lucide-react";
import { confirmAction } from "@/components/ui/action-dialog-service";
import { useI18n } from "@/i18n/i18n";
import { codingTerminalURL } from "@/lib/api";
import { CodingButton } from "./coding-settings-primitives";
import { createXTerm, observeResize, type XTerm } from "./coding-xterm";

/** FLOW_CODING_DEBUG_TERMINAL_TTL default; the server may send the real deadline as `expiresAt`. */
export const DEBUG_TERMINAL_TTL_MS = 20 * 60 * 1000;

type TerminalState = "idle" | "connecting" | "connected" | "closed" | "error";

/** Text frames from the server: progress while the sandbox starts, then status or errors. */
type ServerFrame = { type: "status" | "progress" | "ready"; message?: string; expiresAt?: string } | { type: "error"; message?: string } | { type: "exit"; code?: number; message?: string };

function remaining(ms: number) {
  const total = Math.max(0, Math.round(ms / 1000));
  return `${Math.floor(total / 60)}:${String(total % 60).padStart(2, "0")}`;
}

/**
 * Debug terminal (§18): a temporary sandbox restored from the latest snapshot, attached over a
 * WebSocket. Binary frames carry TTY bytes both ways; text frames carry JSON control messages.
 * Closing the socket stops the sandbox.
 */
export function DebugTerminal({ environmentId, disabled, available = true }: { environmentId: string; disabled?: boolean; available?: boolean }) {
  const { t } = useI18n();
  const [state, setState] = useState<TerminalState>("idle");
  const [message, setMessage] = useState<string>();
  const [expiresAt, setExpiresAt] = useState<number>();
  const [now, setNow] = useState(() => Date.now());
  const container = useRef<HTMLDivElement>(null);
  const socket = useRef<WebSocket | null>(null);
  const term = useRef<XTerm | null>(null);
  const cleanup = useRef<(() => void)[]>([]);

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
    setState("connecting");
    setMessage(t("Starting a temporary sandbox…"));
    setExpiresAt(undefined);
    let terminal: XTerm;
    let fit: () => void;
    try {
      ({ terminal, fit } = await createXTerm({ cursorBlink: true, convertEol: false, fontSize: 12 }));
    } catch {
      setState("error");
      setMessage(t("Could not load the terminal"));
      return;
    }
    term.current = terminal;
    cleanup.current.push(() => terminal.dispose());
    if (container.current) {
      terminal.open(container.current);
      fit();
      cleanup.current.push(observeResize(container.current, fit));
    }

    const ws = new WebSocket(codingTerminalURL(environmentId));
    ws.binaryType = "arraybuffer";
    socket.current = ws;
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

    ws.onopen = () => {
      ws.send(JSON.stringify({ type: "resize", cols: terminal.cols, rows: terminal.rows }));
    };
    ws.onmessage = (event: MessageEvent) => {
      if (typeof event.data === "string") {
        let frame: ServerFrame;
        try {
          frame = JSON.parse(event.data) as ServerFrame;
        } catch {
          terminal.write(event.data);
          return;
        }
        if (frame.type === "error") {
          setState("error");
          setMessage(frame.message || t("The terminal failed"));
        } else if (frame.type === "exit") {
          setMessage(frame.message || t("The shell exited"));
        } else {
          if (frame.type === "ready") setState("connected");
          if (frame.message) setMessage(frame.message);
          if ("expiresAt" in frame && frame.expiresAt) {
            const deadline = Date.parse(frame.expiresAt);
            if (!Number.isNaN(deadline)) setExpiresAt(deadline);
          }
        }
        return;
      }
      // First TTY bytes mean the shell is attached.
      setState((current) => (current === "connecting" ? "connected" : current));
      setExpiresAt((current) => current ?? Date.now() + DEBUG_TERMINAL_TTL_MS);
      setMessage(undefined);
      terminal.write(event.data instanceof ArrayBuffer ? new Uint8Array(event.data) : String(event.data));
    };
    ws.onerror = () => {
      setState("error");
      setMessage(t("Could not connect to the debug terminal"));
    };
    ws.onclose = (event: CloseEvent) => {
      setState((current) => (current === "error" ? current : "closed"));
      setMessage((current) => event.reason || current || t("Disconnected. The temporary sandbox was stopped."));
      terminal.write("\r\n\x1b[2m[disconnected]\x1b[0m\r\n");
      socket.current = null;
    };
    terminal.focus();
  };

  const disconnect = () => {
    const ws = socket.current;
    if (ws) ws.close(1000, "closed by user");
    setState("closed");
    setMessage(t("Disconnected. The temporary sandbox was stopped."));
  };

  const runPrepare = async () => {
    const confirmed = await confirmAction(t("Run the prepare script with secrets?"), {
      description: t("Secrets are injected into this sandbox, so anyone using this terminal can read them."),
      confirmLabel: t("Run prepare script"),
      danger: false,
    });
    if (confirmed) sendJSON({ type: "runPrepare" });
  };

  const live = state === "connecting" || state === "connected";
  const idleText = !available ? t("The debug terminal is turned off on this server.") : state === "closed" && message ? message : t("No terminal is running.");
  return (
    <div className="cs-terminal" data-testid="debug-terminal" data-state={state}>
      {state === "error" ? (
        <div className="cs-terminal-error">
          <div className="cs-terminal-error-title">
            <span className="cs-status-dot" aria-hidden="true" />
            {t("Terminal failed")}
          </div>
          <p role="status">{message}</p>
          <div className="cs-terminal-actions">
            <CodingButton small disabled={disabled || !available} onClick={() => void connect()}>Retry terminal</CodingButton>
          </div>
        </div>
      ) : live ? (
        <div className="cs-terminal-bar">
          <span className="cs-terminal-text" role="status">{message ?? t("Connected to a temporary sandbox.")}</span>
          {state === "connected" && expiresAt && <span className="cs-terminal-countdown" data-testid="terminal-countdown">{t("Expires in {time}").replace("{time}", remaining(expiresAt - now))}</span>}
          {state === "connected" && <CodingButton small onClick={() => void runPrepare()}>Run prepare script</CodingButton>}
          <button type="button" className="cs-icon-button" aria-label={t("Stop terminal")} title={t("Stop terminal")} onClick={disconnect}>
            <Square size={12} />
          </button>
        </div>
      ) : (
        <div className="cs-terminal-bar">
          <span className="cs-terminal-text" role="status">{idleText}</span>
          <button type="button" className="cs-icon-button" aria-label={t("Start terminal")} title={t("Start terminal")} disabled={disabled || !available} onClick={() => void connect()}>
            <Play size={14} />
          </button>
        </div>
      )}
      <div ref={container} className={`cs-terminal-screen${live ? "" : " is-hidden"}`} />
    </div>
  );
}
