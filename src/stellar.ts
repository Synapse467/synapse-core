import { Injectable, Logger, ServiceUnavailableException } from "@nestjs/common";
import {
  Keypair,
  Networks,
  Contract,
  TransactionBuilder,
  BASE_FEE,
  Address,
  nativeToScVal,
  scValToNative,
  rpc,
  xdr,
  type Transaction,
} from "@stellar/stellar-sdk";
import { hash } from "./core";

export interface AnchorResult {
  txHash: string;
  ledgerSequence: number;
  anchoredAt: string;
  network: string;
  manifestHash: string;
  contractId: string;
  explorerUrl: string;
}

export interface ReceiptAnchorResult {
  txHash: string;
  anchoredAt: string;
  contractId: string;
  explorerUrl: string;
}

export interface SettlementReceipt {
  settlementId: string;
  /** Hash of the computed payout plan, NOT a Stellar transaction hash — no asset transfer has occurred. See settleRevenueSplit doc. */
  planHash: string;
  totalAmount: number;
  assetCode: string;
  payouts: Array<{ recipient: string; amount: number }>;
  settledAt: string;
}

/**
 * Real Soroban integration (PRD §17, §29). This service submits actual
 * transactions to the deployed contracts on Stellar Testnet/Mainnet via
 * Soroban RPC — it never fabricates a tx hash.
 *
 * MVP custodial-signing note: every on-chain "owner"/"licensor"/"admin"
 * argument is currently the platform's own signing key
 * (`STELLAR_SIGNER_SECRET`), because `require_auth()` in the contracts can only
 * be satisfied by whoever actually signs the transaction, and today's
 * experts authenticate to the API with email/password or a read-only wallet
 * link rather than co-signing every anchor with their own Freighter wallet.
 * This is documented here rather than hidden: per-expert self-custodied
 * signing (the user's own wallet authorizing `register_capsule`/`grant`
 * directly) is a real, tracked Phase 2 gap, not a silent shortcut — see
 * repo README "Incomplete product requirements".
 */
@Injectable()
export class StellarService {
  private readonly logger = new Logger(StellarService.name);
  private network = process.env.STELLAR_NETWORK || "TESTNET";
  private rpcUrl =
    process.env.STELLAR_RPC_URL || "https://soroban-testnet.stellar.org";
  private server = new rpc.Server(this.rpcUrl, {
    allowHttp: this.rpcUrl.startsWith("http://"),
  });
  private keypair?: Keypair;
  private capsuleRegistryId = process.env.STELLAR_CAPSULE_CONTRACT_ID;
  private licenseRegistryId = process.env.STELLAR_LICENSE_CONTRACT_ID;
  private usageReceiptRegistryId =
    process.env.STELLAR_USAGE_CONTRACT_ID;
  private settlementRegistryId = process.env.STELLAR_SETTLEMENT_CONTRACT_ID;

  constructor() {
    const secret = process.env.STELLAR_SIGNER_SECRET;
    if (secret) {
      try {
        this.keypair = Keypair.fromSecret(secret);
      } catch {
        this.logger.error(
          "STELLAR_SIGNER_SECRET is set but is not a valid Stellar secret key.",
        );
      }
    }
  }

  private networkPassphrase(): string {
    return this.network === "PUBLIC" ? Networks.PUBLIC : Networks.TESTNET;
  }

  private explorer(txHash: string): string {
    return `https://stellar.expert/explorer/${this.network.toLowerCase()}/tx/${txHash}`;
  }

  private requireSigner(): Keypair {
    if (!this.keypair)
      throw new ServiceUnavailableException(
        "Stellar signing key is not configured (STELLAR_SIGNER_SECRET).",
      );
    return this.keypair;
  }

  private bytes32(hex: string): xdr.ScVal {
    if (!/^[a-f0-9]{64}$/i.test(hex))
      throw new Error(`Expected a 32-byte hex value, got: ${hex}`);
    return nativeToScVal(Buffer.from(hex, "hex"), { type: "bytes" });
  }

