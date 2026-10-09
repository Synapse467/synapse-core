package capsule

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// DraftFile is the name of the draft inside a workspace directory.
const DraftFile = "synapse.draft.json"

// Status is where an item is in the expert's review.
type Status string

// Review states. Only approved items can enter a published capsule.
const (
	Pending  Status = "pending"
	Approved Status = "approved"
	Rejected Status = "rejected"
)

// DraftItem is an item under review. If the expert edits a proposal, the original proposal is
// kept in Proposal, so the audit trail shows what was suggested and what was approved.
type DraftItem struct {
	Item
	Status     Status `json:"status"`
	Proposal   *Item  `json:"proposal,omitempty"`
	Confidence string `json:"confidence,omitempty"` // how the item was found: "extracted" or "authored"
}

// Draft is the expert's working copy of a capsule.
type Draft struct {
	Slug         string        `json:"slug"`
	Title        string        `json:"title"`
	Domain       string        `json:"domain"`
	Scope        string        `json:"scope"`
	Owner        string        `json:"owner"`
	Contributors []Contributor `json:"contributors,omitempty"`
	Sources      []Source      `json:"sources,omitempty"`
	Items        []DraftItem   `json:"items"`
	Policy       Policy        `json:"policy"`
	// Published is the version last published from this draft, and PreviousHash its hash.
	Published    int    `json:"published"`
	PreviousHash string `json:"previousHash,omitempty"`
}

// NewDraft starts a draft owned by the given address.
func NewDraft(slug, title, domain, scope, owner string) (*Draft, error) {
	if !slugPattern.MatchString(slug) {
		return nil, errors.New("capsule: the slug must be lower-case letters, digits and '-', up to 63 characters")
	}
	if title == "" {
		return nil, errors.New("capsule: a capsule needs a title")
	}
	return &Draft{Slug: slug, Title: title, Domain: domain, Scope: scope, Owner: owner, Items: []DraftItem{}}, nil
}

// AddSource records an original document by its hash. Adding the same document twice is a no-op.
func (d *Draft) AddSource(source Source) error {
	if !idPattern.MatchString(source.ID) || !hexPattern.MatchString(source.SHA256) {
		return errors.New("capsule: a source needs an id (lower-case letters, digits, '.', '_' and '-') and a SHA-256")
	}
	for _, existing := range d.Sources {
		if existing.ID == source.ID {
			if existing.SHA256 != source.SHA256 {
				return fmt.Errorf("capsule: source %q already exists with different contents", source.ID)
			}
			return nil
		}
	}
	d.Sources = append(d.Sources, source)
	return nil
}

// Propose adds an item for review. confidence says how it was found ("extracted" or "authored").
// The item gets a stable ID, and an identical item cannot be added twice.
func (d *Draft) Propose(item Item, confidence string) (DraftItem, error) {
	if item.Contributor == "" {
		item.Contributor = d.Owner
	}
	item.ID = ItemID(item)
	for _, existing := range d.Items {
		if existing.ID == item.ID {
			return DraftItem{}, fmt.Errorf("capsule: this item is already in the draft as %s", item.ID)
		}
	}
	if issues := item.Validate(); len(issues) > 0 {
		return DraftItem{}, errors.New(issues[0])
	}
	proposal := item
	entry := DraftItem{Item: item, Status: Pending, Proposal: &proposal, Confidence: confidence}
	d.Items = append(d.Items, entry)
	return entry, nil
}

func (d *Draft) find(id string) (*DraftItem, error) {
	for i := range d.Items {
		if d.Items[i].ID == id {
			return &d.Items[i], nil
		}
	}
	return nil, fmt.Errorf("capsule: no item %q in the draft", id)
}

// Approve marks an item as approved.
func (d *Draft) Approve(id string) error {
	item, err := d.find(id)
	if err != nil {
		return err
	}
	item.Status = Approved
	return nil
}

// Reject marks an item as rejected. A rejected item can never enter a published capsule.
func (d *Draft) Reject(id string) error {
	item, err := d.find(id)
	if err != nil {
		return err
	}
	item.Status = Rejected
	return nil
}

// Edit replaces an item's content and approves it. The original proposal stays in the draft.
// The ID does not change, so references to the item keep working.
func (d *Draft) Edit(id string, edit func(*Item)) error {
	entry, err := d.find(id)
	if err != nil {
		return err
	}
	updated := entry.Item
	edit(&updated)
	updated.ID = id
	if issues := updated.Validate(); len(issues) > 0 {
		return errors.New(issues[0])
	}
	entry.Item = updated
	entry.Status = Approved
	return nil
}

// Counts returns how many items are in each state.
func (d *Draft) Counts() map[Status]int {
	counts := map[Status]int{Pending: 0, Approved: 0, Rejected: 0}
	for _, item := range d.Items {
		counts[item.Status]++
	}
	return counts
}

// Build turns the approved items into a manifest for the next version. Pending and rejected
// items are left out, so nothing the expert has not approved can be published.
func (d *Draft) Build(createdAt string, evaluation Evaluation) (Manifest, error) {
	var approved []Item
	for _, item := range d.Items {
		if item.Status == Approved {
			approved = append(approved, item.Item)
		}
	}
	if len(approved) == 0 {
		return Manifest{}, errors.New("capsule: no approved items to publish")
	}
	sort.SliceStable(approved, func(i, j int) bool { return approved[i].ID < approved[j].ID })

	// Keep only the sources the approved items actually cite.
	cited := map[string]bool{}
	for _, item := range approved {
		for _, citation := range item.Citations {
			cited[citation.Source] = true
		}
	}
	var sources []Source
	for _, source := range d.Sources {
		if cited[source.ID] {
			sources = append(sources, source)
		}
	}
	sort.SliceStable(sources, func(i, j int) bool { return sources[i].ID < sources[j].ID })

	m := Manifest{
		Slug:         d.Slug,
		Title:        d.Title,
		Domain:       d.Domain,
		Scope:        d.Scope,
		Version:      d.Published + 1,
		Previous:     d.PreviousHash,
		Owner:        d.Owner,
		Contributors: d.Contributors,
		CreatedAt:    createdAt,
		Knowledge:    approved,
		Sources:      sources,
		Evaluation:   evaluation,
		Policy:       d.Policy,
	}
	if issues := validateManifest(m); len(issues) > 0 {
		return Manifest{}, fmt.Errorf("capsule: %s", issues[0])
	}
	return m, nil
}

// MarkPublished records that a version was published, so the next one continues from it.
func (d *Draft) MarkPublished(c *Capsule) {
	d.Published = c.Manifest.Version
	d.PreviousHash = c.Hash
}

// LoadDraft reads the draft in a workspace directory.
func LoadDraft(dir string) (*Draft, error) {
	data, err := os.ReadFile(filepath.Join(dir, DraftFile))
	if err != nil {
		return nil, err
	}
	var d Draft
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("capsule: %s is not a valid draft: %w", DraftFile, err)
	}
	return &d, nil
}

// Save writes the draft into a workspace directory.
func (d *Draft) Save(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, DraftFile), append(data, '\n'), 0o644)
}
