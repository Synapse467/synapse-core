package license

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/Synapse467/synapse-core/canonical"
	"github.com/Synapse467/synapse-core/identity"
)

// RevocationFormat is the value of a revocation's "format" field.
const RevocationFormat = "synapse.revocation/1"

// RevocationDomain labels signatures made over a revocation.
const RevocationDomain = "synapse.revocation/1"

// Revocation is the grantor's signed statement that a license is withdrawn. It takes effect
// immediately for anyone who has it, and can also be recorded on-chain so everyone sees it.
type Revocation struct {
	Format    string `json:"format"`
	License   string `json:"license"` // the license's terms ID
	Grantor   string `json:"grantor"`
	RevokedAt string `json:"revokedAt"`
	Signature string `json:"signature"`
}

type revocationBody struct {
	Format    string `json:"format"`
	License   string `json:"license"`
	Grantor   string `json:"grantor"`
	RevokedAt string `json:"revokedAt"`
}

func (r Revocation) body() revocationBody {
	return revocationBody{Format: r.Format, License: r.License, Grantor: r.Grantor, RevokedAt: r.RevokedAt}
}

func (r Revocation) digest() ([]byte, error) {
	hash, err := canonical.Hash(r.body())
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(hash))
	return []byte(hex.EncodeToString(sum[:])), nil
}

// Revoke signs a revocation for a license. Only the license's grantor can revoke it.
func Revoke(l *License, grantor *identity.Identity, at time.Time) (*Revocation, error) {
	if grantor.Address() != l.Terms.Grantor {
		return nil, errors.New("license: only the grantor can revoke a license")
	}
	r := &Revocation{Format: RevocationFormat, License: l.Terms.ID, Grantor: grantor.Address(), RevokedAt: at.UTC().Format(time.RFC3339)}
	payload, err := r.digest()
	if err != nil {
		return nil, err
	}
	signature, err := grantor.Sign(RevocationDomain, payload)
	if err != nil {
		return nil, err
	}
	r.Signature = base64.StdEncoding.EncodeToString(signature)
	return r, nil
}

// Verify checks the revocation's signature.
func (r Revocation) Verify() error {
	if r.Format != RevocationFormat {
		return errors.New("license: unsupported revocation format")
	}
	if _, err := time.Parse(time.RFC3339, r.RevokedAt); err != nil {
		return errors.New("license: revokedAt must be an RFC 3339 time")
	}
	payload, err := r.digest()
	if err != nil {
		return err
	}
	raw, err := base64.StdEncoding.DecodeString(r.Signature)
	if err != nil {
		return errors.New("license: the revocation signature is not valid base64")
	}
	if err := identity.Verify(r.Grantor, RevocationDomain, payload, raw); err != nil {
		return errors.New("license: the revocation signature is not valid")
	}
	return nil
}

// RevocationSet holds verified revocations, keyed by license ID. The zero value (nil) is a valid
// empty set.
type RevocationSet map[string]Revocation

// Add stores a revocation if its signature is valid. An invalid one is rejected, so a forged
// revocation can never block anyone.
func (s RevocationSet) Add(r Revocation) error {
	if err := r.Verify(); err != nil {
		return err
	}
	s[r.License] = r
	return nil
}

// Revoked reports whether l has been revoked by its own grantor. A revocation signed by anyone
// else does not count, even if its license ID matches.
func (s RevocationSet) Revoked(l *License) bool {
	r, ok := s[l.Terms.ID]
	return ok && r.Grantor == l.Terms.Grantor && r.Verify() == nil
}

// LoadRevocations reads a list of revocations, ignoring any whose signature is not valid. A
// missing file is an empty set.
func LoadRevocations(path string) (RevocationSet, error) {
	set := RevocationSet{}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return set, nil
	}
	if err != nil {
		return nil, err
	}
	var list []Revocation
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, errors.New("license: the revocation list is not valid JSON")
	}
	for _, r := range list {
		_ = set.Add(r)
	}
	return set, nil
}

// Save writes the set as a list.
func (s RevocationSet) Save(path string) error {
	list := make([]Revocation, 0, len(s))
	for _, r := range s {
		list = append(list, r)
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
