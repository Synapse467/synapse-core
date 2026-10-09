// Package chain is the optional Stellar layer.
//
// Nothing in Synapse needs it: capsules, licenses and usage logs all work offline. When it is
// used, it anchors capsule versions, records license grants and revocations, and records sealed
// usage batches on Soroban contracts that anyone can call. There is nothing to configure: the
// defaults point at the public Testnet deployment, and a new identity is funded automatically
// through Friendbot.
package chain

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/stellar/go-stellar-sdk/clients/rpcclient"
	"github.com/stellar/go-stellar-sdk/keypair"
	protocol "github.com/stellar/go-stellar-sdk/protocols/rpc"
	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/txnbuild"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// Config says which network and contracts to use. The zero value is not useful; start from
// Testnet().
type Config struct {
	RPCURL     string `json:"rpcUrl"`
	Passphrase string `json:"passphrase"`
	Friendbot  string `json:"friendbot,omitempty"`
	Anchor     string `json:"capsuleAnchor"`
	License    string `json:"licenseLedger"`
	Usage      string `json:"usageLedger"`
}

// Testnet returns the public Testnet deployment that ships with Synapse.
func Testnet() Config {
	return Config{
		RPCURL:     "https://soroban-testnet.stellar.org",
		Passphrase: "Test SDF Network ; September 2015",
		Friendbot:  "https://friendbot.stellar.org",
		Anchor:     "CAII7IQVEDGYE3VMO4JZA2V7JMPSHIISJFBX2LWV7XPAP5GQUL6UPXQP",
		License:    "CAI26T22K4EU4M6OQA7RJUAV5PQZYFJQJIIAPW2FUXJCQ3ADAWQMBMFN",
		Usage:      "CDNVTWXTSQ66D34KKCBUIESF2OSA67L7WU7SWOGSLI7IYONVHLF7XHKX",
	}
}

// Validate checks that a configuration is complete and well formed.
func (c Config) Validate() error {
	if c.RPCURL == "" || c.Passphrase == "" {
		return errors.New("chain: the RPC URL and network passphrase are required")
	}
	for name, id := range map[string]string{"capsule anchor": c.Anchor, "license ledger": c.License, "usage ledger": c.Usage} {
		if !strkey.IsValidContractAddress(id) {
			return fmt.Errorf("chain: the %s contract address is not valid", name)
		}
	}
	return nil
}

// Client sends transactions and reads state, signing as one identity.
type Client struct {
	cfg  Config
	rpc  *rpcclient.Client
	kp   *keypair.Full
	http *http.Client
}

// New creates a client. The keypair is the identity that signs and pays; pass the one from the
// identity package.
func New(cfg Config, kp *keypair.Full) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if kp == nil {
		return nil, errors.New("chain: an identity is required")
	}
	h := &http.Client{Timeout: 60 * time.Second}
	return &Client{cfg: cfg, rpc: rpcclient.NewClient(cfg.RPCURL, h), kp: kp, http: h}, nil
}

// Address returns the account that signs and pays.
func (c *Client) Address() string { return c.kp.Address() }

// Result is the outcome of a write.
type Result struct {
	TxHash string `json:"txHash"`
	Ledger uint32 `json:"ledger"`
}

