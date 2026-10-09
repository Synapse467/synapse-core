package capsule

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Synapse467/synapse-core/identity"
)

func newID(t *testing.T) *identity.Identity {
	t.Helper()
	id, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func passed() Evaluation {
	return Evaluation{
		SuiteHash:          strings.Repeat("ab", 32),
		Cases:              5,
		Passed:             true,
		CitationValidityBP: 10000,
		CoverageBP:         9500,
		AbstentionBP:       10000,
		AttributionBP:      10000,
	}
}

// sampleManifest is a small, valid, entirely fictional capsule.
func sampleManifest(owner string, extra ...Contributor) Manifest {
	source := Source{ID: "notes", Title: "Starter notes", SHA256: strings.Repeat("cd", 32), Bytes: 120}
	quote := "Feed the starter at the same time each day."
	return Manifest{
		Slug:         "sourdough",
		Title:        "Sourdough troubleshooting",
		Domain:       "baking",
		Scope:        "Home sourdough starters and loaves",
		Version:      1,
		Owner:        owner,
		Contributors: extra,
		CreatedAt:    "2026-10-10T00:00:00Z",
		Knowledge: []Item{
			{
				ID:          "item-feed-schedule",
				Type:        Heuristic,
				Title:       "A sluggish starter",
				Body:        "Feed it on a fixed schedule and keep it warm.",
				Contributor: owner,
				Citations:   []Citation{NewCitation("notes", 0, len(quote), quote)},
			},
		},
		Sources:    []Source{source},
		Evaluation: passed(),
		Policy:     Policy{Open: true, Purposes: []string{"learning"}},
	}
}

func seal(t *testing.T, m Manifest, owner *identity.Identity) *Capsule {
	t.Helper()
	c, err := Seal(m, owner)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestSealAndVerify(t *testing.T) {
	owner := newID(t)
	c := seal(t, sampleManifest(owner.Address()), owner)
	report := Verify(c, nil)
	if !report.OK {
		t.Fatalf("a freshly sealed capsule failed verification: %v", report.Issues)
	}
	if report.Hash != c.Hash || report.Version != 1 || report.Owner != owner.Address() {
		t.Fatalf("unexpected report %+v", report)
	}
	if len(report.Signers) != 1 || !report.Signers[0].Valid || report.Signers[0].Role != "owner" {
		t.Fatalf("unexpected signers %+v", report.Signers)
	}
	if report.Contributions[owner.Address()] != 1 {
		t.Fatalf("unexpected contributions %+v", report.Contributions)
	}
}

func TestVerifySurvivesASaveAndLoadRoundTrip(t *testing.T) {
	owner := newID(t)
	c := seal(t, sampleManifest(owner.Address()), owner)
	path := filepath.Join(t.TempDir(), "sourdough.capsule")
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if report := Verify(loaded, nil); !report.OK {
		t.Fatalf("a reloaded capsule failed verification: %v", report.Issues)
	}
	if loaded.Hash != c.Hash {
		t.Fatal("the hash changed across a save and load")
	}
}

func TestAnyChangeAfterSigningIsDetected(t *testing.T) {
	owner := newID(t)
	original := seal(t, sampleManifest(owner.Address()), owner)
	tampers := map[string]func(c *Capsule){
		"the title":       func(c *Capsule) { c.Manifest.Title = "Something else" },
		"an item's text":  func(c *Capsule) { c.Manifest.Knowledge[0].Body = "Never feed it." },
		"an added item":   func(c *Capsule) { c.Manifest.Knowledge = append(c.Manifest.Knowledge, c.Manifest.Knowledge[0]) },
		"the owner":       func(c *Capsule) { c.Manifest.Owner = newID(t).Address() },
		"the policy":      func(c *Capsule) { c.Manifest.Policy.AITraining = true },
		"the evaluation":  func(c *Capsule) { c.Manifest.Evaluation.Cases = 999 },
		"the version":     func(c *Capsule) { c.Manifest.Version = 2 },
		"the hash itself": func(c *Capsule) { c.Hash = strings.Repeat("0", 64) },
	}
	for name, tamper := range tampers {
		t.Run(name, func(t *testing.T) {
			data, _ := json.Marshal(original)
			copied, err := Parse(data)
			if err != nil {
				t.Fatal(err)
			}
			tamper(copied)
			if report := Verify(copied, nil); report.OK {
				t.Fatalf("a change to %s was not detected", name)
			}
		})
	}
}

func TestACitationQuoteCannotBeChangedWithoutBreakingItsHash(t *testing.T) {
	owner := newID(t)
	m := sampleManifest(owner.Address())
	m.Knowledge[0].Citations[0].Quote = "A different sentence."
	if _, err := Seal(m, owner); err == nil {
		t.Fatal("a citation whose quote does not match its hash must not be sealed")
	}
}

func TestASignatureFromSomeoneElseIsNotTheOwners(t *testing.T) {
	owner := newID(t)
	intruder := newID(t)
	c := seal(t, sampleManifest(owner.Address()), owner)
	// Replace the owner's signature with the intruder's, claiming the owner role.
	forged := *c
	forged.Signatures = nil
	if err := forged.sign(intruder, "owner"); err != nil {
		t.Fatal(err)
	}
	report := Verify(&forged, nil)
	if report.OK {
		t.Fatal("a capsule signed by someone other than its owner verified")
	}
}

func TestSealRequiresTheOwnersKey(t *testing.T) {
	owner, other := newID(t), newID(t)
	if _, err := Seal(sampleManifest(owner.Address()), other); err == nil {
		t.Fatal("sealing with a key that is not the owner's must fail")
	}
}

func TestContributorsCoSignAndKeepTheirAttribution(t *testing.T) {
	owner, expert := newID(t), newID(t)
	m := sampleManifest(owner.Address(), Contributor{Address: expert.Address(), Name: "Second expert"})
	second := Item{
		ID: "item-second", Type: Claim, Title: "Hydration", Body: "A wetter dough ferments faster.",
		Contributor: expert.Address(), Authored: true,
	}
	m.Knowledge = append(m.Knowledge, second)
	c := seal(t, m, owner)
	if err := c.CoSign(expert); err != nil {
		t.Fatal(err)
	}
	report := Verify(c, nil)
	if !report.OK {
		t.Fatalf("co-signed capsule failed: %v", report.Issues)
	}
	if len(report.Signers) != 2 {
		t.Fatalf("expected two signers, got %+v", report.Signers)
	}
	if report.Contributions[owner.Address()] != 1 || report.Contributions[expert.Address()] != 1 {
		t.Fatalf("each expert should keep their own contribution: %+v", report.Contributions)
	}
}

func TestOnlyListedContributorsCanCoSignAndOnlyOnce(t *testing.T) {
	owner, expert, stranger := newID(t), newID(t), newID(t)
	c := seal(t, sampleManifest(owner.Address(), Contributor{Address: expert.Address()}), owner)
	if err := c.CoSign(stranger); err == nil {
		t.Fatal("a stranger was allowed to co-sign")
	}
	if err := c.CoSign(expert); err != nil {
		t.Fatal(err)
	}
	if err := c.CoSign(expert); err == nil {
		t.Fatal("a contributor was allowed to sign twice")
	}
	// A co-signature from a non-contributor, forced in by hand, is also caught.
	forged := *c
	if err := forged.sign(stranger, "contributor"); err != nil {
		t.Fatal(err)
	}
	if Verify(&forged, nil).OK {
		t.Fatal("a signature from a non-contributor verified")
	}
}

func TestAnItemMustNameAListedContributor(t *testing.T) {
	owner, stranger := newID(t), newID(t)
	m := sampleManifest(owner.Address())
	m.Knowledge[0].Contributor = stranger.Address()
	if _, err := Seal(m, owner); err == nil {
		t.Fatal("an item attributed to someone who is not listed was sealed")
	}
}

func TestItemRules(t *testing.T) {
	owner := newID(t).Address()
	base := func() Item {
		return Item{ID: "item-x", Type: Claim, Title: "T", Body: "B", Contributor: owner, Authored: true}
	}
	cases := map[string]func(i *Item){
		"no citation and not authored":         func(i *Item) { i.Authored = false },
		"unknown type":                         func(i *Item) { i.Type = "opinion" },
		"empty title":                          func(i *Item) { i.Title = "  " },
		"empty body for a claim":               func(i *Item) { i.Body = "" },
		"a procedure with no steps":            func(i *Item) { i.Type = Procedure },
		"an exception that applies to nothing": func(i *Item) { i.Type = Exception },
		"a bad id":                             func(i *Item) { i.ID = "Bad ID!" },
		"no contributor":                       func(i *Item) { i.Contributor = "" },
		"an overlong body":                     func(i *Item) { i.Body = strings.Repeat("x", MaxBody+1) },
		"a blank step":                         func(i *Item) { i.Type = Procedure; i.Steps = []string{"ok", " "} },
		"a citation with no source":            func(i *Item) { i.Authored = false; i.Citations = []Citation{NewCitation("", 0, 1, "q")} },
		"a citation with backwards offsets":    func(i *Item) { i.Authored = false; i.Citations = []Citation{NewCitation("s", 5, 1, "q")} },
	}
	if issues := base().Validate(); len(issues) != 0 {
		t.Fatalf("the base item should be valid: %v", issues)
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			item := base()
			mutate(&item)
			if len(item.Validate()) == 0 {
				t.Fatalf("expected %q to be rejected", name)
			}
		})
	}
}

