// Package identity is a Stellar keypair used to sign capsules, licenses and requests.
//
// Nothing needs to be set up. LoadOrCreate generates a keypair the first time it is called and
// keeps it in the user's Synapse directory, readable only by that user. The public half is an
// ordinary Stellar address (G...), so the same identity can also anchor to the network.
package identity

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/stellar/go-stellar-sdk/keypair"

	"github.com/Synapse467/synapse-core/home"
)

// Identity holds a Stellar signing key.
type Identity struct {
	key *keypair.Full
}

type stored struct {
	Version int    `json:"version"`
	Address string `json:"address"`
	Seed    string `json:"seed"`
}

// Generate creates a new random identity.
func Generate() (*Identity, error) {
	key, err := keypair.Random()
	if err != nil {
		return nil, err
	}
	return &Identity{key: key}, nil
}

// FromSeed restores an identity from a Stellar secret seed (S...).
func FromSeed(seed string) (*Identity, error) {
	key, err := keypair.ParseFull(seed)
	if err != nil {
		return nil, errors.New("identity: not a valid Stellar secret seed")
	}
	return &Identity{key: key}, nil
}

// Address returns the public Stellar address (G...).
func (i *Identity) Address() string { return i.key.Address() }

// Seed returns the secret seed (S...). Treat it like a password.
func (i *Identity) Seed() string { return i.key.Seed() }

// Keypair exposes the underlying key for Stellar transaction signing.
func (i *Identity) Keypair() *keypair.Full { return i.key }

// Sign signs payload under a domain label, so a signature made for one purpose can never be
// replayed as another. The signed bytes are the domain, a newline, then the payload.
func (i *Identity) Sign(domain string, payload []byte) ([]byte, error) {
	return i.key.Sign(message(domain, payload))
}

// Verify checks a signature made by Sign for the given Stellar address.
func Verify(address, domain string, payload, signature []byte) error {
	key, err := keypair.ParseAddress(address)
	if err != nil {
		return fmt.Errorf("identity: %q is not a valid Stellar address", address)
	}
	if err := key.Verify(message(domain, payload), signature); err != nil {
		return errors.New("identity: signature does not match")
	}
	return nil
}

func message(domain string, payload []byte) []byte {
	return append([]byte(domain+"\n"), payload...)
}

// DefaultPath is where the local identity is kept.
func DefaultPath() string { return filepath.Join(home.Dir(), "identity.json") }

// LoadOrCreate returns the identity stored at path, creating one if the file does not exist.
// created reports whether a new key was generated.
func LoadOrCreate(path string) (id *Identity, created bool, err error) {
	data, readErr := os.ReadFile(path)
	if readErr == nil {
		var s stored
		if err := json.Unmarshal(data, &s); err != nil {
			return nil, false, fmt.Errorf("identity: %s is not a valid identity file", path)
		}
		id, err := FromSeed(s.Seed)
		if err != nil {
			return nil, false, err
		}
		if s.Address != "" && s.Address != id.Address() {
			return nil, false, fmt.Errorf("identity: %s has an address that does not match its seed", path)
		}
		return id, false, nil
	}
	if !errors.Is(readErr, os.ErrNotExist) {
		return nil, false, readErr
	}
	id, err = Generate()
	if err != nil {
		return nil, false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, false, err
	}
	data, err = json.MarshalIndent(stored{Version: 1, Address: id.Address(), Seed: id.Seed()}, "", "  ")
	if err != nil {
		return nil, false, err
	}
	// O_EXCL, so two processes starting together cannot overwrite each other's new key.
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return LoadOrCreate(path)
		}
		return nil, false, err
	}
	defer file.Close()
	if _, err := file.Write(append(data, '\n')); err != nil {
		return nil, false, err
	}
	return id, true, nil
}
