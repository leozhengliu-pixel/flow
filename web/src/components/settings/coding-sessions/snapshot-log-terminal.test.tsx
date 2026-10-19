import { act, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { THEME_CHANGE_EVENT } from "@/lib/theme";
import { SNAPSHOT_LOG_SCROLLBACK, stripAnsi } from "./coding-xterm";
import { SnapshotLogTerminal } from "./snapshot-log-terminal";

const terminals: FakeTerminal[] = [];
let failLoad = false;
class FakeTerminal {
  written: string[] = [];
  resets = 0;
  disposed = false;
  opened?: HTMLElement;
  options: Record<string, unknown>;
  constructor(options: Record<string, unknown>) {
    if (failLoad) throw new Error("no canvas");
    this.options = { ...options };
    terminals.push(this);
  }
  open(element: HTMLElement) {
    this.opened = element;
  }
  write(data: string) {
    this.written.push(data);
  }
  reset() {
    this.resets += 1;
    this.written = [];
  }
  loadAddon() {}
  dispose() {
    this.disposed = true;
  }
}
vi.mock("@xterm/xterm", () => ({ Terminal: FakeTerminal }));
vi.mock("@xterm/addon-fit", () => ({ FitAddon: class { fit() {} } }));
vi.mock("@xterm/xterm/css/xterm.css", () => ({}));

beforeEach(() => {
  terminals.length = 0;
  failLoad = false;
  document.documentElement.style.removeProperty("--theme-text-primary");
});

describe("SnapshotLogTerminal", () => {
  it("renders the log in a read-only xterm and appends polled output", async () => {
    document.documentElement.style.setProperty("--theme-text-primary", "rgb(1, 2, 3)");
    const { rerender, unmount } = render(<SnapshotLogTerminal text={"\x1b[32mcloning\x1b[0m\n"} placeholder="Loading…" />);
    await waitFor(() => expect(screen.getByTestId("snapshot-logs")).toHaveAttribute("data-ready"));
    const terminal = terminals[0];
    expect(terminal.options).toMatchObject({ disableStdin: true, convertEol: true, scrollback: SNAPSHOT_LOG_SCROLLBACK, fontSize: 13, lineHeight: 1.5 });
    expect((terminal.options.theme as Record<string, string>).foreground).toBe("rgb(1, 2, 3)");
    expect(terminal.opened).toBeInstanceOf(HTMLElement);
    // ANSI colours go to xterm untouched; the screen-reader copy is plain text.
    expect(terminal.written.join("")).toContain("\x1b[32mcloning\x1b[0m\n");
    expect(screen.getByTestId("snapshot-logs").querySelector("pre")).toHaveTextContent("cloning");
    expect(screen.getByTestId("snapshot-logs").querySelector("pre")).toHaveClass("sr-only");

    rerender(<SnapshotLogTerminal text={"\x1b[32mcloning\x1b[0m\nmise install\n"} placeholder="Loading…" />);
    expect(terminal.written.at(-1)).toBe("mise install\n");
    expect(terminal.resets).toBe(0);

    // A log that no longer extends what was written (e.g. truncated) redraws from scratch.
    rerender(<SnapshotLogTerminal text={"restarted\n"} placeholder="Loading…" />);
    expect(terminal.resets).toBe(1);
    expect(terminal.written.join("")).toContain("restarted\n");

    unmount();
    expect(terminal.disposed).toBe(true);
  });

  it("repaints with the new token values when the theme changes", async () => {
    render(<SnapshotLogTerminal text="ok" placeholder="Loading…" />);
    await waitFor(() => expect(terminals).toHaveLength(1));
    await waitFor(() => expect(screen.getByTestId("snapshot-logs")).toHaveAttribute("data-ready"));
    document.documentElement.style.setProperty("--theme-text-primary", "rgb(9, 9, 9)");
    await act(async () => {
      window.dispatchEvent(new CustomEvent(THEME_CHANGE_EVENT, { detail: "dark" }));
      await new Promise((resolve) => requestAnimationFrame(resolve));
    });
    await waitFor(() => expect((terminals[0].options.theme as Record<string, string>).foreground).toBe("rgb(9, 9, 9)"));
  });

  it("shows the placeholder for an empty log", async () => {
    render(<SnapshotLogTerminal text="" placeholder="No output yet" />);
    await waitFor(() => expect(screen.getByTestId("snapshot-logs")).toHaveAttribute("data-ready"));
    expect(screen.getByText("No output yet")).toHaveClass("cs-logs-fallback");
  });

  it("falls back to plain text when xterm cannot load", async () => {
    failLoad = true;
    render(<SnapshotLogTerminal text={"\x1b[31mnpm ERR!\x1b[0m"} placeholder="Loading…" />);
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(screen.getByText("npm ERR!")).toHaveClass("cs-logs-fallback");
    expect(screen.getByTestId("snapshot-logs")).not.toHaveAttribute("data-ready");
  });
});

describe("stripAnsi", () => {
  it("removes colour, cursor and OSC sequences", () => {
    expect(stripAnsi("\x1b[1;31merror\x1b[0m \x1b[?25lhidden\x1b]0;title\x07done")).toBe("error hiddendone");
  });
});