func TestEveryItemTypeCanBeBuilt(t *testing.T) {
	owner := newID(t).Address()
	items := []Item{
		{Type: Claim, Title: "c", Body: "b"},
		{Type: Procedure, Title: "p", Steps: []string{"one", "two"}, Conditions: []string{"when cold"}},
		{Type: Heuristic, Title: "h", Body: "b", Rationale: "because"},
		{Type: Exception, Title: "e", Body: "b", AppliesTo: []string{"item-other"}},
		{Type: Case, Title: "k", Body: "context, action, outcome"},
	}
	for _, item := range items {
		item.ID, item.Contributor, item.Authored = "item-"+string(item.Type), owner, true
		if issues := item.Validate(); len(issues) != 0 {
			t.Errorf("%s: %v", item.Type, issues)
		}
	}
}

func TestAnExceptionMustPointAtAnItemThatExists(t *testing.T) {
	owner := newID(t)
	m := sampleManifest(owner.Address())
	m.Knowledge = append(m.Knowledge, Item{
		ID: "item-exc", Type: Exception, Title: "Not when cold", Body: "Skip it in winter.",
		AppliesTo: []string{"item-missing"}, Contributor: owner.Address(), Authored: true,
	})
	if _, err := Seal(m, owner); err == nil {
		t.Fatal("an exception pointing at a missing item was sealed")
	}
	m.Knowledge[1].AppliesTo = []string{"item-feed-schedule"}
	if _, err := Seal(m, owner); err != nil {
		t.Fatalf("a valid exception was rejected: %v", err)
	}
}

