import { useCallback, useEffect, useRef, useState } from "react";
import * as DropdownMenu from "@radix-ui/react-dropdown-menu";
import * as Popover from "@radix-ui/react-popover";
import { Globe } from "lucide-react";

import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { FlowGlyph } from "@/components/ui/menu-glyphs";
import { FlowDropdownMenuContent, FlowMenuItem, FlowMenuRowContent, FlowMenuSeparator } from "@/components/ui/row-context-menu";
import { useOptionalTranslate } from "@/i18n/i18n";
import { request } from "@/lib/api-client";
import { toast } from "@/lib/toast";

import {
  authorizeConnector,
  connectConnector,
  connectorLabel,
  listConnectorPolicies,
  removeConnectorPolicy,
  saveConnectorPolicy,
} from "./connector-api";
import type { ConnectorPolicy } from "./connector-api";
import { ConnectorDialog, ConnectorMenu } from "./connector-dialog";
import { SettingsConfirmDialog } from "./settings-confirm-dialog";
import { SettingsGroup } from "./settings-primitives";

import "./personal-connectors.css";

type Translate = (source: string) => string;

const count = (t: Translate, value: number, one: string, many: string) =>
  value === 1 ? t(one) : t(many).replace("{count}", String(value));

/**
 * Settings › Agent personalization › MCP connectors: the viewer's own MCP
 * connectors, with a "…" menu per connector (rename, reconnect, tool approval,
 * remove). Workspace connectors are managed in security settings.
 */
