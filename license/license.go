// Package license is the permission to consult a capsule: a small file, signed by the expert,
// that says who may use which capsule, for what, until when, and how many times.
//
// A license is verified offline, from the file alone. Deciding whether a particular request is
// allowed (Check) is a pure function of the license, the capsule, the list of revocations and
// the request, so any program can reproduce the answer.
package license

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Synapse467/synapse-core/canonical"
	"github.com/Synapse467/synapse-core/identity"
)

// Format is the value of a license's "format" field.
const Format = "synapse.license/1"

// Domain labels signatures made over a license hash.
const Domain = "synapse.license/1"

// MaxFileBytes bounds how much Parse reads.
const MaxFileBytes = 1 << 20

// CapsuleRef says which capsule, and which versions of it, a license covers.
type CapsuleRef struct {
	Owner      string `json:"owner"`
	Slug       string `json:"slug"`
	MinVersion int    `json:"minVersion,omitempty"` // 0 means any
	MaxVersion int    `json:"maxVersion,omitempty"` // 0 means any
	Hash       string `json:"hash,omitempty"`       // pins one exact version when set
}

// Terms is everything the signature covers.
type Terms struct {
	ID         string     `json:"id"`
	Capsule    CapsuleRef `json:"capsule"`
	Grantor    string     `json:"grantor"`
	Grantee    string     `json:"grantee"`
	Purposes   []string   `json:"purposes"`
	Commercial bool       `json:"commercial"`
	AITraining bool       `json:"aiTraining"`
	Derivative bool       `json:"derivative"`
	NotBefore  string     `json:"notBefore,omitempty"`
	ExpiresAt  string     `json:"expiresAt,omitempty"`
	MaxQueries int        `json:"maxQueries,omitempty"` // 0 means unlimited
	Note       string     `json:"note,omitempty"`
}

// License is the file format.
type License struct {
	Format    string `json:"format"`
	Terms     Terms  `json:"terms"`
	Hash      string `json:"hash"`
	Signature string `json:"signature"`
}

// NewID returns a random 128-bit license ID.
func NewID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("license: no randomness available: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// Issue validates terms and signs them as the grantor. Only a capsule's owner can license it.
func Issue(t Terms, grantor *identity.Identity) (*License, error) {
	if t.ID == "" {
		t.ID = NewID()
	}
	if t.Grantor == "" {
		t.Grantor = grantor.Address()
	}
	if grantor.Address() != t.Grantor {
		return nil, errors.New("license: the signing identity is not the grantor")
	}
	if t.Capsule.Owner != t.Grantor {
		return nil, errors.New("license: only the owner of a capsule can license it")
	}
	if err := validateTerms(t); err != nil {
		return nil, err
	}
	hash, err := canonical.Hash(t)
	if err != nil {
		return nil, err
	}
	signature, err := grantor.Sign(Domain, []byte(hash))
	if err != nil {
		return nil, err
	}
	return &License{Format: Format, Terms: t, Hash: hash, Signature: base64.StdEncoding.EncodeToString(signature)}, nil
}

func validateTerms(t Terms) error {
	switch {
	case t.Capsule.Slug == "" || t.Capsule.Owner == "":
		return errors.New("license: name the capsule's owner and slug")
	case t.Grantee == "":
		return errors.New("license: name the grantee")
	case len(t.Purposes) == 0:
		return errors.New("license: list at least one purpose (or \"*\" for any)")
	case t.MaxQueries < 0:
		return errors.New("license: maxQueries cannot be negative")
	case t.Capsule.MinVersion < 0 || t.Capsule.MaxVersion < 0 ||
		(t.Capsule.MaxVersion != 0 && t.Capsule.MinVersion > t.Capsule.MaxVersion):
		return errors.New("license: the version range is invalid")
	}
	for _, purpose := range t.Purposes {
		if strings.TrimSpace(purpose) == "" {
			return errors.New("license: a purpose cannot be empty")
		}
	}
	var notBefore, expires time.Time
	var err error
	if t.NotBefore != "" {
		if notBefore, err = time.Parse(time.RFC3339, t.NotBefore); err != nil {
			return errors.New("license: notBefore must be an RFC 3339 time")
		}
	}
	if t.ExpiresAt != "" {
		if expires, err = time.Parse(time.RFC3339, t.ExpiresAt); err != nil {
			return errors.New("license: expiresAt must be an RFC 3339 time")
		}
		if !notBefore.IsZero() && !expires.After(notBefore) {
			return errors.New("license: expiresAt must be after notBefore")
		}
	}
	return nil
}

// Verify checks the license's own integrity: its format, its hash, its terms and the grantor's
// signature. It says nothing about whether a request is allowed; that is Check.
func (l *License) Verify() error {
	if l.Format != Format {
		return fmt.Errorf("license: unsupported format %q", l.Format)
	}
	if err := validateTerms(l.Terms); err != nil {
		return err
	}
	if l.Terms.Capsule.Owner != l.Terms.Grantor {
		return errors.New("license: the grantor is not the capsule's owner")
	}
	hash, err := canonical.Hash(l.Terms)
	if err != nil {
		return err
	}
	if hash != l.Hash {
		return errors.New("license: the terms do not match the hash; the license has been changed")
	}
	raw, err := base64.StdEncoding.DecodeString(l.Signature)
	if err != nil {
		return errors.New("license: the signature is not valid base64")
	}
	if err := identity.Verify(l.Terms.Grantor, Domain, []byte(l.Hash), raw); err != nil {
		return errors.New("license: the grantor's signature is not valid")
	}
	return nil
}

// Ref returns the 32-byte reference (hex) under which a license is recorded on-chain.
func (l *License) Ref() string { return RefOf(l.Terms.ID) }

// RefOf returns the on-chain reference for a license ID. Usage logs record IDs, so this is how a
// log entry is matched to the chain.
func RefOf(id string) string {
	sum := sha256.Sum256([]byte("synapse.license\n" + id))
	return hex.EncodeToString(sum[:])
}

// Parse decodes a license strictly.
func Parse(data []byte) (*License, error) {
	if len(data) > MaxFileBytes {
		return nil, errors.New("license: file is too large")
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	var l License
	if err := decoder.Decode(&l); err != nil {
		return nil, fmt.Errorf("license: not a valid license file: %w", err)
	}
	if decoder.More() {
		return nil, errors.New("license: unexpected data after the license")
	}
	return &l, nil
}

// Load reads and parses a license file.
func Load(path string) (*License, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Save writes the license as indented JSON.
func (l *License) Save(path string) error {
	data, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