func TestACapsuleCannotBeSealedUnlessItPassedEvaluation(t *testing.T) {
	owner := newID(t)
	m := sampleManifest(owner.Address())
	m.Evaluation.Passed = false
	if _, err := Seal(m, owner); err == nil {
		t.Fatal("a capsule that failed its evaluation was sealed")
	}
	m = sampleManifest(owner.Address())
	m.Evaluation.SuiteHash = ""
	if _, err := Seal(m, owner); err == nil {
		t.Fatal("a capsule with no evaluation suite hash was sealed")
	}
}

func TestVersionsFormAnUnbrokenChain(t *testing.T) {
	owner := newID(t)
	v1 := seal(t, sampleManifest(owner.Address()), owner)

	m2 := sampleManifest(owner.Address())
	m2.Version, m2.Previous = 2, v1.Hash
	v2 := seal(t, m2, owner)
	if report := Verify(v2, v1); !report.OK {
		t.Fatalf("a correct continuation failed: %v", report.Issues)
	}

	m3 := sampleManifest(owner.Address())
	m3.Version, m3.Previous = 2, strings.Repeat("9", 64)
	wrongLink := seal(t, m3, owner)
	if Verify(wrongLink, v1).OK {
		t.Fatal("a version linking to the wrong hash was accepted")
	}

	m4 := sampleManifest(owner.Address())
	m4.Version, m4.Previous = 3, v1.Hash
	skipped := seal(t, m4, owner)
	if Verify(skipped, v1).OK {
		t.Fatal("a skipped version number was accepted")
	}

	other := newID(t)
	otherManifest := sampleManifest(other.Address())
	otherManifest.Version, otherManifest.Previous = 2, v1.Hash
	if Verify(seal(t, otherManifest, other), v1).OK {
		t.Fatal("a different owner was allowed to continue someone else's capsule")
	}
}

