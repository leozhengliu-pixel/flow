import { describe, expect, it, vi } from "vitest";

import {
  emptyApplicationForm,
  formStateFromApplication,
  formStateFromManifest,
  githubUsernameError,
  manifestFileName,
  manifestFromFormState,
  manifestFromSearch,
  parseManifestText,
} from "./oauth-app-manifest";

describe("manifestFromSearch", () => {
  it("reads dot-notation params with repeated values", () => {
    const manifest = manifestFromSearch(
      "?distribution=public&display.description=Sync&display.iconUrl=https%3A%2F%2Fx.test%2Fi.png&developer.name=Acme" +
        "&oauth.client_name=Acme%20Sync&oauth.client_uri=https%3A%2F%2Facme.test" +
        "&oauth.redirect_uris=https%3A%2F%2Fa.test%2Fcb&oauth.redirect_uris=https%3A%2F%2Fb.test%2Fcb" +
        "&oauth.grant_types=authorization_code&oauth.grant_types=client_credentials&oauth.scopes=read&oauth.scopes=write" +
        "&webhook.enabled=true&webhook.url=https%3A%2F%2Fhooks.test&webhook.resourceTypes=Issue&webhook.resourceTypes=Comment" +
        "&agent.assignable=true&agent.mentionable=false",
    );
    expect(manifest).toEqual({
      distribution: "public",
      display: { description: "Sync", iconUrl: "https://x.test/i.png" },
      developer: { name: "Acme" },
      oauth: {
        client_name: "Acme Sync",
        client_uri: "https://acme.test",
        redirect_uris: ["https://a.test/cb", "https://b.test/cb"],
        grant_types: ["authorization_code", "client_credentials"],
        scopes: ["read", "write"],
      },
      webhook: { enabled: true, url: "https://hooks.test", resourceTypes: ["Issue", "Comment"] },
      agent: { assignable: true, mentionable: false },
    });
  });

  it("lets the manifest param win over dot params", () => {
    const manifest = manifestFromSearch(
      `?manifest=${encodeURIComponent(JSON.stringify({ oauth: { client_name: "From JSON" } }))}&oauth.client_name=From%20params&distribution=public`,
    );
    expect(manifest).toEqual({ oauth: { client_name: "From JSON" } });
  });

  it("ignores unsupported params, bad values and a broken manifest param", () => {
    expect(manifestFromSearch("?foo=bar&oauth.unknown=1&distribution=weird&webhook.enabled=maybe")).toEqual({});
    expect(manifestFromSearch("?manifest=%7Bnope&oauth.client_name=Fallback")).toEqual({
      oauth: { client_name: "Fallback" },
    });
  });

  it("drops unknown keys and wrongly typed values inside a manifest param", () => {
    const manifest = manifestFromSearch(
      `?manifest=${encodeURIComponent(JSON.stringify({ oauth: { client_name: 5, redirect_uris: ["https://a.test", 3] }, evil: true, display: { description: "ok" } }))}`,
    );
    expect(manifest).toEqual({ display: { description: "ok" }, oauth: { redirect_uris: ["https://a.test"] } });
  });
});

describe("form <-> manifest", () => {
  it("round-trips agent capabilities through the agent scopes", () => {
    const state = formStateFromManifest({
      oauth: { client_name: "Bot", scopes: ["write", "app:assignable"], grant_types: ["authorization_code", "client_credentials"] },
    });
    expect(state.scopes).toEqual(["read", "write"]);
    expect(state.assignable).toBe(true);
    expect(state.mentionable).toBe(false);
    expect(state.clientCredentials).toBe(true);
    const manifest = manifestFromFormState({ ...state, mentionable: true, redirectUris: "https://a.test/cb\n\nhttps://b.test/cb\n" });
    expect(manifest.oauth?.scopes).toEqual(["read", "write", "app:assignable", "app:mentionable"]);
    expect(manifest.oauth?.redirect_uris).toEqual(["https://a.test/cb", "https://b.test/cb"]);
    expect(manifest.oauth?.grant_types).toEqual(["authorization_code", "client_credentials"]);
    expect(manifest.agent).toEqual({ assignable: true, mentionable: true });
  });

  it("starts from a private application with read access", () => {
    expect(emptyApplicationForm()).toMatchObject({ distribution: "private", scopes: ["read"], webhookEnabled: false });
    expect(manifestFromFormState(emptyApplicationForm()).oauth?.grant_types).toEqual(["authorization_code"]);
  });

  it("builds the form from a saved application", () => {
    const state = formStateFromApplication({
      id: "app-1",
      name: "Saved",
      clientId: "c",
      creatorId: "u",
      createdAt: "",
      updatedAt: "",
      redirectUris: ["https://a.test/cb"],
      scopes: ["read", "admin", "app:mentionable"],
      publicEnabled: true,
      developerName: "Dev",
      developerUrl: "https://dev.test",
      webhookEnabled: true,
      webhookUrl: "https://hooks.test",
      webhookResourceTypes: ["Issue"],
    });
    expect(state).toMatchObject({
      name: "Saved",
      distribution: "public",
      scopes: ["read", "admin"],
      mentionable: true,
      assignable: false,
      developerName: "Dev",
      webhookResourceTypes: ["Issue"],
    });
  });
});

