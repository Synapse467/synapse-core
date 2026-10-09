package license

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Synapse467/synapse-core/capsule"
	"github.com/Synapse467/synapse-core/identity"
)

var epoch = time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)

func newID(t *testing.T) *identity.Identity {
	t.Helper()
	id, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func sealedCapsule(t *testing.T, owner *identity.Identity, version int, open bool) *capsule.Capsule {
	t.Helper()
	q := "Feed the starter daily."
	m := capsule.Manifest{
		Slug: "sourdough", Title: "Sourdough", Domain: "baking", Scope: "starters", Version: version, Owner: owner.Address(),
		CreatedAt: "2026-10-01T00:00:00Z",
		Knowledge: []capsule.Item{{ID: "item-a", Type: capsule.Claim, Title: "Feeding", Body: "Feed daily.", Contributor: owner.Address(),
			Citations: []capsule.Citation{capsule.NewCitation("s", 0, len(q), q)}}},
		Sources:    []capsule.Source{{ID: "s", Title: "S", SHA256: strings.Repeat("ab", 32), Bytes: 10}},
		Evaluation: capsule.Evaluation{SuiteHash: strings.Repeat("cd", 32), Cases: 3, Passed: true, CitationValidityBP: 10000, CoverageBP: 10000, AbstentionBP: 10000, AttributionBP: 10000},
		Policy:     capsule.Policy{Open: open, Purposes: []string{"learning"}},
	}
	if version > 1 {
		m.Previous = strings.Repeat("9", 64)
	}
	c, err := capsule.Seal(m, owner)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func terms(owner, buyer *identity.Identity) Terms {
	return Terms{
		Capsule:  CapsuleRef{Owner: owner.Address(), Slug: "sourdough"},
		Grantor:  owner.Address(),
		Grantee:  buyer.Address(),
		Purposes: []string{"research", "support"},
	}
}

func issue(t *testing.T, owner *identity.Identity, tm Terms) *License {
	t.Helper()
	l, err := Issue(tm, owner)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func request(buyer *identity.Identity, purpose string) Request {
	return Request{Grantee: buyer.Address(), Purpose: purpose, Now: epoch}
}

func TestIssueAndVerify(t *testing.T) {
	owner, buyer := newID(t), newID(t)
	l := issue(t, owner, terms(owner, buyer))
	if err := l.Verify(); err != nil {
		t.Fatal(err)
	}
	if len(l.Terms.ID) != 32 || len(l.Ref()) != 64 {
		t.Fatalf("unexpected id/ref lengths %d %d", len(l.Terms.ID), len(l.Ref()))
	}
	again := issue(t, owner, terms(owner, buyer))
	if again.Terms.ID == l.Terms.ID {
		t.Fatal("two licenses must get different ids")
	}
}

func TestOnlyTheOwnerCanLicenseACapsule(t *testing.T) {
	owner, other, buyer := newID(t), newID(t), newID(t)
	tm := terms(owner, buyer)
	if _, err := Issue(tm, other); err == nil {
		t.Fatal("someone else signed a license in the owner's name")
	}
	tm.Grantor = other.Address()
	if _, err := Issue(tm, other); err == nil {
		t.Fatal("a non-owner licensed a capsule they do not own")
	}
}

func TestInvalidTermsAreRefused(t *testing.T) {
	owner, buyer := newID(t), newID(t)
	bad := map[string]func(*Terms){
		"no slug":                func(t *Terms) { t.Capsule.Slug = "" },
		"no grantee":             func(t *Terms) { t.Grantee = "" },
		"no purposes":            func(t *Terms) { t.Purposes = nil },
		"a blank purpose":        func(t *Terms) { t.Purposes = []string{" "} },
		"negative quota":         func(t *Terms) { t.MaxQueries = -1 },
		"a bad expiry":           func(t *Terms) { t.ExpiresAt = "tomorrow" },
		"a bad start":            func(t *Terms) { t.NotBefore = "soon" },
		"expiry before start":    func(t *Terms) { t.NotBefore = "2026-12-01T00:00:00Z"; t.ExpiresAt = "2026-11-01T00:00:00Z" },
		"min version above max":  func(t *Terms) { t.Capsule.MinVersion, t.Capsule.MaxVersion = 3, 2 },
		"a negative min version": func(t *Terms) { t.Capsule.MinVersion = -1 },
	}
	for name, mutate := range bad {
		t.Run(name, func(t *testing.T) {
			tm := terms(owner, buyer)
			mutate(&tm)
			if _, err := Issue(tm, owner); err == nil {
				t.Fatalf("%s was accepted", name)
			}
		})
	}
}

func TestAnyChangeToALicenseIsDetected(t *testing.T) {
	owner, buyer := newID(t), newID(t)
	original := issue(t, owner, terms(owner, buyer))
	changes := map[string]func(l *License){
		"the grantee":   func(l *License) { l.Terms.Grantee = newID(t).Address() },
		"the purposes":  func(l *License) { l.Terms.Purposes = []string{"*"} },
		"the quota":     func(l *License) { l.Terms.MaxQueries = 1000000 },
		"AI training":   func(l *License) { l.Terms.AITraining = true },
		"the expiry":    func(l *License) { l.Terms.ExpiresAt = "2099-01-01T00:00:00Z" },
		"the signature": func(l *License) { l.Signature = "AAAA" },
		"the hash":      func(l *License) { l.Hash = strings.Repeat("0", 64) },
		"the format":    func(l *License) { l.Format = "other/1" },
		"the grantor":   func(l *License) { l.Terms.Grantor = buyer.Address() },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			data, _ := json.Marshal(original)
			copied, err := Parse(data)
			if err != nil {
				t.Fatal(err)
			}
			change(copied)
			if copied.Verify() == nil {
				t.Fatalf("a change to %s was not detected", name)
			}
		})
	}
}

func TestAnAllowedRequest(t *testing.T) {
	owner, buyer := newID(t), newID(t)
	c := sealedCapsule(t, owner, 1, false)
	l := issue(t, owner, terms(owner, buyer))
	d := Check(l, c, nil, request(buyer, "research"))
	if !d.Allowed || d.Code != OK || d.Remaining != -1 {
		t.Fatalf("unexpected decision %+v", d)
	}
}

func TestEveryWayARequestIsDenied(t *testing.T) {
	owner, buyer, stranger := newID(t), newID(t), newID(t)
	c := sealedCapsule(t, owner, 2, false)
	pinned := sealedCapsule(t, owner, 2, false)
	base := terms(owner, buyer)

	type tc struct {
		name   string
		tweak  func(*Terms)
		req    func(*Request)
		wantOn Code
		cap    *capsule.Capsule
	}
	cases := []tc{
		{name: "a purpose that is not covered", req: func(r *Request) { r.Purpose = "marketing" }, wantOn: PurposeNotAllowed},
		{name: "no purpose at all", req: func(r *Request) { r.Purpose = "" }, wantOn: PurposeNotAllowed},
		{name: "commercial use", req: func(r *Request) { r.Commercial = true }, wantOn: CommercialNotAllowed},
		{name: "AI training", req: func(r *Request) { r.AITraining = true }, wantOn: TrainingNotAllowed},
		{name: "derivative works", req: func(r *Request) { r.Derivative = true }, wantOn: DerivativeNotAllowed},
		{name: "someone else asking", req: func(r *Request) { r.Grantee = stranger.Address() }, wantOn: WrongGrantee},
		{name: "before the start date", tweak: func(t *Terms) { t.NotBefore = "2026-11-01T00:00:00Z" }, wantOn: NotYetValid},
		{name: "after the expiry", tweak: func(t *Terms) { t.ExpiresAt = "2026-10-01T00:00:00Z" }, wantOn: Expired},
		{name: "exactly at the expiry", tweak: func(t *Terms) { t.ExpiresAt = "2026-10-10T12:00:00Z" }, wantOn: Expired},
		{name: "a used-up quota", tweak: func(t *Terms) { t.MaxQueries = 5 }, req: func(r *Request) { r.Used = 5 }, wantOn: QuotaExhausted},
		{name: "a version below the range", tweak: func(t *Terms) { t.Capsule.MinVersion = 3 }, wantOn: WrongVersion},
		{name: "a version above the range", tweak: func(t *Terms) { t.Capsule.MaxVersion = 1 }, wantOn: WrongVersion},
		{name: "a different pinned version", tweak: func(t *Terms) { t.Capsule.Hash = strings.Repeat("7", 64) }, wantOn: WrongVersion},
		{name: "a license for another capsule", tweak: func(t *Terms) { t.Capsule.Slug = "other" }, wantOn: WrongCapsule},
	}
	_ = pinned
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			tm := base
			if test.tweak != nil {
				test.tweak(&tm)
			}
			l := issue(t, owner, tm)
			req := request(buyer, "research")
			if test.req != nil {
				test.req(&req)
			}
			d := Check(l, c, nil, req)
			if d.Allowed || d.Code != test.wantOn {
				t.Fatalf("got %+v, want a denial with code %s", d, test.wantOn)
			}
			if d.Reason == "" {
				t.Fatal("a denial must explain itself")
			}
		})
	}
}

