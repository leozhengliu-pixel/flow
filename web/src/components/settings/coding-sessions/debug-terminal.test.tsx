import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ActionDialogHost } from "@/components/ui/action-dialogs";
import { I18nProvider } from "@/i18n/i18n";
import { DebugTerminal } from "./debug-terminal";

const terminals: FakeTerminal[] = [];
class FakeTerminal {
  cols = 80;
  rows = 24;
  written: (string | Uint8Array)[] = [];
  dataListener?: (data: string) => void;
  resizeListener?: (size: { cols: number; rows: number }) => void;
  disposed = false;
  opened?: HTMLElement;
  options: unknown;
  constructor(options: unknown) {
    this.options = options;
    terminals.push(this);
  }
  open(element: HTMLElement) {
    this.opened = element;
  }
  write(data: string | Uint8Array) {
    this.written.push(data);
  }
  onData(listener: (data: string) => void) {
    this.dataListener = listener;
    return { dispose: () => (this.dataListener = undefined) };
  }
  onResize(listener: (size: { cols: number; rows: number }) => void) {
    this.resizeListener = listener;
    return { dispose: () => (this.resizeListener = undefined) };
  }
  loadAddon() {}
  focus() {}
  dispose() {
    this.disposed = true;
  }
}
vi.mock("@xterm/xterm", () => ({ Terminal: FakeTerminal }));
vi.mock("@xterm/addon-fit", () => ({ FitAddon: class { fit() {} } }));
vi.mock("@xterm/xterm/css/xterm.css", () => ({}));

class FakeWebSocket {
  static CONNECTING = 0;
  static OPEN = 1;
  static CLOSING = 2;
  static CLOSED = 3;
  static instances: FakeWebSocket[] = [];
  readyState = FakeWebSocket.CONNECTING;
  binaryType = "blob";
  sent: unknown[] = [];
  closeCalls: [number?, string?][] = [];
  onopen?: () => void;
  onmessage?: (event: { data: unknown }) => void;
  onerror?: () => void;
  onclose?: (event: { code: number; reason: string }) => void;
  url: string;
  constructor(url: string) {
    this.url = url;
    FakeWebSocket.instances.push(this);
  }
  send(data: unknown) {
    this.sent.push(data);
  }
  close(code?: number, reason?: string) {
    this.closeCalls.push([code, reason]);
    this.serverClose(code ?? 1000, "");
  }
  // test helpers
  serverOpen() {
    this.readyState = FakeWebSocket.OPEN;
    this.onopen?.();
  }
  serverText(value: unknown) {
    this.onmessage?.({ data: JSON.stringify(value) });
  }
  serverBytes(text: string) {
    this.onmessage?.({ data: Uint8Array.from(text, (char) => char.charCodeAt(0)).buffer });
  }
  serverClose(code = 1000, reason = "") {
    if (this.readyState === FakeWebSocket.CLOSED) return;
    this.readyState = FakeWebSocket.CLOSED;
    this.onclose?.({ code, reason });
  }
}

beforeEach(() => {
  terminals.length = 0;
  FakeWebSocket.instances = [];
  vi.stubGlobal("WebSocket", FakeWebSocket);
  localStorage.setItem("flow:locale", "en-US");
});
afterEach(() => vi.unstubAllGlobals());

function renderTerminal(props: Partial<Parameters<typeof DebugTerminal>[0]> = {}) {
  return render(
    <I18nProvider>
      <DebugTerminal environmentId="env 1" {...props} />
      <ActionDialogHost />
    </I18nProvider>,
  );
}

async function connect() {
  fireEvent.click(screen.getByRole("button", { name: "Start terminal" }));
  await waitFor(() => expect(FakeWebSocket.instances).toHaveLength(1));
  return FakeWebSocket.instances[0];
}

