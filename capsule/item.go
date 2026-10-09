package capsule

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// ItemType is the kind of expertise an item holds.
type ItemType string

// The five kinds of expertise a capsule can hold.
const (
	Claim     ItemType = "claim"     // a statement the expert stands behind
	Procedure ItemType = "procedure" // ordered steps, with the conditions they apply under
	Heuristic ItemType = "heuristic" // a rule of thumb: in this situation, do this
	Exception ItemType = "exception" // when another item does not apply
	Case      ItemType = "case"      // a real example: context, action, outcome, lesson
)

// ValidTypes lists every item type.
var ValidTypes = []ItemType{Claim, Procedure, Heuristic, Exception, Case}

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,79}$`)

// Limits keep a capsule reviewable and keep a hostile file from being enormous.
const (
	MaxTitle     = 200
	MaxBody      = 4000
	MaxStep      = 1000
	MaxSteps     = 50
	MaxListItems = 20
	MaxQuote     = 2000
)

// Item is one piece of approved expertise.
type Item struct {
	ID          string     `json:"id"`
	Type        ItemType   `json:"type"`
	Title       string     `json:"title"`
	Body        string     `json:"body"`
	Steps       []string   `json:"steps,omitempty"`
	Conditions  []string   `json:"conditions,omitempty"`
	Exceptions  []string   `json:"exceptions,omitempty"`
	Rationale   string     `json:"rationale,omitempty"`
	Limitations string     `json:"limitations,omitempty"`
	AppliesTo   []string   `json:"appliesTo,omitempty"`
	Tags        []string   `json:"tags,omitempty"`
	Contributor string     `json:"contributor"`
	Authored    bool       `json:"authored,omitempty"`
	Citations   []Citation `json:"citations,omitempty"`
}

// Citation points to the words in a source that support an item. The quote travels with the
// capsule; the full source does not.
type Citation struct {
	Source      string `json:"source"`
	Start       int    `json:"start"`
	End         int    `json:"end"`
	Quote       string `json:"quote"`
	QuoteSHA256 string `json:"quoteSha256"`
}

// NewCitation builds a citation and computes the quote's hash.
func NewCitation(sourceID string, start, end int, quote string) Citation {
	return Citation{Source: sourceID, Start: start, End: end, Quote: quote, QuoteSHA256: HashText(quote)}
}

// HashText returns the lower-case hex SHA-256 of a string's UTF-8 bytes.
func HashText(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// ItemID derives a stable ID from what an item says and who contributed it, so the same item
// always gets the same ID and a duplicate cannot be added twice.
func ItemID(item Item) string {
	return "item-" + HashText(string(item.Type) + "\n" + item.Title + "\n" + item.Body + "\n" + item.Contributor)[:12]
}

// Validate returns every problem with an item. An empty result means the item is well formed.
// It checks the item on its own; Verify also checks it against the rest of the capsule.
func (item Item) Validate() []string {
	var issues []string
	add := func(format string, args ...any) {
		issues = append(issues, fmt.Sprintf("item %q: "+format, append([]any{item.ID}, args...)...))
	}

	if !idPattern.MatchString(item.ID) {
		add("the id is empty or not made of lower-case letters, digits, '.', '_' and '-'")
	}
	valid := false
	for _, t := range ValidTypes {
		if item.Type == t {
			valid = true
		}
	}
	if !valid {
		add("unknown type %q", item.Type)
	}
	if strings.TrimSpace(item.Title) == "" || utf8.RuneCountInString(item.Title) > MaxTitle {
		add("the title must be 1 to %d characters", MaxTitle)
	}
	if utf8.RuneCountInString(item.Body) > MaxBody {
		add("the body is longer than %d characters", MaxBody)
	}
	if item.Type == Procedure {
		if len(item.Steps) == 0 {
			add("a procedure needs at least one step")
		}
	} else if strings.TrimSpace(item.Body) == "" {
		add("the body is empty")
	}
	if len(item.Steps) > MaxSteps {
		add("more than %d steps", MaxSteps)
	}
	for _, step := range item.Steps {
		if strings.TrimSpace(step) == "" || utf8.RuneCountInString(step) > MaxStep {
			add("each step must be 1 to %d characters", MaxStep)
			break
		}
	}
	for name, list := range map[string][]string{"conditions": item.Conditions, "exceptions": item.Exceptions, "tags": item.Tags} {
		if len(list) > MaxListItems {
			add("more than %d %s", MaxListItems, name)
		}
		for _, entry := range list {
			if strings.TrimSpace(entry) == "" || utf8.RuneCountInString(entry) > MaxStep {
				add("each entry in %s must be 1 to %d characters", name, MaxStep)
				break
			}
		}
	}
	if item.Type == Exception && len(item.AppliesTo) == 0 {
		add("an exception must say which items it applies to")
	}
	if item.Contributor == "" {
		add("every item needs a contributor, so attribution is never lost")
	}
	if !item.Authored && len(item.Citations) == 0 {
		add("an item needs a citation, or must be marked as written directly by the expert")
	}
	for i, citation := range item.Citations {
		for _, problem := range citation.validate() {
			add("citation %d: %s", i+1, problem)
		}
	}
	return issues
}

func (c Citation) validate() []string {
	var issues []string
	if strings.TrimSpace(c.Source) == "" {
		issues = append(issues, "no source")
	}
	if c.Start < 0 || c.End < c.Start {
		issues = append(issues, "invalid start and end")
	}
	if strings.TrimSpace(c.Quote) == "" || utf8.RuneCountInString(c.Quote) > MaxQuote {
		issues = append(issues, fmt.Sprintf("the quote must be 1 to %d characters", MaxQuote))
	}
	if c.QuoteSHA256 != HashText(c.Quote) {
		issues = append(issues, "the quote does not match its recorded hash")
	}
	return issues
}
