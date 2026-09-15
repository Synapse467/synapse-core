-- The original `immutable_version` trigger blocked EVERY update to
-- CapsuleVersion, including the legitimate one this system actually needs:
-- writing the real Stellar anchor result (txHash/ledger/anchoredAt) after
-- the async on-chain confirmation completes, which necessarily happens
-- after the version row is created. That made stellar-publish jobs fail
-- every single time (observed: a real, successful on-chain anchor could
-- never be recorded locally, and the retry then hit the contract's own
-- VersionAlreadyPublished guard).
--
-- PRD invariant #4 ("published capsule versions are immutable") is about
-- the approved knowledge content, not about async anchor metadata. This
-- replacement trigger keeps every content column immutable and additionally
-- keeps the anchor columns immutable ONCE SET (a NULL -> value transition is
-- allowed exactly once; changing an already-set anchor value is still
-- rejected), and deletes remain fully blocked.
CREATE OR REPLACE FUNCTION reject_immutable_version_change() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    RAISE EXCEPTION 'Published records and audit history are immutable';
  END IF;
  IF NEW."capsuleId" IS DISTINCT FROM OLD."capsuleId"
    OR NEW."version" IS DISTINCT FROM OLD."version"
    OR NEW."manifestHash" IS DISTINCT FROM OLD."manifestHash"
    OR NEW."manifest" IS DISTINCT FROM OLD."manifest"
    OR NEW."knowledge" IS DISTINCT FROM OLD."knowledge"
    OR NEW."evaluation" IS DISTINCT FROM OLD."evaluation"
    OR NEW."publishedAt" IS DISTINCT FROM OLD."publishedAt"
    OR (OLD."stellarTxHash" IS NOT NULL AND NEW."stellarTxHash" IS DISTINCT FROM OLD."stellarTxHash")
    OR (OLD."stellarLedger" IS NOT NULL AND NEW."stellarLedger" IS DISTINCT FROM OLD."stellarLedger")
    OR (OLD."stellarAnchoredAt" IS NOT NULL AND NEW."stellarAnchoredAt" IS DISTINCT FROM OLD."stellarAnchoredAt")
  THEN
    RAISE EXCEPTION 'Published records and audit history are immutable';
  END IF;
  RETURN NEW;
END; $$;

DROP TRIGGER IF EXISTS immutable_version ON "CapsuleVersion";
CREATE TRIGGER immutable_version BEFORE UPDATE OR DELETE ON "CapsuleVersion"
  FOR EACH ROW EXECUTE FUNCTION reject_immutable_version_change();
