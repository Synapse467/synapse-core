// Package capsule defines the Expertise Capsule: one signed, versioned file holding approved
// knowledge, where each piece came from, who contributed it, and the proof that it was
// evaluated before it was published.
//
// A capsule is verified, not trusted. Anyone with the file can recompute its hash, check every
// signature, check that each citation's quote matches its recorded hash, and check that a
// version continues the one before it. No server, account or network is involved.
package capsule

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/Synapse467/synapse-core/canonical"
	"github.com/Synapse467/synapse-core/identity"
)

// Format is the value of a capsule's "format" field.
const Format = "synapse.capsule/1"

// SignDomain labels signatures made over a capsule hash.
const SignDomain = "synapse.capsule/1"

// MaxFileBytes bounds how much Parse reads, so a hostile file cannot exhaust memory.
const MaxFileBytes = 64 << 20

var (
	slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	hexPattern  = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Capsule is the file format.
type Capsule struct {
	Format     string      `json:"format"`
	Manifest   Manifest    `json:"manifest"`
	Hash       string      `json:"hash"`
	Signatures []Signature `json:"signatures"`
}

// Manifest is everything the hash and the signatures cover.
type Manifest struct {
	Slug         string        `json:"slug"`
	Title        string        `json:"title"`
	Domain       string        `json:"domain"`
	Scope        string        `json:"scope"`
	Version      int           `json:"version"`
	Previous     string        `json:"previous,omitempty"`
	Owner        string        `json:"owner"`
	Contributors []Contributor `json:"contributors,omitempty"`
	CreatedAt    string        `json:"createdAt"`
	Knowledge    []Item        `json:"knowledge"`
	Sources      []Source      `json:"sources,omitempty"`
	Evaluation   Evaluation    `json:"evaluation"`
	Policy       Policy        `json:"policy"`
}

// Contributor is a person who contributed knowledge, identified by a Stellar address.
type Contributor struct {
	Address string `json:"address"`
	Name    string `json:"name,omitempty"`
}

// Source describes an original document the knowledge came from. Only its hash is recorded,
// never its contents: the private source stays with its owner. Citations carry the short quotes
// that support each item.
type Source struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	SHA256 string `json:"sha256"`
	Bytes  int    `json:"bytes"`
}

// Evaluation is the proof that a capsule was tested before it was published. Scores are in basis
// points (10000 = 100%), because the canonical form has no floating-point numbers.
type Evaluation struct {
	SuiteHash          string `json:"suiteHash"`
	Cases              int    `json:"cases"`
	Passed             bool   `json:"passed"`
	CitationValidityBP int    `json:"citationValidityBp"`
	CoverageBP         int    `json:"coverageBp"`
	AbstentionBP       int    `json:"abstentionBp"`
	AttributionBP      int    `json:"attributionBp"`
}

// Policy is the default licensing posture published with a capsule.
type Policy struct {
	// Open lets anyone consult the capsule, for the listed purposes, without a license.
	Open       bool     `json:"open"`
	Purposes   []string `json:"purposes,omitempty"`
	Commercial bool     `json:"commercial"`
	AITraining bool     `json:"aiTraining"`
	Derivative bool     `json:"derivative"`
}

// Signature is a signer's signature over the capsule hash.
type Signature struct {
	Signer    string `json:"signer"`
	Role      string `json:"role"` // "owner" or "contributor"
	Signature string `json:"signature"`
}

// Hash computes the hash of a manifest: SHA-256 over its canonical JSON.
func (m Manifest) Hash() (string, error) { return canonical.Hash(m) }

// Ref returns the 32-byte reference (hex) under which a capsule is recorded on-chain. It binds
// the owner and the slug, so two owners can use the same slug without colliding.
func Ref(owner, slug string) string {
	sum := sha256.Sum256([]byte("synapse.capsule\n" + owner + "\n" + slug))
	return hex.EncodeToString(sum[:])
}

// Seal validates a manifest, computes its hash and signs it as the owner.
func Seal(m Manifest, owner *identity.Identity) (*Capsule, error) {
	if owner.Address() != m.Owner {
		return nil, errors.New("capsule: the signing identity is not the manifest owner")
	}
	if issues := validateManifest(m); len(issues) > 0 {
		return nil, fmt.Errorf("capsule: invalid manifest: %s", issues[0])
	}
	hash, err := m.Hash()
	if err != nil {
		return nil, err
	}
	c := &Capsule{Format: Format, Manifest: m, Hash: hash}
	if err := c.sign(owner, "owner"); err != nil {
		return nil, err
	}
	return c, nil
}

// CoSign adds a contributor's signature. The signer must be listed as a contributor.
func (c *Capsule) CoSign(signer *identity.Identity) error {
	listed := false
	for _, contributor := range c.Manifest.Contributors {
		if contributor.Address == signer.Address() {
			listed = true
		}
	}
	if !listed {
		return errors.New("capsule: the signer is not a listed contributor")
	}
	for _, existing := range c.Signatures {
		if existing.Signer == signer.Address() {
			return errors.New("capsule: this contributor has already signed")
		}
	}
	return c.sign(signer, "contributor")
}

func (c *Capsule) sign(signer *identity.Identity, role string) error {
	signature, err := signer.Sign(SignDomain, []byte(c.Hash))
	if err != nil {
		return err
	}
	c.Signatures = append(c.Signatures, Signature{
		Signer:    signer.Address(),
		Role:      role,
		Signature: base64.StdEncoding.EncodeToString(signature),
	})
	return nil
}

// Item returns the knowledge item with the given ID.
func (c *Capsule) Item(id string) (Item, bool) {
	for _, item := range c.Manifest.Knowledge {
		if item.ID == id {
			return item, true
		}
	}
	return Item{}, false
}

// Parse decodes a capsule strictly: unknown fields are an error, so a typo or a hidden field
// cannot slip past a reader.
func Parse(data []byte) (*Capsule, error) {
	if len(data) > MaxFileBytes {
		return nil, errors.New("capsule: file is too large")
	}
	decoder := json.NewDecoder(bytesReader(data))
	decoder.DisallowUnknownFields()
	var c Capsule
	if err := decoder.Decode(&c); err != nil {
		return nil, fmt.Errorf("capsule: not a valid capsule file: %w", err)
	}
	if decoder.More() {
		return nil, errors.New("capsule: unexpected data after the capsule")
	}
	return &c, nil
}

// Load reads and parses a capsule file.
func Load(path string) (*Capsule, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := readLimited(file, MaxFileBytes)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Save writes the capsule as indented JSON.
func (c *Capsule) Save(path string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// Now returns the current time in the format a manifest uses.
func Now() string { return time.Now().UTC().Format(time.RFC3339) }
