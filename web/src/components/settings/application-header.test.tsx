import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { ApplicationHeader, OAuthAppImage } from "./application-header";

describe("ApplicationHeader", () => {
  it("renders OAuth app image initial and title", () => {
    render(
      <ApplicationHeader
        app={{
          id: "oauth_1",
          name: "Linear Mirror",
          clientId: "flow_client_1",
          redirectUris: [],
          scopes: ["read"],
          creatorId: "user_1",
          createdAt: "2026-01-01T00:00:00Z",
          updatedAt: "2026-01-01T00:00:00Z",
        }}
      />,
    );
    expect(screen.getByText("Linear Mirror")).toBeVisible();
    expect(screen.getByText("L")).toBeVisible();
  });

  it("renders logo image when logoUrl is present", () => {
    const { container } = render(
      <OAuthAppImage
        app={{ name: "Acme", logoUrl: "https://example.com/logo.png" }}
      />,
    );
    expect(container.querySelector("img")?.getAttribute("src")).toBe(
      "https://example.com/logo.png",
    );
  });
});
