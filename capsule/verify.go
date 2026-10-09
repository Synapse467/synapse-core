package capsule

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Synapse467/synapse-core/identity"
)

func bytesReader(data []byte) io.Reader { return bytes.NewReader(data) }

func readLimited(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("capsule: file is larger than %d bytes", limit)
	}
	return data, nil
}

// SignerStatus reports one signature.
type SignerStatus struct {
	Address string `json:"address"`
	Role    string `json:"role"`
	Valid   bool   `json:"valid"`
}

// Report is the result of Verify.
type Report struct {
	OK            bool           `json:"ok"`
	Hash          string         `json:"hash"`
	Version       int            `json:"version"`
	Owner         string         `json:"owner"`
	Signers       []SignerStatus `json:"signers"`
	Contributions map[string]int `json:"contributions"`
	Issues        []string       `json:"issues,omitempty"`
}

// Verify checks a capsule completely and offline. previous is the earlier version, if the caller
// has it; with it, Verify also checks that this version continues that one.
func Verify(c *Capsule, previous *Capsule) Report {
	m := c.Manifest
	report := Report{Hash: c.Hash, Version: m.Version, Owner: m.Owner, Contributions: map[string]int{}}
	fail := func(format string, args ...any) {
		report.Issues = append(report.Issues, fmt.Sprintf(format, args...))
	}

	if c.Format != Format {
		fail("unsupported format %q, expected %q", c.Format, Format)
	}
	for _, issue := range validateManifest(m) {
		fail("%s", issue)
	}
	recomputed, err := m.Hash()
	if err != nil {
		fail("the manifest cannot be hashed: %v", err)
	} else if recomputed != c.Hash {
		fail("the hash does not match the manifest: it has been changed since it was signed")
	}

	listed := map[string]bool{m.Owner: true}
	for _, contributor := range m.Contributors {
		listed[contributor.Address] = true
	}
	ownerSigned := false
	seen := map[string]bool{}
	for _, signature := range c.Signatures {
		status := SignerStatus{Address: signature.Signer, Role: signature.Role}
		raw, decodeErr := base64.StdEncoding.DecodeString(signature.Signature)
		if decodeErr == nil && identity.Verify(signature.Signer, SignDomain, []byte(c.Hash), raw) == nil {
			status.Valid = true
		}
		switch {
		case seen[signature.Signer]:
			status.Valid = false
			fail("%s signed more than once", signature.Signer)
		case !status.Valid:
			fail("the signature of %s is not valid", signature.Signer)
		case !listed[signature.Signer]:
			status.Valid = false
			fail("%s signed but is not the owner or a listed contributor", signature.Signer)
		case signature.Role == "owner" && signature.Signer != m.Owner:
			status.Valid = false
			fail("%s claims the owner role but is not the owner", signature.Signer)
		}
		seen[signature.Signer] = true
		if status.Valid && signature.Signer == m.Owner {
			ownerSigned = true
		}
		report.Signers = append(report.Signers, status)
	}
	if !ownerSigned {
		fail("the owner has not signed this capsule")
	}

	for _, item := range m.Knowledge {
		report.Contributions[item.Contributor]++
	}

	if previous != nil {
		switch {
		case previous.Manifest.Slug != m.Slug || previous.Manifest.Owner != m.Owner:
			fail("the previous version belongs to a different capsule")
		case m.Version != previous.Manifest.Version+1:
			fail("version %d does not follow version %d", m.Version, previous.Manifest.Version)
		case m.Previous != previous.Hash:
			fail("the previous hash does not match version %d", previous.Manifest.Version)
		}
	}

	report.OK = len(report.Issues) == 0
	return report
}

func validateManifest(m Manifest) []string {
	var issues []string
	add := func(format string, args ...any) { issues = append(issues, fmt.Sprintf(format, args...)) }

	if !slugPattern.MatchString(m.Slug) {
		add("the slug must be lower-case letters, digits and '-', up to 63 characters")
	}
	if strings.TrimSpace(m.Title) == "" || utf8.RuneCountInString(m.Title) > MaxTitle {
		add("the title must be 1 to %d characters", MaxTitle)
	}
	if m.Version < 1 {
		add("the version must be 1 or more")
	}
	switch {
	case m.Version == 1 && m.Previous != "":
		add("version 1 cannot have a previous version")
	case m.Version > 1 && !hexPattern.MatchString(m.Previous):
		add("a later version must name the hash of the one before it")
	}
	if m.Owner == "" {
		add("the capsule has no owner")
	}
	if _, err := time.Parse(time.RFC3339, m.CreatedAt); err != nil {
		add("createdAt must be an RFC 3339 time")
	}
	if len(m.Knowledge) == 0 {
		add("a capsule must hold at least one approved item")
	}

	people := map[string]bool{m.Owner: true}
	for _, contributor := range m.Contributors {
		if contributor.Address == "" {
			add("a contributor has no address")
		}
		people[contributor.Address] = true
	}
	sources := map[string]bool{}
	for _, source := range m.Sources {
		if !idPattern.MatchString(source.ID) || sources[source.ID] {
			add("source ids must be lower-case letters, digits, '.', '_' and '-', and unique")
		}
		sources[source.ID] = true
		if !hexPattern.MatchString(source.SHA256) {
			add("source %q has no valid SHA-256", source.ID)
		}
	}
	ids := map[string]bool{}
	for _, item := range m.Knowledge {
		issues = append(issues, item.Validate()...)
		if ids[item.ID] {
			add("item id %q is used twice", item.ID)
		}
		ids[item.ID] = true
		if !people[item.Contributor] {
			add("item %q names %s as its contributor, who is not listed", item.ID, item.Contributor)
		}
		for _, citation := range item.Citations {
			if !sources[citation.Source] {
				add("item %q cites unknown source %q", item.ID, citation.Source)
			}
		}
	}
	for _, item := range m.Knowledge {
		for _, target := range item.AppliesTo {
			if !ids[target] {
				add("item %q applies to %q, which is not in this capsule", item.ID, target)
			}
		}
	}

	e := m.Evaluation
	if !hexPattern.MatchString(e.SuiteHash) {
		add("the evaluation suite hash is missing: a capsule is published only after it is evaluated")
	}
	if !e.Passed {
		add("the capsule did not pass its evaluation")
	}
	for name, bp := range map[string]int{"citation validity": e.CitationValidityBP, "coverage": e.CoverageBP, "abstention": e.AbstentionBP, "attribution": e.AttributionBP} {
		if bp < 0 || bp > 10000 {
			add("the %s score must be between 0 and 10000", name)
		}
	}
	return issues
}
