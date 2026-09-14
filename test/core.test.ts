import { describe, it, expect } from "vitest";
import {
  canonical,
  hash,
  passwordHash,
  verifyPassword,
  ensureLicense,
} from "../src/core";
describe("security and publication primitives", () => {
  it("canonicalizes nested manifest keys", () =>
    expect(hash(canonical({ b: 2, a: { z: 1, c: 3 } }))).toBe(
      hash(canonical({ a: { c: 3, z: 1 }, b: 2 })),
    ));
  it("hashes passwords with distinct salts and rejects wrong passwords", async () => {
    const a = await passwordHash("long-test-password");
    const b = await passwordHash("long-test-password");
    expect(a).not.toBe(b);
    expect(await verifyPassword("long-test-password", a)).toBe(true);
    expect(await verifyPassword("wrong-password", a)).toBe(false);
  });
  it.each(["revoked", "expired", "future", "quota", "purpose", "recipient"])(
    "rejects %s grants",
    (reason) => {
      const grant = {
        status: "ACTIVE",
        startsAt: new Date(Date.now() - 1000),
        expiresAt: new Date(Date.now() + 100000),
        used: 0,
        usageLimit: 10,
        purposes: ["learning"],
        grantee: "test@example.test",
      };
      if (reason === "revoked") grant.status = "REVOKED";
      if (reason === "expired") grant.expiresAt = new Date(0);
      if (reason === "future") grant.startsAt = new Date(Date.now() + 100000);
      if (reason === "quota") grant.used = 10;
      expect(() =>
        ensureLicense(
          grant,
          reason === "recipient" ? "other@example.test" : grant.grantee,
          reason === "purpose" ? "training" : "learning",
        ),
      ).toThrow();
    },
  );
});
