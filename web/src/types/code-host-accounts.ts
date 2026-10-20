export type CodeHostKind = "github" | "gitlab";

export interface CodeHostAccount {
  id: string;
  codeHost: CodeHostKind;
  hostName: string;
  baseUrl: string;
  login: string;
  externalUserId: string;
  authType: "githubApp" | "oauth" | "pat";
  scopes: string[];
  status: "active" | "reconnect";
  statusReason?: string;
  instanceId?: string;
  canAct: boolean;
  linkedAt: string;
  updatedAt: string;
  tokenExpiresAt?: string;
}

export interface GitLabInstanceOption {
  id: string;
  name: string;
  baseUrl: string;
  hostName: string;
  oauthAvailable: boolean;
}

export interface CodeHostAccountsResponse {
  accounts: CodeHostAccount[];
  github: { available: boolean; method: "githubApp" | "oauth" | ""; hostName: string };
  gitlabInstances: GitLabInstanceOption[];
}

export interface GitLabOAuthApplication {
  configured: boolean;
  applicationId: string;
  redirectUrl: string;
  scopes: string[];
}
