import { describe, expect, test } from "bun:test";
import { cardBadge } from "../client/badge";

describe("cardBadge", () => {
  test("shows Live only while frames flow", () => {
    expect(cardBadge(true, true, null)).toEqual({ label: "Live", live: true });
  });

  test("shows the hub status instead of Live", () => {
    expect(cardBadge(true, true, "suspended")).toEqual({ label: "suspended", live: false });
  });

  test("shows Paused for the newest card when paused", () => {
    expect(cardBadge(false, true, null)).toEqual({ label: "Paused", live: false });
  });

  test("shows Earlier for older cards", () => {
    expect(cardBadge(false, false, "suspended")).toEqual({ label: "Earlier", live: false });
  });
});
