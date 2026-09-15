-- AlterTable
ALTER TABLE "User" ADD COLUMN "platformRole" TEXT NOT NULL DEFAULT 'NONE';

-- AlterTable
ALTER TABLE "LicenseTemplate" ADD COLUMN "pricePerUnitMinor" INTEGER NOT NULL DEFAULT 0;
ALTER TABLE "LicenseTemplate" ADD COLUMN "assetCode" TEXT NOT NULL DEFAULT 'USD';

-- AlterTable
ALTER TABLE "LicenseGrant" ADD COLUMN "pricePerUnitMinor" INTEGER NOT NULL DEFAULT 0;
ALTER TABLE "LicenseGrant" ADD COLUMN "assetCode" TEXT NOT NULL DEFAULT 'USD';

-- AlterTable
ALTER TABLE "UsageEvent" ADD COLUMN "receiptBatchId" TEXT;
ALTER TABLE "UsageEvent" ADD COLUMN "settled" BOOLEAN NOT NULL DEFAULT false;
CREATE INDEX "UsageEvent_capsuleId_receiptBatchId_idx" ON "UsageEvent"("capsuleId", "receiptBatchId");
CREATE INDEX "UsageEvent_capsuleId_settled_idx" ON "UsageEvent"("capsuleId", "settled");

-- CreateTable
CREATE TABLE "UsageReceiptBatch" (
    "id" TEXT NOT NULL,
    "capsuleId" TEXT NOT NULL,
    "licenseId" TEXT NOT NULL,
    "eventCount" INTEGER NOT NULL,
    "usageManifestHash" TEXT NOT NULL,
    "stellarTxHash" TEXT,
    "stellarAnchoredAt" TIMESTAMP(3),
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT "UsageReceiptBatch_pkey" PRIMARY KEY ("id")
);
CREATE INDEX "UsageReceiptBatch_capsuleId_createdAt_idx" ON "UsageReceiptBatch"("capsuleId", "createdAt");

-- CreateTable
CREATE TABLE "SettlementEvent" (
    "id" TEXT NOT NULL,
    "settlementRef" TEXT NOT NULL,
    "capsuleId" TEXT NOT NULL,
    "contributorId" TEXT NOT NULL,
    "amountMinor" INTEGER NOT NULL,
    "assetCode" TEXT NOT NULL,
    "status" TEXT NOT NULL DEFAULT 'PENDING',
    "stellarTxHash" TEXT,
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT "SettlementEvent_pkey" PRIMARY KEY ("id")
);
CREATE INDEX "SettlementEvent_capsuleId_createdAt_idx" ON "SettlementEvent"("capsuleId", "createdAt");
CREATE INDEX "SettlementEvent_settlementRef_idx" ON "SettlementEvent"("settlementRef");
