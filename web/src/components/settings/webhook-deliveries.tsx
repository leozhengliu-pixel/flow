import { useCallback, useEffect, useState } from "react";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import {
  fetchWebhookDeliveries,
  fetchWebhookDelivery,
  redeliverWebhookDelivery,
} from "@/lib/api";
import { toast } from "@/lib/toast";
import type { WebhookDelivery, WebhookDeliveryStatus } from "@/types/flow";
import "./webhook-deliveries.css";

function formatTimestamp(value?: string) {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return new Intl.DateTimeFormat(document.documentElement.lang || "en-US", {
    month: "short",
    day: "numeric",
    hour: "numeric",
    minute: "2-digit",
    second: "2-digit",
  }).format(date);
}

const STATUS_LABELS: Record<WebhookDeliveryStatus, string> = {
  pending: "Pending",
  delivering: "Sending",
  retrying: "Retrying",
  succeeded: "Delivered",
  failed: "Failed",
  canceled: "Canceled",
};

function StatusBadge({ status }: { status: WebhookDeliveryStatus }) {
  return (
    <span className={`webhook-delivery-status is-${status}`}>
      {STATUS_LABELS[status] ?? status}
    </span>
  );
}

function eventLabel(delivery: WebhookDelivery) {
  return `${delivery.eventType} · ${delivery.action}`;
}

function prettyJSON(value: unknown) {
  if (value === undefined || value === null) return "";
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}

