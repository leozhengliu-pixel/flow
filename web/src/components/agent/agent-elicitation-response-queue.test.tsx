import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import {
  AgentElicitationResponseQueue,
  summarizeElicitationQueue,
} from "./agent-elicitation-response-queue";

describe("AgentElicitationResponseQueue", () => {
  it("hides for a single pending elicitation", () => {
    const { container } = render(
      <AgentElicitationResponseQueue answeredCount={0} elicitationCount={1} />,
    );
    expect(container).toBeEmptyDOMElement();
  });

  it("shows N of M answered for multi-elicitation queues", () => {
    render(
      <AgentElicitationResponseQueue answeredCount={1} elicitationCount={3} />,
    );
    expect(screen.getByTestId("agent-elicitation-response-queue")).toHaveTextContent(
      "1 of 3 answered",
    );
  });

  it("shows submitting state", () => {
    render(
      <AgentElicitationResponseQueue
        answeredCount={2}
        elicitationCount={2}
        isSubmitting
      />,
    );
    expect(screen.getByText("Submitting answers…")).toBeVisible();
  });

  it("summarizes elicitation parts", () => {
    expect(
      summarizeElicitationQueue([
        { type: "elicitation", status: "pending" },
        { type: "elicitation", elicitation: { action: "accept" } },
        { type: "text" },
      ]),
    ).toEqual({ answeredCount: 1, elicitationCount: 2 });
  });
});
