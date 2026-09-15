-- Fixes drift between the initial migration and schema.prisma: User.stellarPublicKey
-- was already used throughout src/controller.ts (wallet sign-in) but was never
-- migrated, so a fresh deploy of the previous migration alone would crash on
-- the first wallet-auth registration. Also adds real Stellar anchor tracking
-- columns to CapsuleVersion (PRD §29 "manifest hash anchored on Testnet") and
-- LicenseGrant (PRD §17 LicenseRegistry).
ALTER TABLE "User" ADD COLUMN "stellarPublicKey" TEXT;
CREATE UNIQUE INDEX "User_stellarPublicKey_key" ON "User"("stellarPublicKey");

ALTER TABLE "CapsuleVersion" ADD COLUMN "stellarTxHash" TEXT;
ALTER TABLE "CapsuleVersion" ADD COLUMN "stellarAnchoredAt" TIMESTAMP(3);
ALTER TABLE "CapsuleVersion" ADD COLUMN "stellarLedger" INTEGER;

ALTER TABLE "LicenseGrant" ADD COLUMN "stellarTxHash" TEXT;
ALTER TABLE "LicenseGrant" ADD COLUMN "stellarAnchoredAt" TIMESTAMP(3);
ALTER TABLE "LicenseGrant" ADD COLUMN "stellarRevokeTxHash" TEXT;