func TestPermissionsTheLicenseGrantsAreHonored(t *testing.T) {
	owner, buyer := newID(t), newID(t)
	c := sealedCapsule(t, owner, 1, false)
	tm := terms(owner, buyer)
	tm.Commercial, tm.AITraining, tm.Derivative = true, true, true
	l := issue(t, owner, tm)
	req := request(buyer, "support")
	req.Commercial, req.AITraining, req.Derivative = true, true, true
	if d := Check(l, c, nil, req); !d.Allowed {
		t.Fatalf("a fully permitted request was denied: %+v", d)
	}
}

func TestAWildcardPurposeAllowsAnyPurposeButNotAnEmptyOne(t *testing.T) {
	owner, buyer := newID(t), newID(t)
	c := sealedCapsule(t, owner, 1, false)
	tm := terms(owner, buyer)
	tm.Purposes = []string{"*"}
	l := issue(t, owner, tm)
	if d := Check(l, c, nil, request(buyer, "anything")); !d.Allowed {
		t.Fatalf("a wildcard should allow any stated purpose: %+v", d)
	}
	if d := Check(l, c, nil, request(buyer, "")); d.Allowed {
		t.Fatal("a request must always state a purpose")
	}
}

func TestTheQuotaCountsDownAndRunsOut(t *testing.T) {
	owner, buyer := newID(t), newID(t)
	c := sealedCapsule(t, owner, 1, false)
	tm := terms(owner, buyer)
	tm.MaxQueries = 3
	l := issue(t, owner, tm)
	for used := 0; used < 3; used++ {
		req := request(buyer, "research")
		req.Used = used
		d := Check(l, c, nil, req)
		if !d.Allowed || d.Remaining != 2-used {
			t.Fatalf("query %d: %+v", used+1, d)
		}
	}
	req := request(buyer, "research")
	req.Used = 3
	if d := Check(l, c, nil, req); d.Allowed || d.Code != QuotaExhausted {
		t.Fatalf("the fourth query should be refused: %+v", d)
	}
}

