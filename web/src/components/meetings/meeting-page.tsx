import { useCallback, useEffect, useMemo, useState } from "react";
import {
  CalendarDays,
  ChevronRight,
  LoaderCircle,
  Plus,
  Users,
} from "lucide-react";
import {
  createMeeting,
  fetchMeetings,
  updateMeeting,
} from "@/lib/api";
import { meetingPath, meetingsPath } from "@/lib/app-routes";
import type { BootstrapData, Meeting, User } from "@/types/flow";
import { UserAvatar } from "@/components/ui/user-avatar";
import { useI18n } from "@/i18n/i18n";
import "./meeting-page.css";

type Props = {
  data: BootstrapData;
  meetingId?: string;
  onNavigate: (path: string) => void;
};

/** LS-0404 — real meetings list + detail (title / date / attendees / notes·transcript). */
export function MeetingPage({ data, meetingId, onNavigate }: Props) {
  const { t, formatDate } = useI18n();
  const [meetings, setMeetings] = useState<Meeting[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [creating, setCreating] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const page = await fetchMeetings();
      setMeetings(page.items);
    } catch (cause) {
      setError(
        cause instanceof Error ? cause.message : t("Could not load meetings"),
      );
    } finally {
      setLoading(false);
    }
  }, [t]);

  useEffect(() => {
    void load();
  }, [load]);

  const selected = meetingId
    ? meetings.find((item) => item.id === meetingId)
    : undefined;

  const create = async () => {
    setCreating(true);
    setError("");
    try {
      const meeting = await createMeeting({
        title: "Untitled meeting",
        startsAt: new Date().toISOString(),
        durationMinutes: 30,
        attendeeIds: [data.viewer.id],
      });
      setMeetings((current) => [meeting, ...current]);
      onNavigate(meetingPath(data.workspace.urlKey, meeting.id));
    } catch (cause) {
      setError(
        cause instanceof Error ? cause.message : t("Could not create meeting"),
      );
    } finally {
      setCreating(false);
    }
  };

  if (meetingId) {
    if (loading && !selected) {
      return (
        <section className="secondary-content meeting-content">
          <div className="secondary-loading">
            <LoaderCircle className="spin" size={16} />
            {t("Loading…")}
          </div>
        </section>
      );
    }
    if (!selected) {
      return (
        <section className="secondary-content meeting-content">
          <EmptyState
            title={t("Meeting not found")}
            body={t("This meeting is no longer available.")}
          />
          <button
            type="button"
            className="secondary-secondary-button"
            onClick={() => onNavigate(meetingsPath(data.workspace.urlKey))}
          >
            {t("Back to meetings")}
          </button>
        </section>
      );
    }
    return (
      <MeetingDetail
        data={data}
        meeting={selected}
        onNavigate={onNavigate}
        onSaved={(next) =>
          setMeetings((current) =>
            current.map((item) => (item.id === next.id ? next : item)),
          )
        }
      />
    );
  }

  return (
    <section className="secondary-content meeting-content">
      <div className="secondary-section-heading">
        <div>
          <h2>{t("Meetings")}</h2>
          <p>{t("Notes, attendees, and transcripts")}</p>
        </div>
        <button
          type="button"
          className="secondary-primary-button"
          disabled={creating}
          onClick={() => void create()}
        >
          {creating ? (
            <LoaderCircle className="spin" size={14} />
          ) : (
            <Plus size={14} />
          )}
          {t("New meeting")}
        </button>
      </div>
      {error ? (
        <div className="secondary-error" role="alert">
          {error}
        </div>
      ) : null}
      {loading ? (
        <div className="secondary-loading">
          <LoaderCircle className="spin" size={16} />
          {t("Loading…")}
        </div>
      ) : (
        <div className="secondary-list" role="list">
          {meetings.map((meeting) => (
            <a
              className="secondary-list-row"
              href={meetingPath(data.workspace.urlKey, meeting.id)}
              key={meeting.id}
              onClick={(event) => {
                event.preventDefault();
                onNavigate(meetingPath(data.workspace.urlKey, meeting.id));
              }}
              role="listitem"
            >
              <div className="secondary-row-icon">
                <CalendarDays size={16} />
              </div>
              <div className="secondary-row-main">
                <strong>{meeting.title || t("Untitled meeting")}</strong>
                <small>
                  {formatDate(meeting.startsAt, {
                    dateStyle: "medium",
                    timeStyle: "short",
                  })}
                  {meeting.attendeeIds.length
                    ? ` · ${meeting.attendeeIds.length} ${t("attendees")}`
                    : ""}
                </small>
              </div>
              <ChevronRight size={15} />
            </a>
          ))}
        </div>
      )}
      {!loading && !meetings.length ? (
        <EmptyState
          title={t("No meetings yet")}
          body={t("Create a meeting to start capturing notes.")}
        />
      ) : null}
    </section>
  );
}