function DeliveryDetailDialog({
  webhookId,
  deliveryId,
  canRedeliver,
  onClose,
  onRedelivered,
}: {
  webhookId: string;
  deliveryId: string;
  canRedeliver: boolean;
  onClose: () => void;
  onRedelivered: (delivery: WebhookDelivery) => void;
}) {
  const [delivery, setDelivery] = useState<WebhookDelivery | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let active = true;
    fetchWebhookDelivery(webhookId, deliveryId)
      .then((item) => active && setDelivery(item))
      .catch(
        (reason) =>
          active &&
          setError(
            reason instanceof Error ? reason.message : "Could not load delivery",
          ),
      );
    return () => {
      active = false;
    };
  }, [webhookId, deliveryId]);

  const redeliver = async () => {
    setBusy(true);
    try {
      const result = await redeliverWebhookDelivery(webhookId, deliveryId);
      if (result.status === "succeeded") toast.success("Delivery sent");
      else toast.error("Redelivery failed");
      onRedelivered(result);
    } catch (reason) {
      toast.error(
        reason instanceof Error ? reason.message : "Could not redeliver",
      );
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open onOpenChange={(value) => !value && onClose()}>
      <DialogContent className="webhook-delivery-dialog">
        <DialogTitle>Webhook delivery</DialogTitle>
        {error ? (
          <p className="webhook-delivery-error" role="alert">
            {error}
          </p>
        ) : !delivery ? (
          <p className="webhook-delivery-muted" role="status">
            Loading delivery…
          </p>
        ) : (
          <div className="webhook-delivery-detail">
            <dl className="webhook-delivery-facts">
              <div>
                <dt>Event</dt>
                <dd data-i18n-ignore>{eventLabel(delivery)}</dd>
              </div>
              <div>
                <dt>Status</dt>
                <dd>
                  <StatusBadge status={delivery.status} />
                </dd>
              </div>
              <div>
                <dt>Delivery ID</dt>
                <dd className="webhook-delivery-mono" data-i18n-ignore>
                  {delivery.id}
                </dd>
              </div>
              <div>
                <dt>URL</dt>
                <dd className="webhook-delivery-mono" data-i18n-ignore>
                  {delivery.url}
                </dd>
              </div>
              <div>
                <dt>Attempts</dt>
                <dd>
                  {delivery.attempt} / {delivery.maxAttempts}
                </dd>
              </div>
              {delivery.nextAttemptAt ? (
                <div>
                  <dt>Next attempt</dt>
                  <dd>{formatTimestamp(delivery.nextAttemptAt)}</dd>
                </div>
              ) : null}
              {delivery.redeliveryOf ? (
                <div>
                  <dt>Redelivery of</dt>
                  <dd className="webhook-delivery-mono" data-i18n-ignore>
                    {delivery.redeliveryOf}
                  </dd>
                </div>
              ) : null}
            </dl>
            {delivery.attempts.length > 0 ? (
              <section aria-label="Attempts">
                <h4>Attempts</h4>
                <ol className="webhook-delivery-attempts">
                  {delivery.attempts.map((attempt) => (
                    <li key={attempt.attempt}>
                      <span>#{attempt.attempt}</span>
                      <time dateTime={attempt.at}>
                        {formatTimestamp(attempt.at)}
                      </time>
                      <span>{attempt.responseStatus ?? "—"}</span>
                      <span>{attempt.durationMs} ms</span>
                      <span className="webhook-delivery-attempt-result">
                        {attempt.succeeded ? "OK" : attempt.error || "Failed"}
                      </span>
                    </li>
                  ))}
                </ol>
              </section>
            ) : null}
            <section aria-label="Request">
              <h4>Request body</h4>
              <pre className="webhook-delivery-code" data-i18n-ignore>
                {prettyJSON(delivery.payload)}
              </pre>
              <p className="webhook-delivery-muted">
                Each attempt adds webhookTimestamp to this body and signs it in
                the Flow-Signature header.
              </p>
            </section>
            <section aria-label="Response">
              <h4>Response</h4>
              <pre className="webhook-delivery-code" data-i18n-ignore>
                {delivery.responseBody || delivery.error || "—"}
              </pre>
            </section>
            {canRedeliver ? (
              <footer className="webhook-delivery-dialog-footer">
                <button
                  type="button"
                  className="webhook-edit-action"
                  disabled={busy}
                  onClick={() => void redeliver()}
                >
                  Redeliver
                </button>
              </footer>
            ) : null}
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}

/**
 * Delivery log of a webhook: each event sent, its status, response code,
 * duration and attempts, with a detail view to inspect and redeliver.
 */
export function WebhookDeliveries({
  webhookId,
  canRedeliver = true,
  refreshKey = 0,
}: {
  webhookId: string;
  canRedeliver?: boolean;
  /** Changing it reloads the log (after a test event). */
  refreshKey?: number;
}) {
  const [items, setItems] = useState<WebhookDelivery[]>([]);
  const [cursor, setCursor] = useState<string | undefined>();
  const [hasMore, setHasMore] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [filter, setFilter] = useState<"" | "failed">("");
  const [selected, setSelected] = useState<string | null>(null);
  const [reloads, setReloads] = useState(0);

  const load = useCallback(
    async (next?: string) => {
      setLoading(true);
      setError("");
      try {
        const page = await fetchWebhookDeliveries(webhookId, {
          cursor: next,
          status: filter || undefined,
        });
        setItems((current) => (next ? [...current, ...page.items] : page.items));
        setCursor(page.nextCursor);
        setHasMore(page.hasMore);
      } catch (reason) {
        setError(
          reason instanceof Error ? reason.message : "Could not load deliveries",
        );
      } finally {
        setLoading(false);
      }
    },
    [webhookId, filter],
  );

  useEffect(() => {
    void load();
  }, [load, refreshKey, reloads]);

  return (
    <section className="webhook-deliveries" aria-label="Recent deliveries">
      <header className="webhook-deliveries-header">
        <div>
          <h3>Recent deliveries</h3>
          <p>
            Failed deliveries are retried after 1 minute, 1 hour and 6 hours.
          </p>
        </div>
        <div className="webhook-deliveries-filter" role="group" aria-label="Filter deliveries">
          <button
            type="button"
            aria-pressed={filter === ""}
            onClick={() => setFilter("")}
          >
            All
          </button>
          <button
            type="button"
            aria-pressed={filter === "failed"}
            onClick={() => setFilter("failed")}
          >
            Failed
          </button>
        </div>
      </header>

      {error ? (
        <div className="webhook-delivery-state webhook-delivery-error" role="alert">
          <p>{error}</p>
          <button type="button" onClick={() => setReloads((value) => value + 1)}>
            Try again
          </button>
        </div>
      ) : null}

      {!error && !loading && items.length === 0 ? (
        <div className="webhook-delivery-state">
          <h4>No deliveries yet</h4>
          <p>Events sent to this webhook will appear here.</p>
        </div>
      ) : null}

      {items.length > 0 ? (
        <div className="webhook-delivery-table-wrap">
          <table className="webhook-delivery-table">
            <thead>
              <tr>
                <th>Time</th>
                <th>Event</th>
                <th>Status</th>
                <th>Response</th>
                <th>Duration</th>
                <th>
                  <span className="sr-only">Details</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {items.map((item) => (
                <tr key={item.id}>
                  <td>
                    <time dateTime={item.createdAt}>
                      {formatTimestamp(item.createdAt)}
                    </time>
                  </td>
                  <td data-i18n-ignore>{eventLabel(item)}</td>
                  <td>
                    <StatusBadge status={item.status} />
                    {item.attempt > 1 ? (
                      <span className="webhook-delivery-muted">
                        {" "}
                        ×{item.attempt}
                      </span>
                    ) : null}
                  </td>
                  <td>{item.responseStatus || "—"}</td>
                  <td>{item.durationMs ? `${item.durationMs} ms` : "—"}</td>
                  <td>
                    <button
                      type="button"
                      className="webhook-delivery-view"
                      aria-label={`View delivery ${eventLabel(item)}`}
                      onClick={() => setSelected(item.id)}
                    >
                      View
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : null}

      {loading ? (
        <div className="webhook-delivery-muted" role="status">
          Loading deliveries…
        </div>
      ) : null}
      {hasMore && !loading ? (
        <button
          type="button"
          className="webhook-delivery-more"
          onClick={() => void load(cursor)}
        >
          Load more
        </button>
      ) : null}

      {selected ? (
        <DeliveryDetailDialog
          webhookId={webhookId}
          deliveryId={selected}
          canRedeliver={canRedeliver}
          onClose={() => setSelected(null)}
          onRedelivered={(delivery) => {
            setSelected(delivery.id);
            setReloads((value) => value + 1);
          }}
        />
      ) : null}
    </section>
  );
}
