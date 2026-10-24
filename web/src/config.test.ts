import { describe, expect, it } from "vitest";
import { resolveDeliveryApiBase } from "./config";

describe("resolveDeliveryApiBase", () => {
  it("is this context's own /api/last-mile-delivery under the runtime origin", () => {
    expect(resolveDeliveryApiBase({ apiOrigin: "http://localhost:8000" }, true)).toBe(
      "http://localhost:8000/api/last-mile-delivery",
    );
  });

  it("drops trailing slashes of the origin", () => {
    expect(resolveDeliveryApiBase({ apiOrigin: "http://kong.local///" }, false)).toBe(
      "http://kong.local/api/last-mile-delivery",
    );
  });

  it("falls back to the developer port outside production", () => {
    expect(resolveDeliveryApiBase({}, false)).toBe("http://localhost:8080");
  });

  it("refuses to guess in production", () => {
    expect(() => resolveDeliveryApiBase({}, true)).toThrow(/apiOrigin is required/);
    expect(() => resolveDeliveryApiBase({ apiOrigin: "" }, true)).toThrow(/apiOrigin is required/);
  });
});