// EnsureFunded makes sure the account exists on the network, asking Friendbot to create it if it
// does not. This only works on test networks.
func (c *Client) EnsureFunded(ctx context.Context) error {
	if _, err := c.rpc.LoadAccount(ctx, c.kp.Address()); err == nil {
		return nil
	}
	if c.cfg.Friendbot == "" {
		return errors.New("chain: the account does not exist on this network and no Friendbot is configured to create it")
	}
	endpoint := c.cfg.Friendbot + "?addr=" + url.QueryEscape(c.kp.Address())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("chain: Friendbot could not be reached: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 300 && !strings.Contains(string(body), "createAccountAlreadyExist") {
		return fmt.Errorf("chain: Friendbot refused to fund the account (status %d)", resp.StatusCode)
	}
	for i := 0; i < 15; i++ {
		if _, err := c.rpc.LoadAccount(ctx, c.kp.Address()); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return errors.New("chain: the account was funded but is not visible yet; try again in a moment")
}

// Anchor records a capsule version. capsuleRef and the hashes are 64-character hex strings.
func (c *Client) Anchor(ctx context.Context, capsuleRef string, version int, manifestHash, previousHash string) (*Result, error) {
	args, err := argList(
		address(c.kp.Address()), bytes32(capsuleRef), u32(version), bytes32(manifestHash), bytes32(previousHash),
	)
	if err != nil {
		return nil, err
	}
	return c.invokeWrite(ctx, c.cfg.Anchor, "anchor", args)
}

// LatestAnchor returns the newest anchored version of a capsule, or 0 if there is none.
func (c *Client) LatestAnchor(ctx context.Context, owner, capsuleRef string) (int, error) {
	args, err := argList(address(owner), bytes32(capsuleRef))
	if err != nil {
		return 0, err
	}
	val, err := c.invokeRead(ctx, c.cfg.Anchor, "latest", args)
	if err != nil {
		return 0, err
	}
	n, ok := val.GetU32()
	if !ok {
		return 0, errors.New("chain: unexpected reply from the capsule anchor")
	}
	return int(n), nil
}

// AnchoredHash returns the manifest hash anchored for a version, and whether one exists.
func (c *Client) AnchoredHash(ctx context.Context, owner, capsuleRef string, version int) (string, bool, error) {
	args, err := argList(address(owner), bytes32(capsuleRef), u32(version))
	if err != nil {
		return "", false, err
	}
	val, err := c.invokeRead(ctx, c.cfg.Anchor, "get", args)
	if err != nil {
		return "", false, err
	}
	if val.Type == xdr.ScValTypeScvVoid {
		return "", false, nil
	}
	m, ok := val.GetMap()
	if !ok || m == nil {
		return "", false, errors.New("chain: unexpected reply from the capsule anchor")
	}
	for _, entry := range *m {
		if sym, ok := entry.Key.GetSym(); ok && string(sym) == "manifest_hash" {
			if b, ok := entry.Val.GetBytes(); ok {
				return hex.EncodeToString(b), true, nil
			}
		}
	}
	return "", false, errors.New("chain: the anchor has no manifest hash")
}

// Grant records a license. expiresAt is a Unix time, or 0 for no expiry.
func (c *Client) Grant(ctx context.Context, licenseRef, capsuleRef, grantee, termsHash string, expiresAt int64) (*Result, error) {
	if expiresAt < 0 {
		return nil, errors.New("chain: the expiry must not be negative")
	}
	args, err := argList(
		address(c.kp.Address()), bytes32(licenseRef), bytes32(capsuleRef), address(grantee), bytes32(termsHash), u64(uint64(expiresAt)),
	)
	if err != nil {
		return nil, err
	}
	return c.invokeWrite(ctx, c.cfg.License, "grant", args)
}

// Revoke records that a license has been revoked.
func (c *Client) Revoke(ctx context.Context, licenseRef string) (*Result, error) {
	args, err := argList(address(c.kp.Address()), bytes32(licenseRef))
	if err != nil {
		return nil, err
	}
	return c.invokeWrite(ctx, c.cfg.License, "revoke", args)
}

// LicenseActive asks the chain whether a license is granted, unrevoked and unexpired.
func (c *Client) LicenseActive(ctx context.Context, grantor, licenseRef string) (bool, error) {
	args, err := argList(address(grantor), bytes32(licenseRef))
	if err != nil {
		return false, err
	}
	val, err := c.invokeRead(ctx, c.cfg.License, "is_active", args)
	if err != nil {
		return false, err
	}
	b, ok := val.GetB()
	if !ok {
		return false, errors.New("chain: unexpected reply from the license ledger")
	}
	return b, nil
}

// Receipt describes one sealed usage batch to record.
type Receipt struct {
	LicenseRef  string
	Seq         int
	BatchHash   string
	PreviousHex string
	Count       int
	PeriodStart int64
	PeriodEnd   int64
}

// RecordUsage records a sealed usage batch.
func (c *Client) RecordUsage(ctx context.Context, r Receipt) (*Result, error) {
	if r.PeriodStart < 0 || r.PeriodEnd < 0 {
		return nil, errors.New("chain: the period must not be negative")
	}
	args, err := argList(
		address(c.kp.Address()), bytes32(r.LicenseRef), u32(r.Seq), bytes32(r.BatchHash), bytes32(r.PreviousHex),
		u32(r.Count), u64(uint64(r.PeriodStart)), u64(uint64(r.PeriodEnd)),
	)
	if err != nil {
		return nil, err
	}
	return c.invokeWrite(ctx, c.cfg.Usage, "record", args)
}

// --- transaction plumbing ---

func (c *Client) invokeRead(ctx context.Context, contract, fn string, args []xdr.ScVal) (xdr.ScVal, error) {
	// Reads use a source account, but never submit, so the zero-balance placeholder is fine when
	// the identity does not exist yet.
	var source txnbuild.Account = &txnbuild.SimpleAccount{AccountID: c.kp.Address(), Sequence: 0}
	if acct, err := c.rpc.LoadAccount(ctx, c.kp.Address()); err == nil {
		source = acct
	}
	tx, err := buildInvoke(source, c.cfg.Passphrase, contract, fn, args, nil, nil)
	if err != nil {
		return xdr.ScVal{}, err
	}
	sim, err := c.simulate(ctx, tx)
	if err != nil {
		return xdr.ScVal{}, err
	}
	if len(sim.Results) == 0 || sim.Results[0].ReturnValueXDR == nil {
		return xdr.ScVal{}, errors.New("chain: the contract returned nothing")
	}
	var val xdr.ScVal
	if err := xdr.SafeUnmarshalBase64(*sim.Results[0].ReturnValueXDR, &val); err != nil {
		return xdr.ScVal{}, fmt.Errorf("chain: could not read the contract's reply: %w", err)
	}
	return val, nil
}

func (c *Client) invokeWrite(ctx context.Context, contract, fn string, args []xdr.ScVal) (*Result, error) {
	if err := c.EnsureFunded(ctx); err != nil {
		return nil, err
	}
	for attempt := 0; attempt < 2; attempt++ {
		source, err := c.rpc.LoadAccount(ctx, c.kp.Address())
		if err != nil {
			return nil, fmt.Errorf("chain: could not load the account: %w", err)
		}
		tx, err := buildInvoke(source, c.cfg.Passphrase, contract, fn, args, nil, nil)
		if err != nil {
			return nil, err
		}
		sim, err := c.simulate(ctx, tx)
		if err != nil {
			return nil, err
		}
		if sim.RestorePreamble != nil {
			// Some of the data this call needs has expired and must be restored first.
			if err := c.restore(ctx, sim.RestorePreamble); err != nil {
				return nil, err
			}
			continue
		}
		var data xdr.SorobanTransactionData
		if err := xdr.SafeUnmarshalBase64(sim.TransactionDataXDR, &data); err != nil {
			return nil, fmt.Errorf("chain: could not read the simulation: %w", err)
		}
		data.ResourceFee += data.ResourceFee / 5 // headroom for small changes between simulation and inclusion
		var auth []xdr.SorobanAuthorizationEntry
		if len(sim.Results) > 0 && sim.Results[0].AuthXDR != nil {
			for _, raw := range *sim.Results[0].AuthXDR {
				var entry xdr.SorobanAuthorizationEntry
				if err := xdr.SafeUnmarshalBase64(raw, &entry); err != nil {
					return nil, fmt.Errorf("chain: could not read the authorization: %w", err)
				}
				auth = append(auth, entry)
			}
		}
		final, err := buildInvoke(source, c.cfg.Passphrase, contract, fn, args, auth, &data)
		if err != nil {
			return nil, err
		}
		return c.submit(ctx, final)
	}
	return nil, errors.New("chain: the data could not be restored; try again later")
}

func (c *Client) restore(ctx context.Context, pre *protocol.RestorePreamble) error {
	var data xdr.SorobanTransactionData
	if err := xdr.SafeUnmarshalBase64(pre.TransactionDataXDR, &data); err != nil {
		return fmt.Errorf("chain: could not read the restore data: %w", err)
	}
	data.ResourceFee += xdr.Int64(pre.MinResourceFee)
	source, err := c.rpc.LoadAccount(ctx, c.kp.Address())
	if err != nil {
		return err
	}
	op := &txnbuild.RestoreFootprint{
		SourceAccount: c.kp.Address(),
		Ext:           xdr.TransactionExt{V: 1, SorobanData: &data},
	}
	tx, err := txnbuild.NewTransaction(txnbuild.TransactionParams{
		SourceAccount: fresh(source), IncrementSequenceNum: true, Operations: []txnbuild.Operation{op},
		BaseFee: 100_000, Preconditions: txnbuild.Preconditions{TimeBounds: txnbuild.NewTimeout(300)},
	})
	if err != nil {
		return err
	}
	_, err = c.submit(ctx, tx)
	return err
}

// fresh copies an account so that building a transaction, which advances the sequence number,
// does not affect the next build.
func fresh(a txnbuild.Account) txnbuild.Account {
	seq, _ := a.GetSequenceNumber()
	return &txnbuild.SimpleAccount{AccountID: a.GetAccountID(), Sequence: seq}
}

func buildInvoke(source txnbuild.Account, passphrase, contract, fn string, args []xdr.ScVal, auth []xdr.SorobanAuthorizationEntry, data *xdr.SorobanTransactionData) (*txnbuild.Transaction, error) {
	raw, err := strkey.Decode(strkey.VersionByteContract, contract)
	if err != nil {
		return nil, fmt.Errorf("chain: bad contract address: %w", err)
	}
	var id xdr.ContractId
	copy(id[:], raw)
	op := &txnbuild.InvokeHostFunction{
		HostFunction: xdr.HostFunction{
			Type: xdr.HostFunctionTypeHostFunctionTypeInvokeContract,
			InvokeContract: &xdr.InvokeContractArgs{
				ContractAddress: xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeContract, ContractId: &id},
				FunctionName:    xdr.ScSymbol(fn),
				Args:            args,
			},
		},
		Auth:          auth,
		SourceAccount: source.GetAccountID(),
	}
	if data != nil {
		op.Ext = xdr.TransactionExt{V: 1, SorobanData: data}
	}
	return txnbuild.NewTransaction(txnbuild.TransactionParams{
		SourceAccount: fresh(source), IncrementSequenceNum: true, Operations: []txnbuild.Operation{op},
		BaseFee: 100_000, Preconditions: txnbuild.Preconditions{TimeBounds: txnbuild.NewTimeout(300)},
	})
}

