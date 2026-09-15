-- Needed to actually transcribe recorded interview audio (previously the
-- segment's content type was discarded after the upload ticket was
-- consumed, so nothing downstream could tell the AI service what kind of
-- file it was receiving).
ALTER TABLE "InterviewSegment" ADD COLUMN "contentType" TEXT NOT NULL DEFAULT 'audio/webm';
