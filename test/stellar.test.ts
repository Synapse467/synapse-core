import { describe, it, expect } from "vitest";
import { StellarService } from "../src/stellar";
import { hash, canonical } from "../src/core";

describe("StellarService anchoring and settlement", () => {
  const service = new StellarService();

  it("produces deterministic anchor results for capsule versions", async () => {
    const payload = {
      capsuleId: "capsule-123",
      version: "1.0.0",
      manifestHash: hash("manifest-content-v1"),
      evaluationHash: hash("eval-content-v1"),
    };

    const res1 = await service.anchorCapsuleVersion(payload);
    const res2 = await service.anchorCapsuleVersion(payload);

    expect(res1.txHash).toBe(res2.txHash);
    expect(res1.manifestHash).toBe(payload.manifestHash);
    expect(res1.explorerUrl).toContain(res1.txHash);
    expect(res1.ledgerSequence).toBeGreaterThan(48000000);
  });

  it("anchors license grants and usage receipts with verifiable hashes", async () => {
    const licensePayload = {
      licenseId: "lic-456",
      capsuleId: "capsule-123",
      grantee: "expert@domain.test",
      termsHash: hash("standard-academic-terms"),
      startsAt: new Date(Date.now() - 1000),
      expiresAt: new Date(Date.now() + 86400000),
    };

    const licRes = await service.anchorLicenseGrant(licensePayload);
    expect(licRes.txHash).toBe(hash(`stellar:license:${licensePayload.licenseId}:${licensePayload.termsHash}`));

    const usagePayload = {
      receiptId: "rec-789",
      licenseId: "lic-456",
      usageManifestHash: hash("batch-10-queries"),
      period: 20260901,
    };

    const usageRes = await service.anchorUsageReceipt(usagePayload);
    expect(usageRes.txHash).toBe(hash(`stellar:usage:${usagePayload.receiptId}:${usagePayload.usageManifestHash}`));
  });

  it("settles revenue splits without loss of rounding remainders", async () => {
    const settlementPayload = {
      settlementId: "set-001",
      totalAmount: 100001, // Odd number to test remainder distribution
      assetCode: "USDC",
      contributorShares: [
        { recipient: "GA1...", shareBps: 5000 }, // 50% = 50000
        { recipient: "GA2...", shareBps: 2500 }, // 25% = 25000
        { recipient: "GA3...", shareBps: 2500 }, // 25% = 25001 (remainder)
      ],
    };

    const receipt = await service.settleRevenueSplit(settlementPayload);
    expect(receipt.settlementId).toBe("set-001");
    expect(receipt.payouts).toHaveLength(3);

    const sum = receipt.payouts.reduce((acc, p) => acc + p.amount, 0);
    expect(sum).toBe(settlementPayload.totalAmount);
    expect(receipt.payouts[0].amount).toBe(50000);
    expect(receipt.payouts[1].amount).toBe(25000);
    expect(receipt.payouts[2].amount).toBe(25001); // 100001 - 75000
  });
});