export function PersonalConnectorSettings({ viewerId, requireApproval = false }: { viewerId: string; requireApproval?: boolean }) {
  const t = useOptionalTranslate();
  const [items, setItems] = useState<ConnectorPolicy[]>();
  const [adding, setAdding] = useState<{ name: string; url: string }>();
  const [renaming, setRenaming] = useState<ConnectorPolicy>();
  const [removing, setRemoving] = useState<ConnectorPolicy>();
  const reload = useCallback(async () => {
    const all = await listConnectorPolicies();
    const own = all.filter((item) => item.kind === "mcp" && !item.shared && item.ownerId === viewerId);
    setItems(own);
    return own;
  }, [viewerId]);

  useEffect(() => {
    let active = true;
    const params = new URLSearchParams(window.location.search);
    if (params.has("connector_error")) toast.error(t("Connector authorization failed. Try authorizing again."));
    const authorized = params.get("connector") === "authorized";
    if (authorized || params.has("connector_error")) {
      params.delete("connector");
      params.delete("connector_error");
      const query = params.toString();
      window.history.replaceState(window.history.state, "", `${window.location.pathname}${query ? `?${query}` : ""}${window.location.hash}`);
    }
    void reload()
      .then(async (own) => {
        // Back from authorization: read the tools of connectors not connected yet.
        if (!active || !authorized) return;
        const waiting = own.filter((item) => !item.connectedAt);
        if (!waiting.length) return;
        await Promise.all(waiting.map((item) => connectConnector(item.id).catch(() => undefined)));
        if (active) await reload();
      })
      .catch((reason) => toast.error(reason instanceof Error ? reason.message : String(reason)));
    return () => {
      active = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps -- once per mount
  }, [reload]);

  const save = async (item: ConnectorPolicy) => {
    try {
      await saveConnectorPolicy(item);
      await reload();
      return true;
    } catch (reason) {
      toast.error(reason instanceof Error ? reason.message : t("Could not save connector"));
      return false;
    }
  };

  const reconnect = async (item: ConnectorPolicy) => {
    const pending = toast.loading(t("Connecting to MCP server…"));
    try {
      const result = await connectConnector(item.id);
      if (result.authorizationRequired) {
        toast.dismiss(pending);
        await authorizeConnector(item.id);
        return;
      }
      if (!result.connected) {
        toast.error(t("Could not reconnect MCP connector"), { id: pending, description: result.error });
        return;
      }
      toast.success(t("MCP connector reconnected"), {
        id: pending,
        description: <span data-i18n-ignore>{connectorLabel(result.policy ?? item)}</span>,
      });
      await reload();
    } catch (reason) {
      toast.error(reason instanceof Error ? reason.message : t("Could not reconnect MCP connector"), { id: pending });
    }
  };

  const remove = async (item: ConnectorPolicy) => {
    try {
      await removeConnectorPolicy(item.id);
      setRemoving(undefined);
      await reload();
    } catch (reason) {
      toast.error(reason instanceof Error ? reason.message : t("Could not remove connector"));
    }
  };

  const list = items ?? [];
  return (
    <SettingsGroup
      title={t("MCP connectors")}
      description={t(
        "Add MCP connectors for use with Flow Agent. Workspace admins can manage available connectors in security settings.",
      )}
    >
      {items && !list.length && (
        <div className="personal-connectors-empty">
          <h3>{t("No connectors added")}</h3>
          <ConnectorMenu onChoose={(name, url) => setAdding({ name, url })} />
        </div>
      )}
      {list.length > 0 && (
        <section className="personal-connectors" aria-label={t("MCP connectors")}>
          <header>
            <h3>{count(t, list.length, "1 connector added", "{count} connectors added")}</h3>
            <ConnectorMenu compact onChoose={(name, url) => setAdding({ name, url })} />
          </header>
          <ul>
            {list.map((item) => (
              <ConnectorRow
                key={item.id}
                item={item}
                requireApproval={requireApproval}
                onRename={() => setRenaming(item)}
                onReconnect={() => void reconnect(item)}
                onToggleApproval={() => {
                  const { toolApproval, ...rest } = item;
                  void save(toolApproval === "always" ? rest : { ...rest, toolApproval: "always" });
                }}
                onDisconnected={() => void reload()}
                onRemove={() => setRemoving(item)}
              />
            ))}
          </ul>
        </section>
      )}
      {adding && (
        <ConnectorDialog
          initial={adding}
          admin={false}
          connect
          onSaved={async () => {
            await reload();
          }}
          onClose={() => setAdding(undefined)}
        />
      )}
      {renaming && (
        <RenameConnectorDialog
          item={renaming}
          onClose={() => setRenaming(undefined)}
          onSave={async (displayName) => {
            if (await save({ ...renaming, displayName })) setRenaming(undefined);
          }}
        />
      )}
      {removing && (
        <RemoveConnectorDialog item={removing} onClose={() => setRemoving(undefined)} onConfirm={() => remove(removing)} />
      )}
    </SettingsGroup>
  );
}

function ConnectorRow({
  item,
  requireApproval,
  onRename,
  onReconnect,
  onToggleApproval,
  onDisconnected,
  onRemove,
}: {
  item: ConnectorPolicy;
  requireApproval: boolean;
  onRename: () => void;
  onReconnect: () => void;
  onToggleApproval: () => void;
  onDisconnected: () => void;
  onRemove: () => void;
}) {
  const t = useOptionalTranslate();
  const [menuOpen, setMenuOpen] = useState(false);
  const [authorization, setAuthorization] = useState<{ authorized: boolean; customHeaders: boolean }>();
  useEffect(() => {
    if (!menuOpen) return;
    let active = true;
    void request<{ authorized: boolean; customHeaders: boolean }>(
      `/api/application-policies/${encodeURIComponent(item.id)}/authorization`,
      { quiet: true },
    )
      .then((value) => {
        if (active) setAuthorization(value);
      })
      .catch(() => undefined);
    return () => {
      active = false;
    };
  }, [item.id, menuOpen]);
  const status =
    item.status === "rejected"
      ? t("Rejected by an admin")
      : requireApproval && item.status !== "approved"
        ? t("Pending approval")
        : "";
  const label = connectorLabel(item);
  return (
    <li className={`personal-connector-row${menuOpen ? " is-active" : ""}`}>
      <figure aria-hidden="true">
        <Globe size={16} strokeWidth={1.5} />
      </figure>
      <div className="personal-connector-copy">
        <strong data-i18n-ignore>{label}</strong>
        <span>
          {item.connectedAt && <ConnectorTools tools={item.tools ?? []} />}
          {item.connectedAt && <span aria-hidden="true">·</span>}
          <span className="personal-connector-url" data-i18n-ignore>
            {item.url}
          </span>
          {status && <span aria-hidden="true">·</span>}
          {status && <span>{status}</span>}
        </span>
      </div>
      <DropdownMenu.Root open={menuOpen} onOpenChange={setMenuOpen}>
        <DropdownMenu.Trigger asChild>
          <button type="button" className="personal-connector-more" aria-label={t("Connector options")}>
            <FlowGlyph name="ellipsis" size={14} />
          </button>
        </DropdownMenu.Trigger>
        <DropdownMenu.Portal>
          <FlowDropdownMenuContent align="end" label={t("Connector options")}>
            <FlowMenuItem icon={<FlowGlyph name="edit" />} label="Rename…" onSelect={onRename} />
            <FlowMenuItem icon={<FlowGlyph name="rerun" />} label="Reconnect" onSelect={onReconnect} />
            <DropdownMenu.CheckboxItem
              className="flow-menu__row"
              checked={item.toolApproval === "always"}
              onCheckedChange={onToggleApproval}
            >
              <FlowMenuRowContent
                icon={<FlowGlyph name="lock" />}
                label={t("Ask before running tools")}
                trailing={
                  item.toolApproval === "always" ? (
                    <span className="flow-menu__check" aria-hidden="true">
                      <FlowGlyph name="check" />
                    </span>
                  ) : undefined
                }
              />
            </DropdownMenu.CheckboxItem>
            {(authorization?.authorized || authorization?.customHeaders) && (
              <FlowMenuItem
                icon={<FlowGlyph name="unsubscribe" />}
                label="Sign out of connector"
                onSelect={() => {
                  void request(`/api/application-policies/${encodeURIComponent(item.id)}/authorization`, { method: "DELETE" })
                    .then(() => {
                      setAuthorization({ authorized: false, customHeaders: false });
                      onDisconnected();
                    })
                    .catch((reason) => toast.error(reason instanceof Error ? reason.message : String(reason)));
                }}
              />
            )}
            <FlowMenuSeparator />
            <FlowMenuItem icon={<FlowGlyph name="delete" />} label="Remove connector" onSelect={onRemove} />
          </FlowDropdownMenuContent>
        </DropdownMenu.Portal>
      </DropdownMenu.Root>
    </li>
  );
}

/** "N tools": the list of the connector's tool names opens on hover (or focus, or a click) and stays while the pointer is on it. */
function ConnectorTools({ tools }: { tools: string[] }) {
  const t = useOptionalTranslate();
  const [open, setOpen] = useState(false);
  const timer = useRef<number>(undefined);
  const show = () => {
    window.clearTimeout(timer.current);
    setOpen(true);
  };
  const hide = () => {
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => setOpen(false), 120);
  };
  useEffect(() => () => window.clearTimeout(timer.current), []);
  const text = count(t, tools.length, "1 tool", "{count} tools");
  if (!tools.length) return <span>{text}</span>;
  return (
    <Popover.Root open={open} onOpenChange={(next) => !next && setOpen(false)}>
      <Popover.Anchor asChild>
        <button
          type="button"
          className="personal-connector-tools"
          data-state={open ? "open" : "closed"}
          aria-expanded={open}
          onPointerEnter={show}
          onPointerLeave={hide}
          onFocus={show}
          onBlur={hide}
          onClick={show}
        >
          {text}
        </button>
      </Popover.Anchor>
      <Popover.Portal>
        <Popover.Content
          className="personal-connector-tool-list"
          data-flow-motion="floating"
          align="start"
          sideOffset={8}
          onOpenAutoFocus={(event) => event.preventDefault()}
          onCloseAutoFocus={(event) => event.preventDefault()}
          onPointerEnter={show}
          onPointerLeave={hide}
        >
          <ul data-i18n-ignore>
            {tools.map((tool) => (
              <li key={tool}>{tool}</li>
            ))}
          </ul>
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>
  );
}

