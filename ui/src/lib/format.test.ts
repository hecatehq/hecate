import { describe, expect, it } from "vitest";

import {
  formatAbsoluteTime,
  formatDurationMs,
  formatDurationRange,
  formatInteger,
  formatLocaleDateTime,
  formatLocaleTime,
  formatMicrosUSD,
} from "./format";

describe("formatDurationMs", () => {
  it("renders sub-second values as Nms with integer rounding", () => {
    expect(formatDurationMs(0.4)).toBe("0ms");
    expect(formatDurationMs(7)).toBe("7ms");
    expect(formatDurationMs(7.6)).toBe("8ms");
    expect(formatDurationMs(999)).toBe("999ms");
  });

  it("renders sub-10s values to one decimal", () => {
    expect(formatDurationMs(1000)).toBe("1.0s");
    expect(formatDurationMs(1234)).toBe("1.2s");
    expect(formatDurationMs(9_999)).toBe("10.0s");
  });

  it("renders 10s..60s as integer seconds", () => {
    expect(formatDurationMs(10_000)).toBe("10s");
    expect(formatDurationMs(45_400)).toBe("45s");
    expect(formatDurationMs(59_999)).toBe("60s");
  });

  it("renders minute+second pairs above 60s", () => {
    expect(formatDurationMs(60_000)).toBe("1m 0s");
    expect(formatDurationMs(125_500)).toBe("2m 6s");
    expect(formatDurationMs(3_600_000)).toBe("60m 0s");
  });

  it("guards non-finite and non-positive input as 0ms", () => {
    expect(formatDurationMs(0)).toBe("0ms");
    expect(formatDurationMs(-100)).toBe("0ms");
    expect(formatDurationMs(Number.NaN)).toBe("0ms");
    expect(formatDurationMs(Number.POSITIVE_INFINITY)).toBe("0ms");
  });
});

describe("formatDurationRange", () => {
  it("returns empty string when start is missing or unparseable", () => {
    expect(formatDurationRange(undefined)).toBe("");
    expect(formatDurationRange("")).toBe("");
    expect(formatDurationRange("not-a-date")).toBe("");
  });

  it("formats explicit start/end ranges via formatDurationMs", () => {
    expect(
      formatDurationRange("2026-01-01T00:00:00.000Z", "2026-01-01T00:00:00.500Z"),
    ).toBe("500ms");
    expect(
      formatDurationRange("2026-01-01T00:00:00.000Z", "2026-01-01T00:00:05.500Z"),
    ).toBe("5.5s");
    expect(
      formatDurationRange("2026-01-01T00:00:00.000Z", "2026-01-01T00:02:06.000Z"),
    ).toBe("2m 6s");
  });

  it("clamps a negative range to 0ms", () => {
    expect(
      formatDurationRange("2026-01-01T00:00:10.000Z", "2026-01-01T00:00:00.000Z"),
    ).toBe("0ms");
  });

  it("rejects an unparseable end", () => {
    expect(formatDurationRange("2026-01-01T00:00:00.000Z", "not-a-date")).toBe("");
  });

  it("falls back to Date.now() when end is omitted", () => {
    const fiveSecondsAgo = new Date(Date.now() - 5_000).toISOString();
    expect(formatDurationRange(fiveSecondsAgo)).toMatch(/^\d+(\.\d)?s$/);
  });
});

describe("formatMicrosUSD", () => {
  it("renders µUSD to three decimal places", () => {
    expect(formatMicrosUSD(1_500_000)).toBe("$1.500");
    expect(formatMicrosUSD(1_234)).toBe("$0.001");
    expect(formatMicrosUSD(900_000)).toBe("$0.900");
  });

  it("guards non-finite and non-positive input as $0.000", () => {
    expect(formatMicrosUSD(0)).toBe("$0.000");
    expect(formatMicrosUSD(-1)).toBe("$0.000");
    expect(formatMicrosUSD(Number.NaN)).toBe("$0.000");
    expect(formatMicrosUSD(Number.POSITIVE_INFINITY)).toBe("$0.000");
  });
});

describe("formatAbsoluteTime", () => {
  it("returns empty string for missing input", () => {
    expect(formatAbsoluteTime(undefined)).toBe("");
    expect(formatAbsoluteTime("")).toBe("");
  });

  it("returns the raw value if it can't be parsed", () => {
    expect(formatAbsoluteTime("not-a-date")).toBe("not-a-date");
  });

  it("formats an ISO timestamp using Intl.DateTimeFormat", () => {
    const out = formatAbsoluteTime("2026-05-15T17:31:41Z");
    // Locale + timezone vary by runner; assert a stable substring
    // shape rather than the full string. The constituent parts
    // (year + a short month name + numeric hour:minute:second + a
    // timezone abbreviation) must all be present.
    expect(out).toMatch(/2026/);
    expect(out).toMatch(/May/);
    expect(out).toMatch(/\d{1,2}:\d{2}:\d{2}/);
    expect(out).toMatch(/[A-Z]{2,5}|GMT|UTC/);
  });
});

describe("formatLocaleDateTime", () => {
  it("returns empty string for missing/unparseable input", () => {
    expect(formatLocaleDateTime(undefined)).toBe("");
    expect(formatLocaleDateTime("")).toBe("");
    expect(formatLocaleDateTime("not-a-date")).toBe("");
  });

  it("matches Date.toLocaleString() for the same instant", () => {
    const iso = "2026-05-15T17:31:41Z";
    expect(formatLocaleDateTime(iso)).toBe(new Date(iso).toLocaleString());
  });
});

describe("formatLocaleTime", () => {
  it("returns em dash for missing/unparseable input", () => {
    expect(formatLocaleTime(undefined)).toBe("—");
    expect(formatLocaleTime("")).toBe("—");
    expect(formatLocaleTime("not-a-date")).toBe("—");
  });

  it("matches Date.toLocaleTimeString() for the same instant", () => {
    const iso = "2026-05-15T17:31:41Z";
    expect(formatLocaleTime(iso)).toBe(new Date(iso).toLocaleTimeString());
  });
});

describe("formatInteger", () => {
  it("renders integers with the runtime's locale thousand separator", () => {
    expect(formatInteger(0)).toBe((0).toLocaleString());
    expect(formatInteger(1_234)).toBe((1234).toLocaleString());
    expect(formatInteger(1_234_567)).toBe((1234567).toLocaleString());
  });

  it("rounds non-integer input before formatting", () => {
    expect(formatInteger(1234.7)).toBe((1235).toLocaleString());
  });

  it("returns em dash for non-finite input", () => {
    expect(formatInteger(Number.NaN)).toBe("—");
    expect(formatInteger(Number.POSITIVE_INFINITY)).toBe("—");
  });
});
