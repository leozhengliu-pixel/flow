import { describe, expect, it } from "vitest";

import { activityActor, attributedActionText, attributedActorAvatar, attributedActorLabel, attributedActorName, isAgentBot, liveBotActor, sameActivityActor, setLiveApplicationIdentities, viaAppName } from "./actor-attribution";

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

describe("Flow agent attribution", () => {
  const person = { id: "u1", displayName: "Dev User", name: "dev" };
  const agent = { id: "builtin-flow-agent", type: "agent", name: "Flow", avatarUrl: "/favicon.svg", onBehalfOfId: "u1", onBehalfOfName: "Dev (old name)" };

  it("names the agent as the actor and the person it acted for", () => {
    expect(isAgentBot(agent)).toBe(true);
    expect(activityActor(person, agent, [person])).toEqual({ name: "Flow", avatarUrl: "/favicon.svg", onBehalfOf: "Dev User" });
    // Without the directory, the recorded name; for the agent's own app user, never the app as the person.
    expect(activityActor(person, agent).onBehalfOf).toBe("Dev (old name)");
    expect(activityActor({ displayName: "Flow", app: true }, { ...agent, onBehalfOfName: undefined, onBehalfOfId: undefined }).onBehalfOf).toBeUndefined();
    expect(activityActor(person, { type: "oauthApplication", name: "Zendesk" })).toEqual({ name: "Dev User", avatarUrl: undefined, via: "Zendesk" });
  });

  it("keeps comments and records as '<User> via Flow'", () => {
    expect(attributedActorLabel(person, agent)).toBe("Dev User via Flow");
  });

  it("phrases the action for rows that render the actor separately", () => {
    const actor = activityActor(person, agent, [person]);
    expect(attributedActionText(actor, "created the initiative")).toBe("created the initiative on behalf of Dev User");
    const zh = (text: string) => (text === "{actor} {action} on behalf of {user}" ? "{actor} 代表 {user} {action}" : text);
    expect(attributedActionText(actor, "创建了目标", zh)).toBe("代表 Dev User 创建了目标");
    expect(attributedActionText({ name: "Dev User" }, "created the initiative")).toBe("created the initiative");
  });

  it("treats the same person through different attribution as different actors", () => {
    expect(sameActivityActor({ actor: person, botActor: agent }, { actor: person, botActor: agent })).toBe(true);
    expect(sameActivityActor({ actor: person, botActor: agent }, { actor: person })).toBe(false);
  });

  it("shows an application's current name and logo on records written before it changed", () => {
    const bot = { id: "client_1", type: "oauthApplication", name: "Old app", avatarUrl: "/uploads/old.png" };
    const external = { ...bot, userDisplayName: "Customer" };
    try {
      setLiveApplicationIdentities([{ app: true, oauthClientId: "client_1", name: "New app", displayName: "New app", avatarUrl: "/uploads/new.png" }]);
      expect(viaAppName(user, bot)).toBe("New app");
      expect(attributedActorLabel(user, bot)).toBe("Mark via New app");
      expect(attributedActorAvatar(user, external)).toBe("/uploads/new.png");
      expect(activityActor(user, bot).via).toBe("New app");
      expect(liveBotActor({ ...bot, id: "other" }).name).toBe("Old app");
      setLiveApplicationIdentities([]);
      expect(viaAppName(user, bot)).toBe("Old app");
    } finally {
      setLiveApplicationIdentities([]);
    }
  });
});
