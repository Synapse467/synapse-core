package chain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/keypair"
)

func TestTestnetDefaultsAreValid(t *testing.T) {
	if err := Testnet().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejectsIncompleteConfigs(t *testing.T) {
	good := Testnet()
	cases := map[string]func(*Config){
		"no rpc":            func(c *Config) { c.RPCURL = "" },
		"no passphrase":     func(c *Config) { c.Passphrase = "" },
		"bad anchor":        func(c *Config) { c.Anchor = "nope" },
		"account as ledger": func(c *Config) { c.License = keypair.MustRandom().Address() },
		"no usage":          func(c *Config) { c.Usage = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c := good
			mutate(&c)
			if c.Validate() == nil {
				t.Fatal("an invalid config was accepted")
			}
		})
	}
}

func TestNewNeedsAnIdentity(t *testing.T) {
	if _, err := New(Testnet(), nil); err == nil {
		t.Fatal("a client without an identity was created")
	}
}

func TestArgumentEncodingRejectsBadInput(t *testing.T) {
	if _, err := bytes32("abc")(); err == nil {
		t.Fatal("a short hash was accepted")
	}
	if _, err := bytes32(strings.Repeat("zz", 32))(); err == nil {
		t.Fatal("non-hex was accepted")
	}
	if _, err := address("not-an-address")(); err == nil {
		t.Fatal("a bad address was accepted")
	}
	if _, err := u32(-1)(); err == nil {
		t.Fatal("a negative number was accepted")
	}
	if _, err := bytes32(strings.Repeat("ab", 32))(); err != nil {
		t.Fatal(err)
	}
}

func TestWritesRejectBadArgumentsBeforeTouchingTheNetwork(t *testing.T) {
	c, err := New(Config{
		RPCURL: "http://127.0.0.1:1", Passphrase: "x", Anchor: Testnet().Anchor, License: Testnet().License, Usage: Testnet().Usage,
	}, keypair.MustRandom())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Anchor(context.Background(), "short", 1, strings.Repeat("a", 64), strings.Repeat("0", 64)); err == nil {
		t.Fatal("a bad capsule reference was accepted")
	}
	if _, err := c.Grant(context.Background(), strings.Repeat("a", 64), strings.Repeat("b", 64), "bad", strings.Repeat("c", 64), 0); err == nil {
		t.Fatal("a bad grantee was accepted")
	}
	if _, err := c.Grant(context.Background(), strings.Repeat("a", 64), strings.Repeat("b", 64), keypair.MustRandom().Address(), strings.Repeat("c", 64), -5); err == nil {
		t.Fatal("a negative expiry was accepted")
	}
}

func h(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// TestLiveTestnet talks to the real Testnet deployment. It is skipped unless SYNAPSE_LIVE_TESTNET=1,
// because it needs the network and creates a funded account.
func TestLiveTestnet(t *testing.T) {
	if os.Getenv("SYNAPSE_LIVE_TESTNET") != "1" {
		t.Skip("set SYNAPSE_LIVE_TESTNET=1 to run against the public Testnet")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	kp := keypair.MustRandom()
	c, err := New(Testnet(), kp)
	if err != nil {
		t.Fatal(err)
	}
	ref := h("capsule-" + kp.Address())
	m1 := h("manifest-1-" + kp.Address())
	zero := strings.Repeat("0", 64)

	if n, err := c.LatestAnchor(ctx, kp.Address(), ref); err != nil || n != 0 {
		t.Fatalf("a new capsule should have no anchors: %d %v", n, err)
	}
	res, err := c.Anchor(ctx, ref, 1, m1, zero)
	if err != nil {
		t.Fatalf("anchor: %v", err)
	}
	t.Logf("anchored in %s", res.TxHash)
	if n, _ := c.LatestAnchor(ctx, kp.Address(), ref); n != 1 {
		t.Fatalf("latest = %d", n)
	}
	got, ok, err := c.AnchoredHash(ctx, kp.Address(), ref, 1)
	if err != nil || !ok || got != m1 {
		t.Fatalf("anchored hash = %q %v %v", got, ok, err)
	}
	if _, err := c.Anchor(ctx, ref, 1, m1, zero); err == nil {
		t.Fatal("anchoring the same version twice must fail")
	}

	grantee := keypair.MustRandom().Address()
	lic := h("license-" + kp.Address())
	if _, err := c.Grant(ctx, lic, ref, grantee, h("terms"), 0); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if active, err := c.LicenseActive(ctx, kp.Address(), lic); err != nil || !active {
		t.Fatalf("license should be active: %v %v", active, err)
	}
	if _, err := c.RecordUsage(ctx, Receipt{LicenseRef: lic, Seq: 1, BatchHash: h("batch"), PreviousHex: zero, Count: 3, PeriodStart: 1, PeriodEnd: 2}); err != nil {
		t.Fatalf("usage: %v", err)
	}
	if _, err := c.Revoke(ctx, lic); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if active, _ := c.LicenseActive(ctx, kp.Address(), lic); active {
		t.Fatal("a revoked license must not be active")
	}
}
