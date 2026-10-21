import { jsonRequest, request } from "@/lib/api-client";
import type { PersonalConnectionsResponse } from "@/types/personal-connections";

export function fetchPersonalConnections(): Promise<PersonalConnectionsResponse> {
  return request("/api/account/connections");
}

export function authorizeGoogleCalendar(): Promise<{ url: string }> {
  return request("/api/account/connections/google-calendar/authorize", jsonRequest("POST", {}));
}

export function disconnectGoogleCalendar(): Promise<void> {
  return request("/api/account/connections/google-calendar", { method: "DELETE" });
}

/** Turns on Notion previews for the member; resolves to Notion's connection page. */
export function connectNotion(): Promise<{ url: string }> {
  return request("/api/account/connections/notion/connect", jsonRequest("POST", {}));
}

export function disconnectNotion(): Promise<void> {
  return request("/api/account/connections/notion", { method: "DELETE" });
}
