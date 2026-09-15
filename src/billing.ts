import { hash, canonical } from "./core";

export type ShareHolder = {
  contributorId: string;
  stellarPublicKey: string | null;
};

export type ContributorShare = {
  contributorId: string;
  recipient: string | null;
  shareBps: number;
};

/**
 * Equal-share split among contributors who have a Stellar public key.
 * Remainder basis points go to the last share-holder so the total is always
 * exactly 10000. If nobody (including the capsule owner) has a wallet, the
 * owner still receives 10000 bps with `recipient: null` — the on-chain
 * `settle_split` call is skipped in that case because Soroban needs Addresses.
 */
export function contributorSharePlan(
  owner: ShareHolder,
  contributors: ShareHolder[],
): ContributorShare[] {
  const unique = new Map<string, ShareHolder>();
  unique.set(owner.contributorId, owner);
  for (const person of contributors) unique.set(person.contributorId, person);
  const withWallet = [...unique.values()].filter((p) => p.stellarPublicKey);
  const pool = withWallet.length ? withWallet : [owner];
  const base = Math.floor(10000 / pool.length);
  let remainder = 10000 - base * pool.length;
  return pool.map((person, index) => {
    const extra = index === pool.length - 1 ? remainder : 0;
    if (index === pool.length - 1) remainder = 0;
    return {
      contributorId: person.contributorId,
      recipient: person.stellarPublicKey,
      shareBps: base + extra,
    };
  });
}

/** Opaque usage proof: event ids + unit counts, never query text (PRD §17). */
export function usageManifestHash(input: {
  licenseId: string;
  period: number;
  events: Array<{ id: string; units: number }>;
}): string {
  return hash(
    canonical({
      licenseId: input.licenseId,
      period: input.period,
      eventCount: input.events.length,
      units: input.events.reduce((sum, event) => sum + event.units, 0),
      eventIds: input.events.map((event) => event.id).sort(),
    }),
  );
}

export function usagePeriod(at = new Date()): number {
  return at.getUTCFullYear() * 10000 + (at.getUTCMonth() + 1) * 100 + at.getUTCDate();
}
