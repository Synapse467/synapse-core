package identity

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSignAndVerify(t *testing.T) {
	id, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(id.Address(), "G") || !strings.HasPrefix(id.Seed(), "S") {
		t.Fatalf("unexpected key shapes %q %q", id.Address(), id.Seed())
	}
	signature, err := id.Sign("synapse.test/1", []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(id.Address(), "synapse.test/1", []byte("hello"), signature); err != nil {
		t.Fatal(err)
	}
}

func TestSignaturesAreBoundToDomainPayloadAndSigner(t *testing.T) {
	alice, _ := Generate()
	bob, _ := Generate()
	signature, _ := alice.Sign("synapse.license/1", []byte("payload"))
	cases := map[string]error{
		"another domain":    Verify(alice.Address(), "synapse.capsule/1", []byte("payload"), signature),
		"another payload":   Verify(alice.Address(), "synapse.license/1", []byte("payload!"), signature),
		"another signer":    Verify(bob.Address(), "synapse.license/1", []byte("payload"), signature),
		"a bad address":     Verify("not-an-address", "synapse.license/1", []byte("payload"), signature),
		"a short signature": Verify(alice.Address(), "synapse.license/1", []byte("payload"), signature[:10]),
	}
	for name, err := range cases {
		if err == nil {
			t.Errorf("a signature verified against %s", name)
		}
	}
}

func TestFromSeedRestoresTheSameIdentity(t *testing.T) {
	original, _ := Generate()
	restored, err := FromSeed(original.Seed())
	if err != nil {
		t.Fatal(err)
	}
	if restored.Address() != original.Address() {
		t.Fatal("restored identity has a different address")
	}
	if _, err := FromSeed("SNOTASEED"); err == nil {
		t.Fatal("expected an error for an invalid seed")
	}
	if _, err := FromSeed(original.Address()); err == nil {
		t.Fatal("a public address must not be accepted as a seed")
	}
}

func TestLoadOrCreateGeneratesOnFirstUseAndReusesAfter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "identity.json")
	first, created, err := LoadOrCreate(path)
	if err != nil || !created {
		t.Fatalf("first call: created=%v err=%v", created, err)
	}
	second, created, err := LoadOrCreate(path)
	if err != nil || created {
		t.Fatalf("second call: created=%v err=%v", created, err)
	}
	if first.Address() != second.Address() {
		t.Fatal("the identity changed between calls")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("identity file mode is %v, want 0600", info.Mode().Perm())
		}
	}
}

func TestLoadOrCreateRejectsCorruptOrMismatchedFiles(t *testing.T) {
	dir := t.TempDir()
	corrupt := filepath.Join(dir, "corrupt.json")
	os.WriteFile(corrupt, []byte("not json"), 0o600)
	if _, _, err := LoadOrCreate(corrupt); err == nil {
		t.Fatal("expected an error for a corrupt file")
	}
	other, _ := Generate()
	real, _ := Generate()
	mismatch := filepath.Join(dir, "mismatch.json")
	os.WriteFile(mismatch, []byte(`{"version":1,"address":"`+other.Address()+`","seed":"`+real.Seed()+`"}`), 0o600)
	if _, _, err := LoadOrCreate(mismatch); err == nil {
		t.Fatal("expected an error when the address does not match the seed")
	}
	// A bad file must never be silently replaced with a new key.
	data, _ := os.ReadFile(corrupt)
	if string(data) != "not json" {
		t.Fatal("a corrupt identity file was overwritten")
	}
}
