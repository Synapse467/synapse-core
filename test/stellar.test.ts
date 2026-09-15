import { describe, it, expect } from "vitest";
import { StellarService } from "../src/stellar";

describe("StellarService.settleRevenueSplit (pure arithmetic, no network)", () => {
  const service = new StellarService();

  it("computes payouts without loss of rounding remainders", async () => {
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

  it("rejects shares that do not sum to exactly 10000 bps", async () => {
    await expect(
      service.settleRevenueSplit({
        settlementId: "set-002",
        totalAmount: 1000,
        assetCode: "USDC",
        contributorShares: [{ recipient: "GA1...", shareBps: 9000 }],
      }),
    ).rejects.toThrow(/10000 bps/);
  });

  it("throws (never fabricates) when no Stellar signer is configured", async () => {
    const unconfigured = new StellarService();
    await expect(
      unconfigured.anchorCapsuleVersion({
        capsuleId: "c1",
        version: "1.0.0",
        manifestHash: "a".repeat(64),
        evaluationHash: "b".repeat(64),
      }),
    ).rejects.toThrow(/not configured/);
  });
});