function RenameConnectorDialog({
  item,
  onClose,
  onSave,
}: {
  item: ConnectorPolicy;
  onClose: () => void;
  onSave: (displayName: string) => Promise<void>;
}) {
  const t = useOptionalTranslate();
  const [value, setValue] = useState(item.displayName ?? "");
  const [busy, setBusy] = useState(false);
  const input = useRef<HTMLInputElement>(null);
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !busy) onClose();
      }}
    >
      <DialogContent
        className="personal-connector-dialog personal-connector-rename"
        aria-describedby={undefined}
        closeLabel={t("Close")}
        onOpenAutoFocus={(event) => {
          // The close button takes focus first; the name field is one Tab away.
          event.preventDefault();
          (event.target as HTMLElement).querySelector<HTMLElement>(".dialog-close")?.focus();
        }}
      >
        <form
          onSubmit={async (event) => {
            event.preventDefault();
            setBusy(true);
            try {
              await onSave(value.trim());
            } finally {
              setBusy(false);
            }
          }}
        >
          <DialogTitle>{t("Rename connector")}</DialogTitle>
          <label>
            <span>{t("Display name")}</span>
            <input
              ref={input}
              value={value}
              maxLength={200}
              placeholder={item.name}
              disabled={busy}
              onChange={(event) => setValue(event.target.value)}
            />
          </label>
          <p>{t("Clear the name to use the connector’s default name.")}</p>
          <footer>
            <button type="button" disabled={busy} onClick={onClose}>
              {t("Cancel")}
            </button>
            <button type="submit" className="primary" disabled={busy}>
              {t("Save")}
            </button>
          </footer>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function RemoveConnectorDialog({
  item,
  onClose,
  onConfirm,
}: {
  item: ConnectorPolicy;
  onClose: () => void;
  onConfirm: () => Promise<void>;
}) {
  const t = useOptionalTranslate();
  const [busy, setBusy] = useState(false);
  return (
    <SettingsConfirmDialog
      title={t("Remove {name}?").replace("{name}", connectorLabel(item))}
      description={t("This MCP connector will be removed and its tools will no longer be available to Flow Agent.")}
      confirmLabel={t("Remove")}
      busy={busy}
      onCancel={onClose}
      onConfirm={async () => {
        setBusy(true);
        try {
          await onConfirm();
        } finally {
          setBusy(false);
        }
      }}
    />
  );
}
