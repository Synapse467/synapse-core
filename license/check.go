package license

import (
	"fmt"
	"time"

	"github.com/Synapse467/synapse-core/capsule"
)

// Code names why a request was allowed or denied. Codes are part of the public contract: other
// programs can branch on them.
type Code string

// Decision codes.
const (
	OK                   Code = "ok"
	OKOpen               Code = "ok_open"
	BadLicense           Code = "bad_license"
	WrongCapsule         Code = "wrong_capsule"
	WrongVersion         Code = "wrong_version"
	WrongGrantee         Code = "wrong_grantee"
	Revoked              Code = "revoked"
	NotYetValid          Code = "not_yet_valid"
	Expired              Code = "expired"
	PurposeNotAllowed    Code = "purpose_not_allowed"
	CommercialNotAllowed Code = "commercial_not_allowed"
	TrainingNotAllowed   Code = "ai_training_not_allowed"
	DerivativeNotAllowed Code = "derivative_not_allowed"
	QuotaExhausted       Code = "quota_exhausted"
	LicenseRequired      Code = "license_required"
)

// Request describes one attempt to use a capsule.
type Request struct {
	Grantee    string    // who is asking (a Stellar address)
	Purpose    string    // what for, e.g. "research"
	Now        time.Time // when
	Used       int       // how many queries this license has already been used for
	Commercial bool      // the use is commercial
	AITraining bool      // the answers will be used to train an AI model
	Derivative bool      // the answers will be used to make a derivative work
}

// Decision is the answer to Check.
type Decision struct {
	Allowed bool   `json:"allowed"`
	Code    Code   `json:"code"`
	Reason  string `json:"reason"`
	// Remaining is how many queries are left, or -1 when there is no cap.
	Remaining int `json:"remaining"`
}

func deny(code Code, format string, args ...any) Decision {
	return Decision{Allowed: false, Code: code, Reason: fmt.Sprintf(format, args...), Remaining: 0}
}

// Check decides whether a request to use capsule c under license l is allowed. It is pure and
// fails closed: anything it cannot positively confirm is a denial. revoked may be nil.
func Check(l *License, c *capsule.Capsule, revoked RevocationSet, req Request) Decision {
	if l == nil {
		return deny(LicenseRequired, "this capsule needs a license")
	}
	if err := l.Verify(); err != nil {
		return deny(BadLicense, "%v", err)
	}
	t := l.Terms
	m := c.Manifest

	if t.Capsule.Owner != m.Owner || t.Capsule.Slug != m.Slug {
		return deny(WrongCapsule, "this license is for %s by %s, not %s by %s", t.Capsule.Slug, t.Capsule.Owner, m.Slug, m.Owner)
	}
	if (t.Capsule.MinVersion != 0 && m.Version < t.Capsule.MinVersion) ||
		(t.Capsule.MaxVersion != 0 && m.Version > t.Capsule.MaxVersion) {
		return deny(WrongVersion, "this license does not cover version %d", m.Version)
	}
	if t.Capsule.Hash != "" && t.Capsule.Hash != c.Hash {
		return deny(WrongVersion, "this license is pinned to a different version of the capsule")
	}
	if revoked.Revoked(l) {
		return deny(Revoked, "the grantor has revoked this license")
	}
	if t.Grantee != req.Grantee {
		return deny(WrongGrantee, "this license was granted to someone else")
	}
	if t.NotBefore != "" {
		if start, err := time.Parse(time.RFC3339, t.NotBefore); err != nil || req.Now.Before(start) {
			return deny(NotYetValid, "this license is not valid until %s", t.NotBefore)
		}
	}
	if t.ExpiresAt != "" {
		if end, err := time.Parse(time.RFC3339, t.ExpiresAt); err != nil || !req.Now.Before(end) {
			return deny(Expired, "this license expired at %s", t.ExpiresAt)
		}
	}
	if !purposeAllowed(t.Purposes, req.Purpose) {
		return deny(PurposeNotAllowed, "the purpose %q is not covered", req.Purpose)
	}
	if req.Commercial && !t.Commercial {
		return deny(CommercialNotAllowed, "commercial use is not allowed")
	}
	if req.AITraining && !t.AITraining {
		return deny(TrainingNotAllowed, "using the answers to train an AI model is not allowed")
	}
	if req.Derivative && !t.Derivative {
		return deny(DerivativeNotAllowed, "making derivative works is not allowed")
	}
	remaining := -1
	if t.MaxQueries > 0 {
		if req.Used >= t.MaxQueries {
			return deny(QuotaExhausted, "all %d queries have been used", t.MaxQueries)
		}
		remaining = t.MaxQueries - req.Used - 1
	}
	return Decision{Allowed: true, Code: OK, Reason: "allowed", Remaining: remaining}
}

// CheckOpen decides a request for a capsule whose owner published it as open: no license is
// needed, but the capsule's own policy still limits purposes and uses.
func CheckOpen(c *capsule.Capsule, req Request) Decision {
	p := c.Manifest.Policy
	if !p.Open {
		return deny(LicenseRequired, "this capsule needs a license")
	}
	if !purposeAllowed(p.Purposes, req.Purpose) {
		return deny(PurposeNotAllowed, "the purpose %q is not covered", req.Purpose)
	}
	if req.Commercial && !p.Commercial {
		return deny(CommercialNotAllowed, "commercial use is not allowed")
	}
	if req.AITraining && !p.AITraining {
		return deny(TrainingNotAllowed, "using the answers to train an AI model is not allowed")
	}
	if req.Derivative && !p.Derivative {
		return deny(DerivativeNotAllowed, "making derivative works is not allowed")
	}
	return Decision{Allowed: true, Code: OKOpen, Reason: "this capsule is open for the stated purpose", Remaining: -1}
}

// Access is the entry point most programs want: with a license it runs Check, without one it
// falls back to CheckOpen.
func Access(l *License, c *capsule.Capsule, revoked RevocationSet, req Request) Decision {
	if l == nil {
		return CheckOpen(c, req)
	}
	return Check(l, c, revoked, req)
}

func purposeAllowed(allowed []string, purpose string) bool {
	if purpose == "" {
		return false
	}
	for _, candidate := range allowed {
		if candidate == "*" || candidate == purpose {
			return true
		}
	}
	return false
}
