/**
 * Central command registry used by keyboard shortcuts and command surfaces.
 * Keeping actions in one place means every entry point can share the same
 * enabled state and gives us a small, predictable undo stack for reversible
 * actions.
 */
export type ActionContext = {
  source?: "keyboard" | "command-menu" | "context-menu" | "ui";
  payload?: unknown;
};

export type RegisteredAction = {
  id: string;
  label: string;
  keywords?: string;
  shortcut?: string[];
  group?: string;
  enabled?: (context: ActionContext) => boolean;
  run: (context: ActionContext) => void | Promise<void>;
  undo?: (context: ActionContext) => void | Promise<void>;
};

type HistoryEntry = {
  action: RegisteredAction;
  context: ActionContext;
};

export class ActionRegistry {
  private readonly actions = new Map<string, RegisteredAction>();
  private readonly history: HistoryEntry[] = [];

  register(action: RegisteredAction) {
    this.actions.set(action.id, action);
    return () => this.actions.delete(action.id);
  }

  registerMany(actions: RegisteredAction[]) {
    const unregister = actions.map((action) => this.register(action));
    return () => unregister.forEach((remove) => remove());
  }

  get(id: string) {
    return this.actions.get(id);
  }

  list(context: ActionContext = {}) {
    return [...this.actions.values()].filter((action) =>
      action.enabled ? action.enabled(context) : true,
    );
  }

  async execute(id: string, context: ActionContext = {}) {
    const action = this.actions.get(id);
    if (!action || (action.enabled && !action.enabled(context))) return false;
    await action.run(context);
    if (action.undo) {
      this.history.push({ action, context });
      // Keep the queue bounded while preserving the most recent commands.
      if (this.history.length > 50) this.history.shift();
    }
    return true;
  }

  async undo(context: ActionContext = {}) {
    const entry = this.history.pop();
    if (!entry?.action.undo) return false;
    await entry.action.undo({ ...entry.context, ...context });
    return true;
  }

  clear() {
    this.actions.clear();
    this.history.length = 0;
  }
}