  /** Encodes one `settlement::ContributorShare { recipient: Address, share_bps: u32 }`
   * Soroban struct as its wire representation (an ScMap keyed by field-name
   * Symbols, matching how soroban-sdk derives struct (de)serialization) —
   * there is no generated TS binding for this contract, so this is built by
   * hand against the exact field names/order in contracts/settlement/src/lib.rs. */
  private contributorShareScVal(recipient: string, shareBps: number): xdr.ScVal {
    return xdr.ScVal.scvMap([
      new xdr.ScMapEntry({
        key: nativeToScVal("recipient", { type: "symbol" }),
        val: new Address(recipient).toScVal(),
      }),
      new xdr.ScMapEntry({
        key: nativeToScVal("share_bps", { type: "symbol" }),
        val: nativeToScVal(shareBps, { type: "u32" }),
      }),
    ]);
  }

  /** Builds, simulates, prepares, signs, submits and polls a contract call to completion. Never fabricates a result. */
  private async invoke(
    contractId: string,
    method: string,
    params: xdr.ScVal[],
  ): Promise<{ txHash: string; ledgerSequence: number; returnValue: unknown }> {
    const signer = this.requireSigner();
    const source = await this.server.getAccount(signer.publicKey());
    const contract = new Contract(contractId);
    const built = new TransactionBuilder(source, {
      fee: BASE_FEE,
      networkPassphrase: this.networkPassphrase(),
    })
      .addOperation(contract.call(method, ...params))
      .setTimeout(60)
      .build();
    let prepared: Transaction;
    try {
      prepared = await this.server.prepareTransaction(built);
    } catch (err) {
      throw new Error(`Stellar simulation failed for ${method}: ${err}`);
    }
    prepared.sign(signer);
    const sendResult = await this.server.sendTransaction(prepared);
    if (sendResult.status === "ERROR")
      throw new Error(
        `Stellar submission failed for ${method}: ${JSON.stringify(sendResult.errorResult)}`,
      );
    const deadline = Date.now() + 30000;
    let getResult = await this.server.getTransaction(sendResult.hash);
    while (
      getResult.status === rpc.Api.GetTransactionStatus.NOT_FOUND &&
      Date.now() < deadline
    ) {
      await new Promise((resolve) => setTimeout(resolve, 1500));
      getResult = await this.server.getTransaction(sendResult.hash);
    }
    if (getResult.status !== rpc.Api.GetTransactionStatus.SUCCESS)
      throw new Error(
        `Stellar transaction for ${method} did not succeed: ${getResult.status}`,
      );
    return {
      txHash: sendResult.hash,
      ledgerSequence: getResult.ledger,
      returnValue: getResult.returnValue
        ? scValToNative(getResult.returnValue)
        : undefined,
    };
  }

  /** Idempotently registers a capsule ref, swallowing "already exists" (contract error code 2). */
  private async ensureCapsuleRegistered(
    contractId: string,
    capsuleRef: string,
    metadataHash: string,
  ): Promise<void> {
    const signer = this.requireSigner();
    try {
      await this.invoke(contractId, "register_capsule", [
        this.bytes32(capsuleRef),
        new Address(signer.publicKey()).toScVal(),
        this.bytes32(metadataHash),
      ]);
    } catch (err) {
      if (!String(err).includes("Error(Contract, #2)")) throw err;
    }
  }