func TestATamperedLicenseIsDeniedAsBad(t *testing.T) {
	owner, buyer := newID(t), newID(t)
	c := sealedCapsule(t, owner, 1, false)
	l := issue(t, owner, terms(owner, buyer))
	l.Terms.Purposes = []string{"*"}
	d := Check(l, c, nil, request(buyer, "research"))
	if d.Allowed || d.Code != BadLicense {
		t.Fatalf("got %+v", d)
	}
}

func TestNoLicenseMeansLicenseRequired(t *testing.T) {
	owner := newID(t)
	c := sealedCapsule(t, owner, 1, false)
	if d := Check(nil, c, nil, Request{Purpose: "research", Now: epoch}); d.Allowed || d.Code != LicenseRequired {
		t.Fatalf("got %+v", d)
	}
	if d := Access(nil, c, nil, Request{Purpose: "learning", Now: epoch}); d.Allowed || d.Code != LicenseRequired {
		t.Fatalf("a closed capsule with no license must be refused: %+v", d)
	}
}

func TestOpenCapsulesNeedNoLicenseButKeepTheirPolicy(t *testing.T) {
	owner := newID(t)
	c := sealedCapsule(t, owner, 1, true)
	if d := Access(nil, c, nil, Request{Purpose: "learning", Now: epoch}); !d.Allowed || d.Code != OKOpen {
		t.Fatalf("got %+v", d)
	}
	for name, req := range map[string]Request{
		"another purpose": {Purpose: "resale", Now: epoch},
		"commercial use":  {Purpose: "learning", Commercial: true, Now: epoch},
		"AI training":     {Purpose: "learning", AITraining: true, Now: epoch},
		"derivatives":     {Purpose: "learning", Derivative: true, Now: epoch},
	} {
		if d := Access(nil, c, nil, req); d.Allowed {
			t.Errorf("an open capsule allowed %s", name)
		}
	}
}