function MeetingDetail({
  data,
  meeting,
  onNavigate,
  onSaved,
}: {
  data: BootstrapData;
  meeting: Meeting;
  onNavigate: (path: string) => void;
  onSaved: (meeting: Meeting) => void;
}) {
  const { t, formatDate } = useI18n();
  const [title, setTitle] = useState(meeting.title);
  const [startsAt, setStartsAt] = useState(toLocalInput(meeting.startsAt));
  const [notes, setNotes] = useState(meeting.notes || "");
  const [transcript, setTranscript] = useState(meeting.transcript || "");
  const [attendeeIds, setAttendeeIds] = useState(meeting.attendeeIds);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    setTitle(meeting.title);
    setStartsAt(toLocalInput(meeting.startsAt));
    setNotes(meeting.notes || "");
    setTranscript(meeting.transcript || "");
    setAttendeeIds(meeting.attendeeIds);
  }, [meeting]);

  const attendees = useMemo(
    () =>
      attendeeIds
        .map((id) => data.users.find((user) => user.id === id))
        .filter(Boolean) as User[],
    [attendeeIds, data.users],
  );

  const persist = async (patch: Partial<Meeting>) => {
    setSaving(true);
    setError("");
    try {
      const next = await updateMeeting(meeting.id, patch);
      onSaved(next);
    } catch (cause) {
      setError(
        cause instanceof Error ? cause.message : t("Could not save meeting"),
      );
    } finally {
      setSaving(false);
    }
  };

  const toggleAttendee = (userId: string) => {
    const next = attendeeIds.includes(userId)
      ? attendeeIds.filter((id) => id !== userId)
      : [...attendeeIds, userId];
    setAttendeeIds(next);
    void persist({ attendeeIds: next });
  };

  return (
    <section className="secondary-content meeting-detail">
      <div className="meeting-detail-crumb">
        <button
          type="button"
          className="secondary-secondary-button"
          onClick={() => onNavigate(meetingsPath(data.workspace.urlKey))}
        >
          {t("Meetings")}
        </button>
        <ChevronRight size={14} aria-hidden />
        <span data-i18n-ignore>{title || t("Untitled meeting")}</span>
        {saving ? (
          <span className="meeting-detail-saving">
            <LoaderCircle className="spin" size={12} />
            {t("Saving…")}
          </span>
        ) : null}
      </div>
      <input
        className="meeting-detail-title"
        aria-label={t("Meeting title")}
        value={title}
        onChange={(event) => setTitle(event.target.value)}
        onBlur={() => {
          if (title.trim() && title !== meeting.title)
            void persist({ title: title.trim() });
        }}
      />
      <div className="meeting-detail-meta">
        <label className="meeting-meta-field">
          <span>{t("Date")}</span>
          <input
            type="datetime-local"
            aria-label={t("Change meeting date")}
            value={startsAt}
            onChange={(event) => setStartsAt(event.target.value)}
            onBlur={() => {
              const iso = fromLocalInput(startsAt);
              if (iso && iso !== meeting.startsAt) void persist({ startsAt: iso });
            }}
          />
        </label>
        <div className="meeting-meta-field">
          <span>{t("Attendees")}</span>
          <div className="meeting-attendees">
            {attendees.map((user) => (
              <button
                type="button"
                className="meeting-attendee-chip"
                key={user.id}
                onClick={() => toggleAttendee(user.id)}
                title={t("Remove attendee")}
              >
                <UserAvatar
                  name={user.displayName || user.name}
                  avatarUrl={user.avatarUrl}
                />
                <span data-i18n-ignore>{user.displayName || user.name}</span>
              </button>
            ))}
            <select
              aria-label={t("Add attendees")}
              className="meeting-attendee-add"
              value=""
              onChange={(event) => {
                if (event.target.value) toggleAttendee(event.target.value);
              }}
            >
              <option value="">{t("Add attendees")}</option>
              {data.users
                .filter((user) => !attendeeIds.includes(user.id))
                .map((user) => (
                  <option key={user.id} value={user.id}>
                    {user.displayName || user.name}
                  </option>
                ))}
            </select>
          </div>
          {!attendees.length ? (
            <small className="meeting-attendees-empty">
              <Users size={12} />
              {t("Identifying attendees…")}
            </small>
          ) : null}
        </div>
      </div>
      <label className="meeting-editor-field">
        <span>{t("Notes")}</span>
        <textarea
          aria-label={t("Meeting notes")}
          placeholder={t("Add meeting notes…")}
          value={notes}
          onChange={(event) => setNotes(event.target.value)}
          onBlur={() => {
            if (notes !== (meeting.notes || "")) void persist({ notes });
          }}
        />
      </label>
      <label className="meeting-editor-field">
        <span>{t("Transcript")}</span>
        <textarea
          aria-label={t("Meeting transcript")}
          className="transcript-content"
          placeholder={t("Paste a transcript…")}
          value={transcript}
          onChange={(event) => setTranscript(event.target.value)}
          onBlur={() => {
            if (transcript !== (meeting.transcript || ""))
              void persist({ transcript });
          }}
        />
      </label>
      <p className="meeting-detail-footnote">
        {t("Updated")}{" "}
        {formatDate(meeting.updatedAt, {
          dateStyle: "medium",
          timeStyle: "short",
        })}
      </p>
      {error ? (
        <div className="secondary-error" role="alert">
          {error}
        </div>
      ) : null}
    </section>
  );
}

function EmptyState({ title, body }: { title: string; body: string }) {
  return (
    <div className="secondary-empty">
      <div className="secondary-empty-icon">
        <CalendarDays size={20} />
      </div>
      <h3>{title}</h3>
      <p>{body}</p>
    </div>
  );
}

function toLocalInput(value: string) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

function fromLocalInput(value: string) {
  if (!value) return "";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "" : date.toISOString();
}

export default MeetingPage;