  async anchorCapsuleVersion(payload: {
    capsuleId: string;
    version: string;
    manifestHash: string;
    evaluationHash: string;
  }): Promise<AnchorResult> {
    if (!this.capsuleRegistryId)
      throw new ServiceUnavailableException(
        "STELLAR_CAPSULE_CONTRACT_ID is not configured.",
      );
    const capsuleRef = hash(payload.capsuleId);
    const versionRef = hash(`${payload.capsuleId}:${payload.version}`);
    await this.ensureCapsuleRegistered(
      this.capsuleRegistryId,
      capsuleRef,
      hash(payload.capsuleId),
    );
    const result = await this.invoke(this.capsuleRegistryId, "publish_version", [
      this.bytes32(capsuleRef),
      this.bytes32(versionRef),
      this.bytes32(payload.manifestHash),
      this.bytes32(payload.evaluationHash),
    ]);
    this.logger.log(
      JSON.stringify({
        event: "stellar.anchored",
        capsuleId: payload.capsuleId,
        version: payload.version,
        manifestHash: payload.manifestHash,
        txHash: result.txHash,
        ledgerSequence: result.ledgerSequence,
      }),
    );
    return {
      txHash: result.txHash,
      ledgerSequence: result.ledgerSequence,
      anchoredAt: new Date().toISOString(),
      network: this.network,
      manifestHash: payload.manifestHash,
      contractId: this.capsuleRegistryId,
      explorerUrl: this.explorer(result.txHash),
    };
  }

  async anchorLicenseGrant(payload: {
    licenseId: string;
    capsuleId: string;
    version: string;
    grantee: string;
    termsHash: string;
    startsAt: Date;
    expiresAt: Date;
  }): Promise<ReceiptAnchorResult> {
    if (!this.licenseRegistryId)
      throw new ServiceUnavailableException(
        "STELLAR_LICENSE_CONTRACT_ID is not configured.",
      );
    const signer = this.requireSigner();
    const licenseRef = hash(payload.licenseId);
    const versionRef = hash(`${payload.capsuleId}:${payload.version}`);
    // The grantee is an off-chain email in this MVP (no wallet requirement to
    // receive a license), so its on-chain address is a deterministic,
    // non-spendable placeholder derived from the license ref — the terms
    // hash and grant record are the source of truth; see README.
    const granteeAddress = Keypair.fromRawEd25519Seed(
      Buffer.from(hash(`grantee:${payload.grantee}:${payload.licenseId}`), "hex").subarray(0, 32),
    ).publicKey();
    const result = await this.invoke(this.licenseRegistryId, "grant", [
      this.bytes32(licenseRef),
      new Address(signer.publicKey()).toScVal(),
      this.bytes32(versionRef),
      new Address(granteeAddress).toScVal(),
      this.bytes32(payload.termsHash),
      nativeToScVal(BigInt(Math.floor(payload.startsAt.getTime() / 1000)), {
        type: "u64",
      }),
      nativeToScVal(BigInt(Math.floor(payload.expiresAt.getTime() / 1000)), {
        type: "u64",
      }),
    ]);
    return {
      txHash: result.txHash,
      anchoredAt: new Date().toISOString(),
      contractId: this.licenseRegistryId,
      explorerUrl: this.explorer(result.txHash),
    };
  }

  async anchorLicenseRevocation(licenseId: string): Promise<ReceiptAnchorResult> {
    if (!this.licenseRegistryId)
      throw new ServiceUnavailableException(
        "STELLAR_LICENSE_CONTRACT_ID is not configured.",
      );
    const result = await this.invoke(this.licenseRegistryId, "revoke", [
      this.bytes32(hash(licenseId)),
    ]);
    return {
      txHash: result.txHash,
      anchoredAt: new Date().toISOString(),
      contractId: this.licenseRegistryId,
      explorerUrl: this.explorer(result.txHash),
    };
  }

  async anchorUsageReceipt(payload: {
    receiptId: string;
    licenseId: string;
    usageManifestHash: string;
    period: number;
  }): Promise<ReceiptAnchorResult> {
    if (!this.usageReceiptRegistryId)
      throw new ServiceUnavailableException(
        "STELLAR_USAGE_CONTRACT_ID is not configured.",
      );
    const signer = this.requireSigner();
    const result = await this.invoke(this.usageReceiptRegistryId, "record", [
      this.bytes32(hash(payload.receiptId)),
      this.bytes32(hash(payload.licenseId)),
      this.bytes32(payload.usageManifestHash),
      nativeToScVal(BigInt(payload.period), { type: "u64" }),
      new Address(signer.publicKey()).toScVal(),
    ]);
    return {
      txHash: result.txHash,
      anchoredAt: new Date().toISOString(),
      contractId: this.usageReceiptRegistryId,
      explorerUrl: this.explorer(result.txHash),
    };
  }

