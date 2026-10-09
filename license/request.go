package license

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/Synapse467/synapse-core/canonical"
	"github.com/Synapse467/synapse-core/identity"
)

// RequestFormat is the value of a signed request's "format" field.
const RequestFormat = "synapse.request/1"

// RequestDomain labels signatures made over a request.
const RequestDomain = "synapse.request/1"

// SignedRequest is a question, signed by the person asking. A gateway that holds a capsule uses
// it to know the question really comes from the licensee, with no account and no password.
// The question itself is signed but only its hash is ever logged.
type SignedRequest struct {
	Format    string `json:"format"`
	Capsule   string `json:"capsule"`           // hash of the capsule being asked
	License   string `json:"license,omitempty"` // hash of the license being used, if any
	Purpose   string `json:"purpose"`
	Question  string `json:"question"`
	Nonce     string `json:"nonce"`
	At        string `json:"at"`
	Grantee   string `json:"grantee"`
	Signature string `json:"signature"`
}

type requestBody struct {
	Format   string `json:"format"`
	Capsule  string `json:"capsule"`
	License  string `json:"license,omitempty"`
	Purpose  string `json:"purpose"`
	Question string `json:"question"`
	Nonce    string `json:"nonce"`
	At       string `json:"at"`
	Grantee  string `json:"grantee"`
}

func (r SignedRequest) digest() ([]byte, error) {
	hash, err := canonical.Hash(requestBody{r.Format, r.Capsule, r.License, r.Purpose, r.Question, r.Nonce, r.At, r.Grantee})
	if err != nil {
		return nil, err
	}
	return []byte(hash), nil
}

// QuestionHash returns the hash that usage logs record instead of the question.
func (r SignedRequest) QuestionHash() string {
	sum := sha256.Sum256([]byte(r.Question))
	return hex.EncodeToString(sum[:])
}

// NewRequest signs a question as the grantee.
func NewRequest(grantee *identity.Identity, capsuleHash, licenseHash, purpose, question string, now time.Time) (*SignedRequest, error) {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	r := &SignedRequest{
		Format: RequestFormat, Capsule: capsuleHash, License: licenseHash, Purpose: purpose, Question: question,
		Nonce: hex.EncodeToString(nonce), At: now.UTC().Format(time.RFC3339), Grantee: grantee.Address(),
	}
	payload, err := r.digest()
	if err != nil {
		return nil, err
	}
	signature, err := grantee.Sign(RequestDomain, payload)
	if err != nil {
		return nil, err
	}
	r.Signature = base64.StdEncoding.EncodeToString(signature)
	return r, nil
}

// Verify checks the signature and that the request is recent (within maxAge either side of now),
// so an old captured request cannot be replayed later.
func (r SignedRequest) Verify(now time.Time, maxAge time.Duration) error {
	if r.Format != RequestFormat {
		return errors.New("license: unsupported request format")
	}
	at, err := time.Parse(time.RFC3339, r.At)
	if err != nil {
		return errors.New("license: the request time is not valid")
	}
	if age := now.Sub(at); age > maxAge || age < -maxAge {
		return errors.New("license: the request is too old or too far in the future")
	}
	payload, err := r.digest()
	if err != nil {
		return err
	}
	raw, err := base64.StdEncoding.DecodeString(r.Signature)
	if err != nil {
		return errors.New("license: the request signature is not valid base64")
	}
	if err := identity.Verify(r.Grantee, RequestDomain, payload, raw); err != nil {
		return errors.New("license: the request signature is not valid")
	}
	return nil
}

// ReplayGuard remembers recent request nonces, so a captured request cannot be sent twice.
type ReplayGuard struct {
	mu   sync.Mutex
	ttl  time.Duration
	seen map[string]time.Time
}

// NewReplayGuard remembers nonces for ttl, which should be at least the maxAge used in Verify.
func NewReplayGuard(ttl time.Duration) *ReplayGuard {
	return &ReplayGuard{ttl: ttl, seen: map[string]time.Time{}}
}

// FirstUse reports whether the nonce has not been seen before, and remembers it.
func (g *ReplayGuard) FirstUse(grantee, nonce string, now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	for key, at := range g.seen {
		if now.Sub(at) > g.ttl {
			delete(g.seen, key)
		}
	}
	key := grantee + "\n" + nonce
	if _, ok := g.seen[key]; ok {
		return false
	}
	g.seen[key] = now
	return true
}