func TestVersionOneCannotHaveAPreviousAndLaterVersionsMust(t *testing.T) {
	owner := newID(t)
	m := sampleManifest(owner.Address())
	m.Previous = strings.Repeat("1", 64)
	if _, err := Seal(m, owner); err == nil {
		t.Fatal("version 1 with a previous hash was sealed")
	}
	m = sampleManifest(owner.Address())
	m.Version = 2
	if _, err := Seal(m, owner); err == nil {
		t.Fatal("version 2 with no previous hash was sealed")
	}
}

func TestParseIsStrict(t *testing.T) {
	owner := newID(t)
	data, _ := json.Marshal(seal(t, sampleManifest(owner.Address()), owner))
	hidden := strings.Replace(string(data), `{"format"`, `{"hidden":"x","format"`, 1)
	if _, err := Parse([]byte(hidden)); err == nil {
		t.Fatal("a capsule with an unknown field was accepted")
	}
	if _, err := Parse(append(data, []byte(`{"extra":1}`)...)); err == nil {
		t.Fatal("trailing data after a capsule was accepted")
	}
	if _, err := Parse([]byte("not json")); err == nil {
		t.Fatal("garbage was accepted")
	}
	if _, err := Parse(make([]byte, MaxFileBytes+1)); err == nil {
		t.Fatal("an oversized file was accepted")
	}
}

func TestRefsAreStableAndSeparateOwners(t *testing.T) {
	a, b := newID(t).Address(), newID(t).Address()
	if Ref(a, "x") != Ref(a, "x") {
		t.Fatal("a ref must be stable")
	}
	if Ref(a, "x") == Ref(b, "x") || Ref(a, "x") == Ref(a, "y") {
		t.Fatal("refs must differ by owner and by slug")
	}
	if len(Ref(a, "x")) != 64 {
		t.Fatal("a ref is 32 bytes of hex")
	}
}

func TestItemIDsAreStableAndContentAddressed(t *testing.T) {
	item := Item{Type: Claim, Title: "t", Body: "b", Contributor: "G1"}
	if ItemID(item) != ItemID(item) {
		t.Fatal("an id must be stable")
	}
	changed := item
	changed.Body = "other"
	if ItemID(item) == ItemID(changed) {
		t.Fatal("different content must give a different id")
	}
	if !strings.HasPrefix(ItemID(item), "item-") || len(ItemID(item)) != len("item-")+12 {
		t.Fatalf("unexpected id shape %q", ItemID(item))
	}
}
func TestSaveCreatesMissingFolders(t *testing.T) {
	owner := newID(t)
	c := seal(t, sampleManifest(owner.Address()), owner)
	path := filepath.Join(t.TempDir(), "dist", "nested", "x.capsule.json")
	if err := c.Save(path); err != nil {
		t.Fatalf("saving into a folder that does not exist yet failed: %v", err)
	}
	if _, err := Load(path); err != nil {
		t.Fatal(err)
	}
}
