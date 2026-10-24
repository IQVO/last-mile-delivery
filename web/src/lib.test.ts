import { describe, expect, it } from "vitest";
import { idProblem, serviceDateProblem, stopMeter, utcLabel } from "./lib";

describe("utcLabel", () => {
  it("formats an instant to the second in UTC", () => {
    expect(utcLabel("2026-10-24T12:00:30+00:00")).toBe("2026-10-24 12:00:30Z");
    expect(utcLabel("2026-10-24T09:00:30-03:00")).toBe("2026-10-24 12:00:30Z");
  });
  it("shows a dash for nothing and the raw text for garbage", () => {
    expect(utcLabel(undefined)).toBe("—");
    expect(utcLabel("soon")).toBe("soon");
  });
});

describe("idProblem", () => {
  it("accepts a plain id", () => {
    expect(idProblem("station", "node-ds-01")).toBeNull();
    expect(idProblem("station", "a".repeat(64))).toBeNull();
  });
  it("names what is wrong", () => {
    expect(idProblem("station", "")).toMatch(/Enter a station id/);
    expect(idProblem("station", "a".repeat(65))).toMatch(/at most 64/);
    expect(idProblem("route", "a b")).toMatch(/cannot contain/);
    expect(idProblem("route", "a/b")).toMatch(/cannot contain/);
  });
});

describe("serviceDateProblem", () => {
  it("accepts a real date", () => {
    expect(serviceDateProblem("2026-10-24")).toBeNull();
  });
  it("rejects the wrong shape and unreal dates", () => {
    expect(serviceDateProblem("")).toMatch(/Enter a service date/);
    expect(serviceDateProblem("24-10-2026")).toMatch(/YYYY-MM-DD/);
    expect(serviceDateProblem("2026-02-30")).toMatch(/not a real calendar date/);
  });
});

describe("stopMeter", () => {
  it("counts assigned against the total capacity in stops", () => {
    expect(stopMeter({ assignedStops: 2, remainingStops: 3 })).toBe("2 of 5 stops assigned");
    expect(stopMeter({ assignedStops: 0, remainingStops: 0 })).toBe("0 of 0 stops assigned");
  });
});
