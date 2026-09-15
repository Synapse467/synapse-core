-- DropForeignKey
ALTER TABLE "AccessRequest" DROP CONSTRAINT "AccessRequest_capsuleId_fkey";

-- DropForeignKey
ALTER TABLE "AccessRequest" DROP CONSTRAINT "AccessRequest_templateId_fkey";

-- DropForeignKey
ALTER TABLE "AccessRequest" DROP CONSTRAINT "AccessRequest_userId_fkey";

-- DropForeignKey
ALTER TABLE "Capsule" DROP CONSTRAINT "Capsule_ownerId_fkey";

-- DropForeignKey
ALTER TABLE "CapsuleVersion" DROP CONSTRAINT "CapsuleVersion_capsuleId_fkey";

-- DropForeignKey
ALTER TABLE "Conversation" DROP CONSTRAINT "Conversation_capsuleId_fkey";

-- DropForeignKey
ALTER TABLE "Conversation" DROP CONSTRAINT "Conversation_userId_fkey";

-- DropForeignKey
ALTER TABLE "Evaluation" DROP CONSTRAINT "Evaluation_capsuleId_fkey";

-- DropForeignKey
ALTER TABLE "EvaluationCase" DROP CONSTRAINT "EvaluationCase_approvedBy_fkey";

-- DropForeignKey
ALTER TABLE "EvaluationCase" DROP CONSTRAINT "EvaluationCase_capsuleId_fkey";

-- DropForeignKey
ALTER TABLE "Interview" DROP CONSTRAINT "Interview_capsuleId_fkey";

-- DropForeignKey
ALTER TABLE "Interview" DROP CONSTRAINT "Interview_ownerId_fkey";

-- DropForeignKey
ALTER TABLE "InterviewSegment" DROP CONSTRAINT "InterviewSegment_interviewId_fkey";

-- DropForeignKey
ALTER TABLE "KnowledgeItem" DROP CONSTRAINT "KnowledgeItem_capsuleId_fkey";

-- DropForeignKey
ALTER TABLE "KnowledgeItem" DROP CONSTRAINT "KnowledgeItem_contributorId_fkey";

-- DropForeignKey
ALTER TABLE "KnowledgeItem" DROP CONSTRAINT "KnowledgeItem_sourceId_fkey";

-- DropForeignKey
ALTER TABLE "LicenseGrant" DROP CONSTRAINT "LicenseGrant_capsuleId_fkey";

-- DropForeignKey
ALTER TABLE "LicenseGrant" DROP CONSTRAINT "LicenseGrant_ownerId_fkey";

-- DropForeignKey
ALTER TABLE "LicenseGrant" DROP CONSTRAINT "LicenseGrant_templateId_fkey";

-- DropForeignKey
ALTER TABLE "LicenseTemplate" DROP CONSTRAINT "LicenseTemplate_capsuleId_fkey";

-- DropForeignKey
ALTER TABLE "Message" DROP CONSTRAINT "Message_conversationId_fkey";

-- DropForeignKey
ALTER TABLE "Message" DROP CONSTRAINT "Message_userId_fkey";

-- DropForeignKey
ALTER TABLE "Session" DROP CONSTRAINT "Session_userId_fkey";

-- DropForeignKey
ALTER TABLE "SourceAsset" DROP CONSTRAINT "SourceAsset_capsuleId_fkey";

-- DropForeignKey
ALTER TABLE "SourceAsset" DROP CONSTRAINT "SourceAsset_contributorId_fkey";

-- DropForeignKey
ALTER TABLE "UploadTicket" DROP CONSTRAINT "UploadTicket_capsuleId_fkey";

-- DropForeignKey
ALTER TABLE "UploadTicket" DROP CONSTRAINT "UploadTicket_ownerId_fkey";

-- DropForeignKey
ALTER TABLE "UsageEvent" DROP CONSTRAINT "UsageEvent_actorId_fkey";

-- DropForeignKey
ALTER TABLE "UsageEvent" DROP CONSTRAINT "UsageEvent_capsuleId_fkey";

-- DropForeignKey
ALTER TABLE "UsageEvent" DROP CONSTRAINT "UsageEvent_capsuleVersionId_fkey";

-- DropForeignKey
ALTER TABLE "UsageEvent" DROP CONSTRAINT "UsageEvent_licenseId_fkey";

-- AlterTable
ALTER TABLE "Capsule" ADD COLUMN     "ownerType" TEXT NOT NULL DEFAULT 'USER';

-- CreateTable
CREATE TABLE "Organization" (
    "id" TEXT NOT NULL,
    "name" TEXT NOT NULL,
    "slug" TEXT NOT NULL,
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT "Organization_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "OrganizationMembership" (
    "id" TEXT NOT NULL,
    "organizationId" TEXT NOT NULL,
    "userId" TEXT NOT NULL,
    "role" TEXT NOT NULL DEFAULT 'MEMBER',
    "totpSecret" TEXT,
    "totpEnabled" BOOLEAN NOT NULL DEFAULT false,
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT "OrganizationMembership_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "ExpertCredential" (
    "id" TEXT NOT NULL,
    "expertId" TEXT NOT NULL,
    "type" TEXT NOT NULL,
    "issuer" TEXT NOT NULL,
    "evidenceObjectKey" TEXT,
    "verificationStatus" TEXT NOT NULL DEFAULT 'PENDING',
    "reviewedBy" TEXT,
    "reviewedAt" TIMESTAMP(3),
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT "ExpertCredential_pkey" PRIMARY KEY ("id")
);

-- CreateIndex
CREATE UNIQUE INDEX "Organization_slug_key" ON "Organization"("slug");

-- CreateIndex
CREATE INDEX "OrganizationMembership_userId_idx" ON "OrganizationMembership"("userId");

-- CreateIndex
CREATE UNIQUE INDEX "OrganizationMembership_organizationId_userId_key" ON "OrganizationMembership"("organizationId", "userId");

-- CreateIndex
CREATE INDEX "ExpertCredential_expertId_idx" ON "ExpertCredential"("expertId");