describe("GitHub username and webhook signing secret", () => {
  it("round-trips the GitHub username and leaves the block out when it is empty", () => {
    const state = formStateFromManifest({ github: { username: "acme-coder[bot]" } });
    expect(state.githubUsername).toBe("acme-coder[bot]");
    expect(manifestFromFormState(state).github).toEqual({ username: "acme-coder[bot]" });
    expect(manifestFromFormState({ ...state, githubUsername: "  " }).github).toBeUndefined();
    expect(manifestFromSearch(`?manifest=${encodeURIComponent(JSON.stringify({ github: { username: "octocat", extra: 1 } }))}`)).toEqual({ github: { username: "octocat" } });
  });

  it("generates a signing secret for new webhooks and never resends a saved one", () => {
    const fresh = emptyApplicationForm();
    expect(fresh.webhookSecret).toMatch(/^flow_wh_[A-Za-z0-9_-]{32}$/);
    expect(fresh.webhookSecretIsNew).toBe(true);
    expect(emptyApplicationForm().webhookSecret).not.toBe(fresh.webhookSecret);
    expect(manifestFromFormState({ ...fresh, webhookEnabled: true, webhookUrl: "https://hooks.test" }).webhook?.secret).toBe(fresh.webhookSecret);
    const saved = formStateFromApplication({ id: "a", name: "A", clientId: "c", creatorId: "u", createdAt: "", updatedAt: "", redirectUris: [], scopes: ["read"], webhookEnabled: true, webhookUrl: "https://hooks.test", webhookSecretPrefix: "flow_wh_abc" });
    expect(saved).toMatchObject({ webhookSecret: "", webhookSecretIsNew: false });
    expect(manifestFromFormState({ ...saved, webhookSecret: "flow_wh_loaded" }).webhook).toEqual({ enabled: true, url: "https://hooks.test", resourceTypes: [] });
  });

  it("checks the GitHub username: format first, then the account on GitHub", async () => {
    const found = vi.fn().mockResolvedValue(new Response("{}", { status: 200 }));
    const missing = vi.fn().mockResolvedValue(new Response("{}", { status: 404 }));
    const offline = vi.fn().mockRejectedValue(new TypeError("offline"));
    expect(await githubUsernameError("", found)).toBeUndefined();
    expect(await githubUsernameError("-bad", found)).toBe("Invalid GitHub username");
    expect(await githubUsernameError("two--hyphens", found)).toBe("Invalid GitHub username");
    expect(found).not.toHaveBeenCalled();
    expect(await githubUsernameError("my-app[bot]", found)).toBeUndefined();
    expect(found).toHaveBeenCalledWith("https://api.github.com/users/my-app%5Bbot%5D", expect.anything());
    expect(await githubUsernameError("nobody", missing)).toBe("Invalid GitHub username");
    expect(await githubUsernameError("octocat", offline)).toBeUndefined();
  });
});

describe("parseManifestText", () => {
  it("reports invalid JSON and non-objects", () => {
    expect(parseManifestText("{oops")).toEqual({ error: "The manifest is not valid JSON." });
    expect(parseManifestText("[1]")).toEqual({ error: "The manifest must be a JSON object." });
    expect(parseManifestText('{"distribution":"public"}')).toEqual({ manifest: { distribution: "public" } });
  });

  it("names exported files after the application", () => {
    expect(manifestFileName(" Acme Sync! ")).toBe("acme-sync.manifest.json");
    expect(manifestFileName("???")).toBe("oauth-app.manifest.json");
  });
});