  /**
   * Submits a REAL on-chain `settlement.settle_split` call (PRD §17/§19,
   * `GET /capsules/:id/settlements`, `SettlementEvent`). This anchors an
   * immutable, verifiable record of "capsule earned totalAmount, split
   * these ways among these contributor addresses" on Stellar — it is the
   * proof-of-split ledger entry the PRD's `Settlement` contract exists for.
   *
   * It does NOT move real-world funds/assets to contributors: the contract
   * itself only records payout math (see contracts/settlement/src/lib.rs —
   * `settle_split` has no Payment operation, only persistent storage of the
   * computed `PayoutEntry` list). Actually disbursing real money to expert
   * bank accounts/wallets is a separate, later business/legal integration
   * (a real payment rail) — see README "Incomplete product requirements".
   */
  async anchorSettlement(payload: {
    settlementRef: string;
    totalAmountMinor: number;
    contributorShares: Array<{ recipient: string; shareBps: number }>;
  }): Promise<ReceiptAnchorResult & { payouts: Array<{ recipient: string; amount: number }> }> {
    if (!this.settlementRegistryId)
      throw new ServiceUnavailableException(
        "STELLAR_SETTLEMENT_CONTRACT_ID is not configured.",
      );
    const totalBps = payload.contributorShares.reduce((sum, s) => sum + s.shareBps, 0);
    if (totalBps !== 10000)
      throw new Error("Contributor shares must sum to exactly 10000 bps.");
    const signer = this.requireSigner();
    const sharesScVal = xdr.ScVal.scvVec(
      payload.contributorShares.map((s) =>
        this.contributorShareScVal(s.recipient, s.shareBps),
      ),
    );
    const result = await this.invoke(this.settlementRegistryId, "settle_split", [
      this.bytes32(payload.settlementRef),
      new Address(signer.publicKey()).toScVal(),
      nativeToScVal(BigInt(payload.totalAmountMinor), { type: "i128" }),
      sharesScVal,
    ]);
    const payoutEntries = (result.returnValue as Array<{ recipient: string; amount: bigint }>) || [];
    return {
      txHash: result.txHash,
      anchoredAt: new Date().toISOString(),
      contractId: this.settlementRegistryId,
      explorerUrl: this.explorer(result.txHash),
      payouts: payoutEntries.map((p) => ({
        recipient: p.recipient,
        amount: Number(p.amount),
      })),
    };
  }

  /**
   * Off-chain preview of the same split arithmetic `settle_split` performs
   * (basis-point/remainder handling), used by the settlements API to show a
   * proposed payout plan before it is anchored on-chain. See
   * `anchorSettlement` for the real on-chain call.
   */
  async settleRevenueSplit(payload: {
    settlementId: string;
    totalAmount: number;
    assetCode: string;
    contributorShares: Array<{ recipient: string; shareBps: number }>;
  }): Promise<SettlementReceipt> {
    const totalBps = payload.contributorShares.reduce(
      (sum, s) => sum + s.shareBps,
      0,
    );
    if (totalBps !== 10000)
      throw new Error("Contributor shares must sum to exactly 10000 bps.");
    let distributed = 0;
    const payouts = payload.contributorShares.map((share, idx) => {
      const isLast = idx === payload.contributorShares.length - 1;
      const amount = isLast
        ? payload.totalAmount - distributed
        : Math.floor((payload.totalAmount * share.shareBps) / 10000);
      distributed += amount;
      return { recipient: share.recipient, amount };
    });
    const settledAt = new Date().toISOString();
    const planHash = hash(
      `settlement-plan:${payload.settlementId}:${payload.totalAmount}:${settledAt}`,
    );
    return {
      settlementId: payload.settlementId,
      planHash,
      totalAmount: payload.totalAmount,
      assetCode: payload.assetCode,
      payouts,
      settledAt,
    };
  }
}
