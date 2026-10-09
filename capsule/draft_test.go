package capsule

import (
	"strings"
	"testing"
)

func newDraft(t *testing.T) (*Draft, string) {
	t.Helper()
	owner := newID(t)
	d, err := NewDraft("sourdough", "Sourdough", "baking", "Home sourdough", owner.Address())
	if err != nil {
		t.Fatal(err)
	}
	d.Policy = Policy{Open: true, Purposes: []string{"learning"}}
	if err := d.AddSource(Source{ID: "notes", Title: "Notes", SHA256: strings.Repeat("cd", 32), Bytes: 100}); err != nil {
		t.Fatal(err)
	}
	return d, owner.Address()
}

func extracted(owner string) Item {
	q := "Always use warm water."
	return Item{Type: Heuristic, Title: "Water temperature", Body: "Use warm water.", Contributor: owner,
		Citations: []Citation{NewCitation("notes", 0, len(q), q)}}
}

func TestNewDraftValidatesTheSlugAndTitle(t *testing.T) {
	if _, err := NewDraft("Bad Slug", "T", "", "", "G1"); err == nil {
		t.Fatal("an invalid slug was accepted")
	}
	if _, err := NewDraft("ok", "", "", "", "G1"); err == nil {
		t.Fatal("an empty title was accepted")
	}
}

func TestProposedItemsStartPendingAndKeepTheirProposal(t *testing.T) {
	d, owner := newDraft(t)
	entry, err := d.Propose(extracted(owner), "extracted")
	if err != nil {
		t.Fatal(err)
	}
	if entry.Status != Pending || entry.Proposal == nil || entry.Confidence != "extracted" {
		t.Fatalf("unexpected entry %+v", entry)
	}
	if d.Counts()[Pending] != 1 {
		t.Fatalf("unexpected counts %v", d.Counts())
	}
}

func TestADuplicateItemCannotBeProposedTwice(t *testing.T) {
	d, owner := newDraft(t)
	if _, err := d.Propose(extracted(owner), "extracted"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Propose(extracted(owner), "extracted"); err == nil {
		t.Fatal("a duplicate was accepted")
	}
}

func TestAnInvalidProposalIsRefused(t *testing.T) {
	d, owner := newDraft(t)
	if _, err := d.Propose(Item{Type: Claim, Title: "no evidence", Body: "b", Contributor: owner}, "authored"); err == nil {
		t.Fatal("an item with no citation and not authored was accepted")
	}
	if len(d.Items) != 0 {
		t.Fatal("a refused proposal was still added")
	}
}

func TestItemsDefaultToTheOwnerAsContributor(t *testing.T) {
	d, owner := newDraft(t)
	item := extracted("")
	entry, err := d.Propose(item, "extracted")
	if err != nil {
		t.Fatal(err)
	}
	if entry.Contributor != owner {
		t.Fatalf("contributor %q, want the owner", entry.Contributor)
	}
}

func TestOnlyApprovedItemsReachAPublishedManifest(t *testing.T) {
	d, owner := newDraft(t)
	a, _ := d.Propose(extracted(owner), "extracted")
	other := Item{Type: Claim, Title: "Salt", Body: "Salt slows fermentation.", Contributor: owner, Authored: true}
	b, _ := d.Propose(other, "authored")
	third := Item{Type: Claim, Title: "Rejected", Body: "Never knead.", Contributor: owner, Authored: true}
	c, _ := d.Propose(third, "authored")
	fourth := Item{Type: Claim, Title: "Pending", Body: "Not reviewed yet.", Contributor: owner, Authored: true}
	d.Propose(fourth, "authored")

	if err := d.Approve(a.ID); err != nil {
		t.Fatal(err)
	}
	if err := d.Approve(b.ID); err != nil {
		t.Fatal(err)
	}
	if err := d.Reject(c.ID); err != nil {
		t.Fatal(err)
	}
	m, err := d.Build("2026-10-10T00:00:00Z", passed())
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Knowledge) != 2 {
		t.Fatalf("expected 2 approved items, got %d", len(m.Knowledge))
	}
	for _, item := range m.Knowledge {
		if item.Title == "Rejected" || item.Title == "Pending" {
			t.Fatalf("an item that was not approved was published: %s", item.Title)
		}
	}
	if m.Version != 1 || m.Previous != "" {
		t.Fatalf("unexpected version info %d %q", m.Version, m.Previous)
	}
}

func TestBuildRefusesWhenNothingIsApproved(t *testing.T) {
	d, owner := newDraft(t)
	d.Propose(extracted(owner), "extracted")
	if _, err := d.Build("2026-10-10T00:00:00Z", passed()); err == nil {
		t.Fatal("a draft with nothing approved was built")
	}
}

