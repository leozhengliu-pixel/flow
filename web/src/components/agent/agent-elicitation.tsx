import { useState } from "react";
import { ExternalLink } from "lucide-react";
import { request, jsonRequest } from "@/lib/api-client";
import type { AgentMessagePart } from "@/types/flow";
import "./agent-elicitation.css";

type Choice = { const: string; title?: string };
export type ElicitationField = {
  type?: string;
  title?: string;
  description?: string;
  default?: unknown;
  format?: string;
  pattern?: string;
  enum?: string[];
  enumNames?: string[];
  oneOf?: Choice[];
  anyOf?: Choice[];
  minimum?: number;
  maximum?: number;
  minLength?: number;
  maxLength?: number;
  minItems?: number;
  maxItems?: number;
  items?: ElicitationField;
};
export type ElicitationPrompt = {
  id: string;
  sessionId: string;
  connectorName: string;
  connectorUrl: string;
  mode: "form" | "url";
  message: string;
  url?: string;
  schema?: {
    type: string;
    properties?: Record<string, ElicitationField>;
    required?: string[];
  };
  action?: string;
};

function choices(field: ElicitationField): Choice[] {
  return (
    field.oneOf ??
    field.anyOf ??
    field.enum?.map((value, index) => ({
      const: value,
      title: field.enumNames?.[index] ?? value,
    })) ??
    []
  );
}

export function AgentElicitation({ part }: { part: AgentMessagePart }) {
  const prompt = part.elicitation;
  const [values, setValues] = useState<Record<string, unknown>>(() =>
    Object.fromEntries(
      Object.entries(prompt?.schema?.properties ?? {})
        .filter(([, field]) => field.default !== undefined || field.type==='boolean' || field.type==='array')
        .map(([name, field]) => [name, field.default ?? (field.type==='boolean'?false:[])]),
    ),
  );
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [action, setAction] = useState("");
  if (!prompt) return null;
  const completed = prompt.action || action;
  const expired = part.status === "error" && !completed;
  const respond = async (next: "accept" | "decline" | "cancel") => {
    setBusy(true);
    setError("");
    try {
      await request(
        `/api/agent/sessions/${encodeURIComponent(prompt.sessionId)}/elicitations/${encodeURIComponent(prompt.id)}`,
        jsonRequest("POST", {
          action: next,
          ...(next === "accept" && prompt.mode === "form"
            ? { content: values }
            : {}),
        }),
      );
      setAction(next);
    } catch (reason) {
      setError(
        reason instanceof Error ? reason.message : "Could not submit response",
      );
    } finally {
      setBusy(false);
    }
  };
  return (
    <section
      className="agent-elicitation"
      aria-label={`${prompt.connectorName} requests information`}
    >
      <header>
        <strong>{prompt.connectorName}</strong>
        <span>{new URL(prompt.connectorUrl).host}</span>
      </header>
      <p>{prompt.message}</p>
      {completed ? (
        <p role="status">
          {completed === "accept"
            ? "Response sent"
            : completed === "decline"
              ? "Request declined"
              : "Request canceled"}
        </p>
      ) : expired ? (
        <p role="status">This request has expired.</p>
      ) : (
        <form
          onSubmit={(event) => {
            event.preventDefault();
            void respond("accept");
          }}
        >
          {prompt.mode === "url" ? (
            <a
              className="agent-elicitation-link"
              href={prompt.url}
              target="_blank"
              rel="noopener noreferrer"
            >
              Open {new URL(prompt.url!).host}
              <ExternalLink size={14} />
            </a>
          ) : (
            Object.entries(prompt.schema?.properties ?? {}).map(
              ([key, field]) => {
                const required = prompt.schema?.required?.includes(key);
                const label = field.title || key;
                const options = choices(field);
                const id = `${prompt.id}-${key}`;
                const change = (value: unknown) =>
                  setValues((current) => ({ ...current, [key]: value }));
                return (
                  <div className="agent-elicitation-field" key={key}>
                    <label htmlFor={id}>
                      {label}
                      {required ? " *" : ""}
                    </label>
                    {field.description && <small>{field.description}</small>}
                    {field.type === "boolean" ? (
                      <input
                        id={id}
                        type="checkbox"
                        checked={Boolean(values[key])}
                        disabled={busy}
                        onChange={(e) => change(e.target.checked)}
                      />
                    ) : field.type === "array" ? (
                      <fieldset aria-label={label}>
                        {choices(field.items ?? {}).map((choice) => (
                          <label key={choice.const}>
                            <input
                              type="checkbox"
                              checked={
                                Array.isArray(values[key]) &&
                                (values[key] as string[]).includes(choice.const)
                              }
                              disabled={busy}
                              onChange={(e) => {
                                const selected = Array.isArray(values[key])
                                  ? (values[key] as string[])
                                  : [];
                                change(
                                  e.target.checked
                                    ? [...selected, choice.const]
                                    : selected.filter(
                                        (value) => value !== choice.const,
                                      ),
                                );
                              }}
                            />
                            {choice.title || choice.const}
                          </label>
                        ))}
                      </fieldset>
                    ) : options.length ? (
                      <select
                        id={id}
                        required={required}
                        disabled={busy}
                        value={String(values[key] ?? "")}
                        onChange={(e) => change(e.target.value)}
                      >
                        <option value="">Select…</option>
                        {options.map((choice) => (
                          <option key={choice.const} value={choice.const}>
                            {choice.title || choice.const}
                          </option>
                        ))}
                      </select>
                    ) : (
                      <input
                        id={id}
                        required={required}
                        disabled={busy}
                        type={
                          field.type === "number" || field.type === "integer"
                            ? "number"
                            : field.format === "email"
                              ? "email"
                              : field.format === "uri"
                                ? "url"
                                : field.format === "date"
                                  ? "date"
                                  : "text"
                        }
                        min={field.minimum}
                        max={field.maximum}
                        step={field.type === "integer" ? 1 : "any"}
                        minLength={field.minLength}
                        maxLength={field.maxLength ?? 8000}
                        pattern={field.pattern}
                        value={String(values[key] ?? "")}
                        onChange={(e) => {
                          if (
                            field.type === "number" ||
                            field.type === "integer"
                          ) {
                            if (e.target.value === "") {
                              setValues((current) => {
                                const next = { ...current };
                                delete next[key];
                                return next;
                              });
                            } else {
                              change(e.target.valueAsNumber);
                            }
                          } else {
                            change(e.target.value);
                          }
                        }}
                      />
                    )}
                  </div>
                );
              },
            )
          )}
          {error && <p role="alert">{error}</p>}
          <footer>
            <button
              type="button"
              disabled={busy}
              onClick={() => void respond("cancel")}
            >
              Cancel
            </button>
            <button
              type="button"
              disabled={busy}
              onClick={() => void respond("decline")}
            >
              Decline
            </button>
            <button type="submit" disabled={busy}>
              {prompt.mode === "url" ? "Continue" : "Submit"}
            </button>
          </footer>
        </form>
      )}
    </section>
  );
}