describe("DebugTerminal", () => {
  it("shows the reference app's idle card", () => {
    renderTerminal();
    expect(screen.getByRole("status")).toHaveTextContent("No terminal is running.");
    expect(screen.getByRole("button", { name: "Start terminal" })).toBeEnabled();
    expect(screen.getByTestId("debug-terminal").querySelector(".cs-terminal-screen")).toHaveClass("is-hidden");
  });

  it("connects, streams TTY bytes both ways and resizes", async () => {
    renderTerminal();
    const ws = await connect();
    expect(ws.url).toBe(`ws://${location.host}/api/coding/environments/env%201/terminal`);
    expect(ws.binaryType).toBe("arraybuffer");
    expect(screen.getByRole("status")).toHaveTextContent("Starting a temporary sandbox…");
    expect(screen.getByTestId("debug-terminal")).toHaveAttribute("data-state", "connecting");

    act(() => ws.serverOpen());
    expect(ws.sent[0]).toBe(JSON.stringify({ type: "resize", cols: 80, rows: 24 }));
    act(() => ws.serverText({ type: "progress", message: "Restoring snapshot…" }));
    expect(screen.getByRole("status")).toHaveTextContent("Restoring snapshot…");

    act(() => ws.serverBytes("$ "));
    const term = terminals[0];
    expect(String.fromCharCode(...(term.written[0] as Uint8Array))).toBe("$ ");
    expect(screen.getByTestId("debug-terminal")).toHaveAttribute("data-state", "connected");
    expect(screen.getByTestId("terminal-countdown")).toHaveTextContent(/Expires in (20:00|19:5\d)/);

    act(() => term.dataListener?.("ls\r"));
    expect(String.fromCharCode(...(ws.sent[1] as Uint8Array))).toBe("ls\r");
    act(() => term.resizeListener?.({ cols: 120, rows: 40 }));
    expect(ws.sent[2]).toBe(JSON.stringify({ type: "resize", cols: 120, rows: 40 }));
  });

  it("uses the server's deadline when it sends one", async () => {
    renderTerminal();
    const ws = await connect();
    act(() => {
      ws.serverOpen();
      ws.serverText({ type: "ready", expiresAt: new Date(Date.now() + 5 * 60 * 1000).toISOString() });
    });
    expect(screen.getByTestId("terminal-countdown")).toHaveTextContent(/Expires in (5:00|4:5\d)/);
  });

  it("disconnects on request and can reconnect", async () => {
    renderTerminal();
    const ws = await connect();
    act(() => {
      ws.serverOpen();
      ws.serverBytes("$ ");
    });
    fireEvent.click(screen.getByRole("button", { name: "Stop terminal" }));
    expect(ws.closeCalls[0]).toEqual([1000, "closed by user"]);
    expect(screen.getByTestId("debug-terminal")).toHaveAttribute("data-state", "closed");
    expect(screen.getByRole("status")).toHaveTextContent("Disconnected. The temporary sandbox was stopped.");
    fireEvent.click(screen.getByRole("button", { name: "Start terminal" }));
    await waitFor(() => expect(FakeWebSocket.instances).toHaveLength(2));
    expect(terminals[0].disposed).toBe(true);
  });

  it("reports a server close and an error frame", async () => {
    renderTerminal();
    const ws = await connect();
    act(() => ws.serverOpen());
    act(() => ws.serverText({ type: "error", message: "Sandbox failed to start" }));
    expect(screen.getByTestId("debug-terminal")).toHaveAttribute("data-state", "error");
    expect(screen.getByText("Terminal failed")).toBeVisible();
    expect(screen.getByRole("status")).toHaveTextContent("Sandbox failed to start");
    act(() => ws.serverClose(1011, ""));
    expect(screen.getByTestId("debug-terminal")).toHaveAttribute("data-state", "error");
    expect(String(terminals[0].written.at(-1))).toContain("[disconnected]");
    fireEvent.click(screen.getByRole("button", { name: "Retry terminal" }));
    await waitFor(() => expect(FakeWebSocket.instances).toHaveLength(2));
    expect(screen.getByTestId("debug-terminal")).toHaveAttribute("data-state", "connecting");
  });

  it("shows the server's close reason when the sandbox deadline hits", async () => {
    renderTerminal();
    const ws = await connect();
    act(() => {
      ws.serverOpen();
      ws.serverBytes("$ ");
      ws.serverClose(1000, "Sandbox expired");
    });
    expect(screen.getByTestId("debug-terminal")).toHaveAttribute("data-state", "closed");
    expect(screen.getByRole("status")).toHaveTextContent("Sandbox expired");
  });

  it("closes the socket when unmounted", async () => {
    const view = renderTerminal();
    const ws = await connect();
    act(() => ws.serverOpen());
    view.unmount();
    expect(ws.closeCalls).toHaveLength(1);
    expect(terminals[0].disposed).toBe(true);
  });

  it("runs the prepare script only after the secrets warning is confirmed", async () => {
    renderTerminal();
    const ws = await connect();
    act(() => {
      ws.serverOpen();
      ws.serverBytes("$ ");
    });
    fireEvent.click(screen.getByRole("button", { name: "Run prepare script" }));
    expect(await screen.findByText("Secrets are injected into this sandbox, so anyone using this terminal can read them.")).toBeVisible();
    const buttons = screen.getAllByRole("button", { name: "Run prepare script" });
    fireEvent.click(buttons[buttons.length - 1]);
    await waitFor(() => expect(ws.sent).toContain(JSON.stringify({ type: "runPrepare" })));
  });

  it("is unavailable when the server turns the terminal off", () => {
    renderTerminal({ available: false });
    expect(screen.getByRole("button", { name: "Start terminal" })).toBeDisabled();
    expect(screen.getByText("The debug terminal is turned off on this server.")).toBeVisible();
  });
});