func TestEditingKeepsTheIDAndTheOriginalProposal(t *testing.T) {
	d, owner := newDraft(t)
	entry, _ := d.Propose(extracted(owner), "extracted")
	if err := d.Edit(entry.ID, func(i *Item) { i.Body = "Use water around 28 degrees Celsius." }); err != nil {
		t.Fatal(err)
	}
	edited, _ := d.find(entry.ID)
	if edited.ID != entry.ID || edited.Status != Approved {
		t.Fatalf("an edit should keep the id and approve the item: %+v", edited)
	}
	if edited.Body != "Use water around 28 degrees Celsius." {
		t.Fatal("the edit was not applied")
	}
	if edited.Proposal == nil || edited.Proposal.Body != "Use warm water." {
		t.Fatal("the original proposal was lost, so the audit trail is gone")
	}
	if err := d.Edit(entry.ID, func(i *Item) { i.Title = "" }); err == nil {
		t.Fatal("an edit that makes the item invalid was accepted")
	}
	if err := d.Approve("item-nope"); err == nil {
		t.Fatal("approving an unknown item should fail")
	}
}

func TestOnlyCitedSourcesAreIncluded(t *testing.T) {
	d, owner := newDraft(t)
	d.AddSource(Source{ID: "unused", Title: "Not cited", SHA256: strings.Repeat("ee", 32), Bytes: 5})
	entry, _ := d.Propose(extracted(owner), "extracted")
	d.Approve(entry.ID)
	m, err := d.Build("2026-10-10T00:00:00Z", passed())
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Sources) != 1 || m.Sources[0].ID != "notes" {
		t.Fatalf("expected only the cited source, got %+v", m.Sources)
	}
}

func TestSourcesAreDeduplicatedByContent(t *testing.T) {
	d, _ := newDraft(t)
	same := Source{ID: "notes", Title: "Notes", SHA256: strings.Repeat("cd", 32), Bytes: 100}
	if err := d.AddSource(same); err != nil {
		t.Fatal(err)
	}
	if len(d.Sources) != 1 {
		t.Fatal("the same source was added twice")
	}
	changed := same
	changed.SHA256 = strings.Repeat("ff", 32)
	if err := d.AddSource(changed); err == nil {
		t.Fatal("a different document reusing an id was accepted")
	}
	if err := d.AddSource(Source{ID: "x", SHA256: "short"}); err == nil {
		t.Fatal("a source without a valid hash was accepted")
	}
}

func TestPublishingAdvancesTheVersionAndLinksThem(t *testing.T) {
	d, owner := newDraft(t)
	ownerID := newID(t)
	d.Owner = ownerID.Address()
	e, _ := d.Propose(Item{Type: Claim, Title: "A", Body: "b", Contributor: d.Owner, Authored: true}, "authored")
	d.Approve(e.ID)
	_ = owner

	m1, err := d.Build("2026-10-10T00:00:00Z", passed())
	if err != nil {
		t.Fatal(err)
	}
	v1 := seal(t, m1, ownerID)
	d.MarkPublished(v1)

	e2, _ := d.Propose(Item{Type: Claim, Title: "B", Body: "c", Contributor: d.Owner, Authored: true}, "authored")
	d.Approve(e2.ID)
	m2, err := d.Build("2026-10-11T00:00:00Z", passed())
	if err != nil {
		t.Fatal(err)
	}
	if m2.Version != 2 || m2.Previous != v1.Hash {
		t.Fatalf("version 2 should link to version 1: %d %q", m2.Version, m2.Previous)
	}
	v2 := seal(t, m2, ownerID)
	if report := Verify(v2, v1); !report.OK {
		t.Fatalf("the chain failed: %v", report.Issues)
	}
	if len(m2.Knowledge) != 2 {
		t.Fatalf("version 2 should hold both approved items, got %d", len(m2.Knowledge))
	}
}

func TestADraftSurvivesASaveAndLoad(t *testing.T) {
	d, owner := newDraft(t)
	entry, _ := d.Propose(extracted(owner), "extracted")
	d.Approve(entry.ID)
	dir := t.TempDir()
	if err := d.Save(dir); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadDraft(dir)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Slug != d.Slug || len(loaded.Items) != 1 || loaded.Items[0].Status != Approved || loaded.Items[0].Proposal == nil {
		t.Fatalf("the draft changed across a save and load: %+v", loaded)
	}
	if _, err := LoadDraft(t.TempDir()); err == nil {
		t.Fatal("loading a missing draft should fail")
	}
}

func TestSourceIDsCannotEscapeTheirFolder(t *testing.T) {
	d, _ := NewDraft("ops", "Ops", "ops", "x", "GOWNER")
	hash := HashText("doc")
	for _, id := range []string{"", "../../etc/passwd", "a/b", `a\b`, "..", ".", ".hidden", "UPPER", "with space", strings.Repeat("a", 81)} {
		if err := d.AddSource(Source{ID: id, Title: "t", SHA256: hash, Bytes: 3}); err == nil {
			t.Errorf("source id %q was accepted", id)
		}
	}
	if err := d.AddSource(Source{ID: "runbook-v2.1", Title: "t", SHA256: hash, Bytes: 3}); err != nil {
		t.Fatalf("a normal source id was refused: %v", err)
	}
}