func (c *Client) simulate(ctx context.Context, tx *txnbuild.Transaction) (*protocol.SimulateTransactionResponse, error) {
	b64, err := tx.Base64()
	if err != nil {
		return nil, err
	}
	sim, err := c.rpc.SimulateTransaction(ctx, protocol.SimulateTransactionRequest{Transaction: b64})
	if err != nil {
		return nil, fmt.Errorf("chain: the network could not be reached: %w", err)
	}
	if sim.Error != "" {
		return nil, fmt.Errorf("chain: the contract rejected the call: %s", explain(sim.Error))
	}
	return &sim, nil
}

func (c *Client) submit(ctx context.Context, tx *txnbuild.Transaction) (*Result, error) {
	signed, err := tx.Sign(c.cfg.Passphrase, c.kp)
	if err != nil {
		return nil, err
	}
	b64, err := signed.Base64()
	if err != nil {
		return nil, err
	}
	sent, err := c.rpc.SendTransaction(ctx, protocol.SendTransactionRequest{Transaction: b64})
	if err != nil {
		return nil, fmt.Errorf("chain: the transaction could not be sent: %w", err)
	}
	if sent.Status == "ERROR" {
		return nil, fmt.Errorf("chain: the network refused the transaction (%s)", sent.ErrorResultXDR)
	}
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		got, err := c.rpc.GetTransaction(ctx, protocol.GetTransactionRequest{Hash: sent.Hash})
		if err == nil {
			switch got.Status {
			case protocol.TransactionStatusSuccess:
				return &Result{TxHash: sent.Hash, Ledger: got.Ledger}, nil
			case protocol.TransactionStatusFailed:
				return nil, fmt.Errorf("chain: the transaction %s failed on the network", sent.Hash)
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return nil, fmt.Errorf("chain: the transaction %s was not confirmed in time; it may still complete", sent.Hash)
}

// explain turns the contracts' numeric error codes into words where it can.
func explain(msg string) string {
	known := map[string]string{
		"Error(Contract, #1)": "a numeric rule of the contract was broken (see the contract's documentation)",
	}
	for code, text := range known {
		if strings.Contains(msg, code) {
			return text + ": " + msg
		}
	}
	return msg
}

// --- argument encoding ---

type argFn func() (xdr.ScVal, error)

func argList(fns ...argFn) ([]xdr.ScVal, error) {
	out := make([]xdr.ScVal, 0, len(fns))
	for _, fn := range fns {
		v, err := fn()
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func address(addr string) argFn {
	return func() (xdr.ScVal, error) {
		id, err := xdr.AddressToAccountId(addr)
		if err != nil {
			return xdr.ScVal{}, fmt.Errorf("chain: %q is not a valid Stellar address", addr)
		}
		sc := xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeAccount, AccountId: &id}
		return xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &sc}, nil
	}
}

func bytes32(hexString string) argFn {
	return func() (xdr.ScVal, error) {
		raw, err := hex.DecodeString(hexString)
		if err != nil || len(raw) != 32 {
			return xdr.ScVal{}, errors.New("chain: expected a 64-character hex hash")
		}
		b := xdr.ScBytes(raw)
		return xdr.ScVal{Type: xdr.ScValTypeScvBytes, Bytes: &b}, nil
	}
}

func u32(n int) argFn {
	return func() (xdr.ScVal, error) {
		if n < 0 || int64(n) > int64(^uint32(0)) {
			return xdr.ScVal{}, errors.New("chain: number out of range")
		}
		v := xdr.Uint32(n)
		return xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: &v}, nil
	}
}

func u64(n uint64) argFn {
	return func() (xdr.ScVal, error) {
		v := xdr.Uint64(n)
		return xdr.ScVal{Type: xdr.ScValTypeScvU64, U64: &v}, nil
	}
}
