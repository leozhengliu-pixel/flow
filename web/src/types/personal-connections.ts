export type PersonalConnectionProvider = "google-calendar" | "notion";

export interface PersonalConnection {
  id: string;
  provider: PersonalConnectionProvider;
  /** The provider's name for the member (the Google account's email). */
  account?: string;
  scopes: string[];
  status: "active" | "reconnect";
  statusReason?: string;
  /** Google Calendar: the member's current or next out-of-office window. */
  awayFrom?: string;
  awayUntil?: string;
  linkedAt: string;
  updatedAt: string;
}

export interface PersonalConnectionOption {
  /** The deployment is configured for the connection. */
  available: boolean;
  connection?: PersonalConnection;
}

export interface PersonalConnectionsResponse {
  googleCalendar: PersonalConnectionOption;
  notion: PersonalConnectionOption;
}
