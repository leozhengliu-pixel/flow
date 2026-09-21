import { useCallback, useEffect, useState } from "react";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { fetchWebhookFailures } from "@/lib/api";
import type { WebhookFailureEvent } from "@/types/flow";
import "./webhook-failure-events.css";

function formatFailureTimestamp(value: string) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return new Intl.DateTimeFormat(document.documentElement.lang || "en-US", {
    month: "short",
    day: "numeric",
    hour: "numeric",
    minute: "2-digit",
  }).format(date);
}

function FailureDetailDialog({
  failure,
  onClose,
}: {
  failure: WebhookFailureEvent;
  onClose: () => void;
}) {
  return (
    <Dialog open onOpenChange={(value) => !value && onClose()}>
      <DialogContent className="webhook-failure-detail-dialog">
        <DialogTitle>Webhook delivery failure</DialogTitle>
        <dl className="webhook-failure-detail-list">
          <div>
            <dt>Execution ID</dt>
            <dd>{failure.executionId}</dd>
          </div>
          <div>
            <dt>Timestamp</dt>
            <dd>
              {formatFailureTimestamp(failure.createdAt)}
              <br />
              <span className="webhook-failure-muted">
                {new Date(failure.createdAt).toISOString()}
              </span>
            </dd>
          </div>
          <div>
            <dt>URL</dt>
            <dd className="webhook-failure-mono">{failure.url}</dd>
          </div>
          <div>
            <dt>HTTP status</dt>
            <dd>{failure.httpStatus ?? "—"}</dd>
          </div>
          <div>
            <dt>Response</dt>
            <dd>
              <pre className="webhook-failure-response">
                {failure.responseOrError}
              </pre>
            </dd>
          </div>
        </dl>
      </DialogContent>
    </Dialog>
  );
}

export function WebhookFailureEvents({ webhookId }: { webhookId: string }) {
  const [events, setEvents] = useState<WebhookFailureEvent[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [selected, setSelected] = useState<WebhookFailureEvent | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const items = await fetchWebhookFailures(webhookId);
      setEvents(items);
    } catch (reason) {
      setError(
        reason instanceof Error
          ? reason.message
          : "Could not load delivery failures",
      );
    } finally {
      setLoading(false);
    }
  }, [webhookId]);

  useEffect(() => {
    void load();
  }, [load]);

  return (
    <section className="webhook-failure-events" aria-label="Delivery failures">
      <header className="webhook-failure-events-header">
        <h3>Delivery failures</h3>
        <p>Recent outbound deliveries that did not succeed.</p>
      </header>

      {loading && (
        <div className="webhook-failure-state" role="status">
          Loading failures…
        </div>
      )}

      {!loading && error && (
        <div className="webhook-failure-state webhook-failure-error" role="alert">
          <p>{error}</p>
          <button type="button" onClick={() => void load()}>
            Try again
          </button>
        </div>
      )}

      {!loading && !error && events.length === 0 && (
        <div className="webhook-failure-state webhook-failure-empty">
          <h4>No failures recorded for this webhook</h4>
          <p>Failed deliveries will appear here.</p>
        </div>
      )}

      {!loading && !error && events.length > 0 && (
        <div className="webhook-failure-table-wrap">
          <table className="webhook-failure-table">
            <thead>
              <tr>
                <th>Timestamp</th>
                <th>Status</th>
                <th>Response</th>
                <th>Details</th>
              </tr>
            </thead>
            <tbody>
              {events.map((event) => (
                <tr key={event.id}>
                  <td>
                    <time dateTime={event.createdAt}>
                      {formatFailureTimestamp(event.createdAt)}
                    </time>
                  </td>
                  <td>{event.httpStatus ?? "—"}</td>
                  <td className="webhook-failure-response-cell">
                    {event.responseOrError}
                  </td>
                  <td>
                    <button
                      type="button"
                      className="webhook-failure-view"
                      aria-label={`View delivery failure from ${formatFailureTimestamp(event.createdAt)}`}
                      onClick={() => setSelected(event)}
                    >
                      View
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {selected && (
        <FailureDetailDialog
          failure={selected}
          onClose={() => setSelected(null)}
        />
      )}
    </section>
  );
}
