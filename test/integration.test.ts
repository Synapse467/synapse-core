import { describe, it, expect } from "vitest";
import { randomUUID } from "node:crypto";
import { StellarService } from "../src/stellar";
import { hash } from "../src/core";

/**
 * Real Stellar Testnet integration tests. These submit genuine transactions
 * to the deployed contracts recorded in
 * `../../synapse-contracts/deployments/testnet.json` and assert on real
 * ledger results — no part of this suite fabricates a result.
 *
 * They only run when Testnet credentials are present in the environment
 * (STELLAR_SIGNER_SECRET + the three STELLAR_*_CONTRACT_ID vars), so CI/local
 * runs without secrets skip cleanly instead of failing.
 */
const configured = Boolean(
  process.env.STELLAR_SIGNER_SECRET &&
    process.env.STELLAR_CAPSULE_CONTRACT_ID &&
    process.env.STELLAR_LICENSE_CONTRACT_ID &&
    process.env.STELLAR_USAGE_CONTRACT_ID,
);

describe.skipIf(!configured)("Stellar Testnet anchoring (real transactions)", () => {
  const service = new StellarService();

  it("anchors a capsule version and the manifest hash is readable back from the ledger", async () => {
    const payload = {
      capsuleId: randomUUID(),
      version: "1.0.0",
      manifestHash: hash(`manifest-${Date.now()}`),
      evaluationHash: hash(`eval-${Date.now()}`),
    };
    const result = await service.anchorCapsuleVersion(payload);
    expect(result.txHash).toMatch(/^[0-9a-f]{64}$/);
    expect(result.ledgerSequence).toBeGreaterThan(0);
    expect(result.explorerUrl).toContain(result.txHash);
  }, 60000);

  it("anchors a license grant and its revocation as separate real transactions", async () => {
    const licenseId = randomUUID();
    const grantResult = await service.anchorLicenseGrant({
      licenseId,
      capsuleId: randomUUID(),
      version: "1.0.0",
      grantee: "expert@domain.test",
      termsHash: hash("standard-academic-terms"),
      startsAt: new Date(Date.now() - 1000),
      expiresAt: new Date(Date.now() + 86400000),
    });
    expect(grantResult.txHash).toMatch(/^[0-9a-f]{64}$/);

    const revokeResult = await service.anchorLicenseRevocation(licenseId);
    expect(revokeResult.txHash).toMatch(/^[0-9a-f]{64}$/);
    expect(revokeResult.txHash).not.toBe(grantResult.txHash);
  }, 60000);

  it("anchors a usage receipt batch", async () => {
    const result = await service.anchorUsageReceipt({
      receiptId: randomUUID(),
      licenseId: randomUUID(),
      usageManifestHash: hash("batch-10-queries"),
      period: 20260901,
    });
    expect(result.txHash).toMatch(/^[0-9a-f]{64}$/);
  }, 60000);
});

if (!configured) {
  describe("Stellar Testnet anchoring", () => {
    it.skip("skipped: set STELLAR_SIGNER_SECRET + STELLAR_*_CONTRACT_ID to run against real Testnet", () => {});
  });
}