func TestRevocation(t *testing.T) {
	owner, buyer, intruder := newID(t), newID(t), newID(t)
	c := sealedCapsule(t, owner, 1, false)
	l := issue(t, owner, terms(owner, buyer))
	set := RevocationSet{}

	if d := Check(l, c, set, request(buyer, "research")); !d.Allowed {
		t.Fatalf("before revocation: %+v", d)
	}
	r, err := Revoke(l, owner, epoch)
	if err != nil {
		t.Fatal(err)
	}
	if err := set.Add(*r); err != nil {
		t.Fatal(err)
	}
	if d := Check(l, c, set, request(buyer, "research")); d.Allowed || d.Code != Revoked {
		t.Fatalf("after revocation: %+v", d)
	}

	if _, err := Revoke(l, intruder, epoch); err == nil {
		t.Fatal("someone other than the grantor revoked a license")
	}
	// A revocation forged in the grantor's name, or signed by someone else, must not count.
	forged := *r
	forged.Signature = "AAAA"
	if err := (RevocationSet{}).Add(forged); err == nil {
		t.Fatal("a forged revocation was accepted")
	}
	other := issue(t, owner, terms(owner, buyer))
	if (RevocationSet{l.Terms.ID: *r}).Revoked(other) {
		t.Fatal("revoking one license revoked another")
	}
	tamperedID := *r
	tamperedID.License = other.Terms.ID
	if err := (RevocationSet{}).Add(tamperedID); err == nil {
		t.Fatal("a revocation re-pointed at another license kept a valid signature")
	}
}

