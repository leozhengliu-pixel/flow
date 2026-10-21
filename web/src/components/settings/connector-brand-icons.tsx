import { Plus } from "lucide-react";

import { IntegrationBrandIcon } from "./integration-brand-icon";

/** Brand colour and initial of the connectors that have no dedicated glyph here. */
const MARKS: Record<string, { color: string; letter: string; ink?: string }> = {
  Amplitude: { color: "#1e61f0", letter: "A" },
  Attio: { color: "#1b1b1f", letter: "A" },
  BetterStack: { color: "#2a2a30", letter: "B" },
  Datadog: { color: "#632ca6", letter: "D" },
  Glean: { color: "#343ced", letter: "g" },
  Granola: { color: "#5e9b1a", letter: "G" },
  HubSpot: { color: "#ff7a59", letter: "H" },
  "incident.io": { color: "#f25533", letter: "i" },
  Intercom: { color: "#286efa", letter: "I" },
  Jam: { color: "#f5c400", letter: "J", ink: "#1b1b1f" },
  PostHog: { color: "#f9bd2b", letter: "P", ink: "#1b1b1f" },
  Sanity: { color: "#f36458", letter: "S" },
  Sentry: { color: "#4a3d78", letter: "S" },
  Stripe: { color: "#635bff", letter: "S" },
  Zapier: { color: "#ff4a00", letter: "Z" },
};

/** The 16px brand mark beside a catalog connector in the add menu; "Custom URL…" gets a plus. */
export function ConnectorBrandIcon({ name }: { name: string }) {
  if (name === "custom") return <Plus size={16} strokeWidth={1.5} aria-hidden="true" />;
  if (name === "GitHub") return <IntegrationBrandIcon provider="github" size={16} />;
  if (name === "Notion") return <IntegrationBrandIcon provider="notion" size={16} />;
  if (name === "Slack Personal") return <IntegrationBrandIcon provider="slack" size={16} />;
  if (name === "Figma") {
    return (
      <svg width="16" height="16" viewBox="0 0 16 16" aria-hidden="true">
        <path fill="#f24e1e" d="M5 1h3v4H5a2 2 0 0 1 0-4Z" />
        <path fill="#ff7262" d="M8 1h3a2 2 0 0 1 0 4H8V1Z" />
        <path fill="#a259ff" d="M5 5h3v4H5a2 2 0 0 1 0-4Z" />
        <circle cx="10" cy="7" r="2" fill="#1abcfe" />
        <path fill="#0acf83" d="M5 9h3v2a2 2 0 1 1-3-1.73A2 2 0 0 1 5 9Z" />
      </svg>
    );
  }
  const mark = MARKS[name];
  if (!mark) return <span className="connector-brand-blank" aria-hidden="true" />;
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" aria-hidden="true">
      <circle cx="8" cy="8" r="7.5" fill={mark.color} />
      <text
        x="8"
        y="11.2"
        textAnchor="middle"
        fontFamily="Inter Variable, Arial, sans-serif"
        fontSize="9"
        fontWeight="700"
        fill={mark.ink ?? "#fff"}
      >
        {mark.letter}
      </text>
    </svg>
  );
}
