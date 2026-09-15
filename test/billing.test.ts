import { describe, it, expect } from "vitest";
import { contributorSharePlan, usageManifestHash, usagePeriod } from "../src/billing";

describe("settlement share plan", () => {
  it("splits 10000 bps equally among wallet holders", () => {
    const plan = contributorSharePlan(
      { contributorId: "owner", stellarPublicKey: "GOWNER" },
      [
        { contributorId: "a", stellarPublicKey: "GA" },
        { contributorId: "b", stellarPublicKey: "GB" },
      ],
    );
    expect(plan.reduce((sum, share) => sum + share.shareBps, 0)).toBe(10000);
    expect(plan).toHaveLength(3);
  });
  it("gives the owner 10000 bps when nobody has a wallet", () => {
    const plan = contributorSharePlan(
      { contributorId: "owner", stellarPublicKey: null },
      [{ contributorId: "a", stellarPublicKey: null }],
    );
    expect(plan).toEqual([
      { contributorId: "owner", recipient: null, shareBps: 10000 },
    ]);
  });
});

describe("usage receipts", () => {
  it("hashes ids and units, never query text", () => {
    const a = usageManifestHash({
      licenseId: "lic",
      period: 20260915,
      events: [
        { id: "e2", units: 1 },
        { id: "e1", units: 2 },
      ],
    });
    const b = usageManifestHash({
      licenseId: "lic",
      period: 20260915,
      events: [
        { id: "e1", units: 2 },
        { id: "e2", units: 1 },
      ],
    });
    expect(a).toBe(b);
    expect(a).toHaveLength(64);
  });
  it("encodes UTC date as YYYYMMDD integer", () => {
    expect(usagePeriod(new Date("2026-09-15T23:00:00Z"))).toBe(20260915);
  });
});
