import { useState } from "react";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { useOptionalTranslate } from "@/i18n/i18n";
import type { ConnectorPolicy, ConnectorToolAccess } from "./connector-api";

import "./connector-tool-access.css";

/**
 * Security › Agent connectors › Tool access: a workspace admin decides per tool whether Flow treats a connector's
 * tool as read-only or as a tool that changes data, instead of the connector's own readOnlyHint. A loop that may not
 * change anything refuses the tools treated as writes, and Flow Agent counts their calls as changes in its answers.
 */
export function ConnectorToolAccessDialog({ item, onSave, onClose }: { item: ConnectorPolicy; onSave: (item: ConnectorPolicy) => Promise<void>; onClose: () => void }) {
  const t = useOptionalTranslate();
  const [access, setAccess] = useState<Record<string, ConnectorToolAccess>>(() => ({ ...(item.toolAccess ?? {}) }));
  const [busy, setBusy] = useState(false);
  const readOnly = new Set(item.readOnlyTools ?? []);
  // Overrides of tools the connector no longer lists stay visible, so they can be removed.
  const tools = [...(item.tools ?? []), ...Object.keys(access).filter(name => !(item.tools ?? []).includes(name))];
  const change = (tool: string, value: string) => setAccess(current => {
    const next = { ...current };
    if (value === "read" || value === "write") next[tool] = value;
    else delete next[tool];
    return next;
  });
  const save = async () => {
    setBusy(true);
    try {
      const next: ConnectorPolicy = { ...item, toolAccess: access };
      if (!Object.keys(access).length) delete next.toolAccess;
      await onSave(next);
      onClose();
    } finally {
      setBusy(false);
    }
  };
  return <Dialog open onOpenChange={open => { if (!open && !busy) onClose(); }}>
    <DialogContent className="feature-dialog connector-tool-access">
      <DialogTitle>{t("Tool access")}</DialogTitle>
      <p>{t("Connectors say themselves which of their tools only read data. Override that for a tool here: loops that may not change anything refuse the tools treated as writes, and Flow Agent counts their calls as changes when it reports what it did.")}</p>
      {tools.length === 0
        ? <p>{t("Connect the connector to list its tools.")}</p>
        : <ul className="connector-tool-access-list">
          {tools.map(tool => <li key={tool}>
            <label>
              <span>{tool}</span>
              <select aria-label={t("Access of {tool}").replace("{tool}", tool)} value={access[tool] ?? ""} disabled={busy} onChange={event => change(tool, event.target.value)}>
                <option value="">{readOnly.has(tool) ? t("Follow connector: read-only") : t("Follow connector: may change data")}</option>
                <option value="read">{t("Treat as read-only")}</option>
                <option value="write">{t("Treat as write")}</option>
              </select>
            </label>
          </li>)}
        </ul>}
      <footer className="feature-dialog-footer">
        <span />
        <span />
        <button className="settings-action" disabled={busy} onClick={onClose}>{t("Cancel")}</button>
        <button className="settings-action" disabled={busy} onClick={() => void save()}>{t("Save")}</button>
      </footer>
    </DialogContent>
  </Dialog>;
}
