import { Filter, MoreHorizontal, Search, ShieldCheck, X } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { toast } from "sonner";
import type { AuditLogEntry, BootstrapData } from "@/types/flow";
import { listAuditLog } from "@/lib/api";
import { SettingsPageTitle, SettingsSelect } from "./settings-primitives";
import { AuditLogStreaming } from "./audit-log-streaming";
import { Toggle } from "@/components/ui/toggle";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";

/**
 * The audit log as the server has it now. Partial reloads (e.g. a loop change
 * refetches only `/api/loops`) leave `data.auditLog` behind, so the page
 * refetches the log when it opens and whenever loops change.
 */
function useAuditLog(data: BootstrapData) {
  const [fetched, setFetched] = useState<AuditLogEntry[]>();
  const admin = ["admin", "owner"].includes(data.viewerRole);
  useEffect(() => {
    if (!admin) return;
    let active = true;
    listAuditLog()
      .then((entries) => {
        if (active && Array.isArray(entries)) setFetched(entries);
      })
      .catch(() => undefined);
    return () => {
      active = false;
    };
  }, [admin, data.auditLog, data.loops]);
  return fetched ?? data.auditLog;
}

export function AuditLogSettings({ data }: { data: BootstrapData }) {
  const [query, setQuery] = useState(""),
    [hideSessions, setHideSessions] = useState(false),
    [eventType, setEventType] = useState("all");
  const auditLog = useAuditLog(data);
  const eventTypes = useMemo(
    () => [...new Set(auditLog.map((item) => item.action))].sort(),
    [auditLog],
  );
  const rows = auditLog
    .filter(
      (item) => !hideSessions || !item.action.toLowerCase().includes("session"),
    )
    .filter((item) => eventType === "all" || item.action === eventType)
    .filter((item) =>
      matches(
        query,
        item.action,
        item.resourceType,
        item.actor.displayName,
        item.actor.email,
        JSON.stringify(item.metadata ?? {}),
      ),
    );
  const copy = async (value: string, label: string) => {
    await navigator.clipboard.writeText(value);
    toast.success(`${label} copied`);
  };
  return (
    <>
      <SettingsPageTitle description="Workspace events are retained for 90 days and available to workspace owners.">
        Audit log
      </SettingsPageTitle>
      <div className="settings-audit-intro">
        <ShieldCheck />
        <p>
          Review account access, subscriptions, integrations, and workspace
          setting changes. Use the API for advanced actor, email, IP, and
          date-range queries.
        </p>
      </div>
      {["admin", "owner"].includes(data.viewerRole) && <AuditLogStreaming />}
      <h3 className="settings-audit-recent">Recent events</h3>
      <div className="settings-audit-controls">
        <label>
          <Search />
          <input
            aria-label="Search audit log"
            placeholder="Search audit log…"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
          />
          {query && (
            <button aria-label="Clear search" onClick={() => setQuery("")}>
              <X />
            </button>
          )}
        </label>
        <label className="settings-audit-event">
          <Filter />
          <SettingsSelect
            align="start"
            label="Event type"
            value={eventType}
            onChange={setEventType}
            options={[
              { value: "all", label: "All event types" },
              ...eventTypes.map((type) => ({
                value: type,
                label: type.replaceAll("_", " "),
                entityName: true,
              })),
            ]}
          />
        </label>
        <label className="settings-audit-toggle">
          <Toggle
            checked={hideSessions}
            label="Hide session events"
            onChange={setHideSessions}
          />
          Hide session events
        </label>
      </div>
      {rows.length ? (
        <div className="settings-audit-table">
          <div className="settings-audit-head">
            <span>Actor</span>
            <span>Event</span>
            <span>Resource</span>
            <span>Date</span>
          </div>
          {rows.map((item) => (
            <div className="settings-audit-row" key={item.id}>
              <span className="settings-audit-avatar">
                {initials(item.actor.displayName)}
              </span>
              <div>
                <strong>{item.actor.displayName}</strong>
                <small>{item.actor.email}</small>
              </div>
              <code>{item.action.replaceAll("_", " ")}</code>
              <span>{item.resourceType.replaceAll("_", " ")}</span>
              <time>{new Date(item.createdAt).toLocaleString()}</time>
              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <button aria-label="Audit entry options">
                    <MoreHorizontal />
                  </button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end">
                  <DropdownMenuItem
                    onSelect={() => void copy(item.id, "Event ID")}
                  >
                    Copy event ID
                  </DropdownMenuItem>
                  <DropdownMenuItem
                    onSelect={() =>
                      void copy(JSON.stringify(item, null, 2), "Event JSON")
                    }
                  >
                    Copy event JSON
                  </DropdownMenuItem>
                </DropdownMenuContent>
              </DropdownMenu>
            </div>
          ))}
        </div>
      ) : (
        <div className="settings-empty compact">
          <ShieldCheck />
          <h3>No audit events</h3>
          <p>Try changing the event type or search query.</p>
        </div>
      )}
    </>
  );
}
function matches(query: string, ...values: string[]) {
  const needle = query.trim().toLowerCase();
  return (
    !needle || values.some((value) => value.toLowerCase().includes(needle))
  );
}
function initials(value: string) {
  return (
    value
      .split(/\s+/)
      .filter(Boolean)
      .map((part) => part[0])
      .join("")
      .slice(0, 2)
      .toUpperCase() || "U"
  );
}
