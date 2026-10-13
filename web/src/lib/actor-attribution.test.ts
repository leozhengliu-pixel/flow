import { describe, expect, it } from "vitest";

import { attributedActorAvatar, attributedActorLabel, attributedActorName, viaAppName } from "./actor-attribution";

const user = { displayName: "Mark", name: "mark", avatarUrl: "https://img.test/mark.png" };

describe("actor attribution", () => {
  it("leaves records without a bot actor unchanged", () => {
    expect(attributedActorName(user, undefined)).toBe("Mark");
    expect(viaAppName(user, undefined)).toBeUndefined();
    expect(attributedActorAvatar(user, undefined)).toBe("https://img.test/mark.png");
    expect(attributedActorLabel(user)).toBe("Mark");
  });

  it("shows '<user> via <app>' with the user's avatar", () => {
    const bot = { type: "oauthApplication", name: "Zendesk", avatarUrl: "https://img.test/zd.png" };
    expect(attributedActorName(user, bot)).toBe("Mark");
    expect(viaAppName(user, bot)).toBe("Zendesk");
    expect(attributedActorAvatar(user, bot)).toBe("https://img.test/mark.png");
    expect(attributedActorLabel(user, bot)).toBe("Mark via Zendesk");
  });

  it("names the external person an app acted for", () => {
    const bot = { type: "oauthApplication", name: "Zendesk", avatarUrl: "https://img.test/zd.png", userDisplayName: "Customer Jo" };
    expect(attributedActorName(user, bot)).toBe("Customer Jo");
    expect(attributedActorLabel(user, bot, "通过")).toBe("Customer Jo 通过 Zendesk");
    expect(attributedActorAvatar(user, bot)).toBe("https://img.test/zd.png");
    expect(attributedActorAvatar(user, { ...bot, userAvatarUrl: "https://img.test/jo.png" })).toBe("https://img.test/jo.png");
    expect(attributedActorAvatar(user, { type: "oauthApplication", name: "Zendesk", userDisplayName: "Jo" })).toBe("https://img.test/mark.png");
  });

  it("does not add 'via' for the application's own user or a nameless bot", () => {
    expect(viaAppName({ ...user, app: true }, { type: "oauthApplication", name: "Zendesk" })).toBeUndefined();
    expect(viaAppName(user, { type: "oauthApplication" })).toBeUndefined();
  });
});