func TestRevocationListsSurviveSaveAndLoadAndDropForgeries(t *testing.T) {
	owner, buyer := newID(t), newID(t)
	l := issue(t, owner, terms(owner, buyer))
	r, _ := Revoke(l, owner, epoch)
	set := RevocationSet{}
	set.Add(*r)
	path := filepath.Join(t.TempDir(), "revocations.json")
	if err := set.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadRevocations(path)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Revoked(l) {
		t.Fatal("the revocation was lost across a save and load")
	}
	empty, err := LoadRevocations(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil || len(empty) != 0 {
		t.Fatal("a missing list should be an empty set")
	}
	forged := *r
	forged.Signature = "AAAA"
	data, _ := json.Marshal([]Revocation{forged})
	forgedPath := filepath.Join(t.TempDir(), "forged.json")
	writeFile(t, forgedPath, data)
	if got, _ := LoadRevocations(forgedPath); got.Revoked(l) {
		t.Fatal("a forged revocation in a list was honored")
	}
}

func TestParseIsStrict(t *testing.T) {
	owner, buyer := newID(t), newID(t)
	data, _ := json.Marshal(issue(t, owner, terms(owner, buyer)))
	if _, err := Parse(data); err != nil {
		t.Fatal(err)
	}
	if _, err := Parse([]byte(strings.Replace(string(data), `{"format"`, `{"hidden":1,"format"`, 1))); err == nil {
		t.Fatal("an unknown field was accepted")
	}
	if _, err := Parse(append(data, []byte("{}")...)); err == nil {
		t.Fatal("trailing data was accepted")
	}
	if _, err := Parse(make([]byte, MaxFileBytes+1)); err == nil {
		t.Fatal("an oversized file was accepted")
	}
}

func TestLicensesSurviveASaveAndLoad(t *testing.T) {
	owner, buyer := newID(t), newID(t)
	l := issue(t, owner, terms(owner, buyer))
	path := filepath.Join(t.TempDir(), "buyer.license")
	if err := l.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := loaded.Verify(); err != nil {
		t.Fatal(err)
	}
}

func TestSignedRequests(t *testing.T) {
	buyer, other := newID(t), newID(t)
	r, err := NewRequest(buyer, strings.Repeat("a", 64), strings.Repeat("b", 64), "research", "How do I fix a sluggish starter?", epoch)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Verify(epoch, time.Minute); err != nil {
		t.Fatal(err)
	}
	if len(r.QuestionHash()) != 64 {
		t.Fatal("the question hash is 32 bytes of hex")
	}

	tampers := map[string]func(*SignedRequest){
		"the question": func(r *SignedRequest) { r.Question = "Something else" },
		"the purpose":  func(r *SignedRequest) { r.Purpose = "marketing" },
		"the license":  func(r *SignedRequest) { r.License = strings.Repeat("c", 64) },
		"the capsule":  func(r *SignedRequest) { r.Capsule = strings.Repeat("c", 64) },
		"the nonce":    func(r *SignedRequest) { r.Nonce = "1" },
		"the grantee":  func(r *SignedRequest) { r.Grantee = other.Address() },
	}
	for name, tamper := range tampers {
		copied := *r
		tamper(&copied)
		if copied.Verify(epoch, time.Minute) == nil {
			t.Errorf("a change to %s was not detected", name)
		}
	}
	if err := r.Verify(epoch.Add(2*time.Minute), time.Minute); err == nil {
		t.Error("a stale request was accepted")
	}
	if err := r.Verify(epoch.Add(-2*time.Minute), time.Minute); err == nil {
		t.Error("a request from the future was accepted")
	}
}

func TestReplayGuardRefusesASecondUseAndForgetsOldNonces(t *testing.T) {
	g := NewReplayGuard(time.Minute)
	if !g.FirstUse("G1", "n1", epoch) {
		t.Fatal("the first use must be allowed")
	}
	if g.FirstUse("G1", "n1", epoch.Add(time.Second)) {
		t.Fatal("a replay was allowed")
	}
	if !g.FirstUse("G2", "n1", epoch) {
		t.Fatal("the same nonce from a different grantee is a different request")
	}
	if !g.FirstUse("G1", "n1", epoch.Add(2*time.Minute)) {
		t.Fatal("an expired nonce should be forgotten so memory stays bounded")
	}
}

func TestSavingLicensesAndRevocationsCreatesMissingFolders(t *testing.T) {
	owner, buyer := newID(t), newID(t)
	l := issue(t, owner, Terms{Capsule: CapsuleRef{Owner: owner.Address(), Slug: "x"}, Grantee: buyer.Address(), Purposes: []string{"research"}})
	dir := filepath.Join(t.TempDir(), "a", "b")
	if err := l.Save(filepath.Join(dir, "l.json")); err != nil {
		t.Fatalf("license: %v", err)
	}
	rev, err := Revoke(l, owner, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	set := RevocationSet{}
	set.Add(*rev)
	if err := set.Save(filepath.Join(dir, "deeper", "r.json")); err != nil {
		t.Fatalf("revocations: %v", err)
	}
}
