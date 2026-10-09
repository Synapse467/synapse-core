// Package usage keeps a tamper-evident record of how a capsule was consulted.
//
// Each event is linked to the one before it by hash, so an edited, removed or reordered event is
// detected. Events record only the hash of a question, never the question itself. Batches of
// events can be sealed into a single hash, which is what gets recorded on-chain as a receipt.
package usage

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Synapse467/synapse-core/canonical"
)

// Zero is the hash that starts every chain.
var Zero = strings.Repeat("0", 64)

// OpenLicense is the license value recorded for consultations of an open capsule.
const OpenLicense = "open"

// Event is one consultation, allowed or denied.
type Event struct {
	Seq            int    `json:"seq"`
	At             string `json:"at"`
	License        string `json:"license"` // the license's terms ID, or "open"
	Capsule        string `json:"capsule"` // hash of the capsule that was consulted
	Version        int    `json:"version"`
	Grantee        string `json:"grantee"`
	Purpose        string `json:"purpose"`
	QuestionSHA256 string `json:"questionSha256"`
	Allowed        bool   `json:"allowed"`
	Code           string `json:"code"`
	Prev           string `json:"prev"`
	Hash           string `json:"hash"`
}

type eventBody struct {
	Seq            int    `json:"seq"`
	At             string `json:"at"`
	License        string `json:"license"`
	Capsule        string `json:"capsule"`
	Version        int    `json:"version"`
	Grantee        string `json:"grantee"`
	Purpose        string `json:"purpose"`
	QuestionSHA256 string `json:"questionSha256"`
	Allowed        bool   `json:"allowed"`
	Code           string `json:"code"`
	Prev           string `json:"prev"`
}

func (e Event) computeHash() (string, error) {
	return canonical.Hash(eventBody{e.Seq, e.At, e.License, e.Capsule, e.Version, e.Grantee, e.Purpose, e.QuestionSHA256, e.Allowed, e.Code, e.Prev})
}

// Batch is a sealed group of allowed events for one license.
type Batch struct {
	License     string   `json:"license"`
	Seq         int      `json:"seq"`
	Previous    string   `json:"previous"`
	Events      []string `json:"events"` // the hashes of the events it covers
	Count       int      `json:"count"`
	PeriodStart int64    `json:"periodStart"`
	PeriodEnd   int64    `json:"periodEnd"`
	Hash        string   `json:"hash"`
}

type batchBody struct {
	License     string   `json:"license"`
	Seq         int      `json:"seq"`
	Previous    string   `json:"previous"`
	Events      []string `json:"events"`
	PeriodStart int64    `json:"periodStart"`
	PeriodEnd   int64    `json:"periodEnd"`
}

func (b Batch) computeHash() (string, error) {
	return canonical.Hash(batchBody{b.License, b.Seq, b.Previous, b.Events, b.PeriodStart, b.PeriodEnd})
}

// Log is an append-only usage log stored as JSON lines.
type Log struct {
	mu      sync.Mutex
	path    string
	events  []Event
	batches []Batch
}

// Open loads a log, creating its directory if needed, and verifies the chain. A log that fails
// verification is an error, never silently repaired.
func Open(path string) (*Log, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	l := &Log{path: path}
	if err := readLines(path, func(line []byte) error {
		var e Event
		if err := json.Unmarshal(line, &e); err != nil {
			return err
		}
		l.events = append(l.events, e)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("usage: %s is not a valid log: %w", path, err)
	}
	if err := readLines(path+".batches", func(line []byte) error {
		var b Batch
		if err := json.Unmarshal(line, &b); err != nil {
			return err
		}
		l.batches = append(l.batches, b)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("usage: the batch file is not valid: %w", err)
	}
	if err := l.verify(); err != nil {
		return nil, err
	}
	return l, nil
}

func readLines(path string, handle func([]byte) error) error {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		if len(strings.TrimSpace(scanner.Text())) == 0 {
			continue
		}
		if err := handle(scanner.Bytes()); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func (l *Log) verify() error {
	prev := Zero
	for i, e := range l.events {
		if e.Seq != i+1 {
			return fmt.Errorf("usage: event %d is out of order", i+1)
		}
		if e.Prev != prev {
			return fmt.Errorf("usage: event %d does not follow the one before it: the log has been changed", e.Seq)
		}
		hash, err := e.computeHash()
		if err != nil || hash != e.Hash {
			return fmt.Errorf("usage: event %d does not match its hash: the log has been changed", e.Seq)
		}
		prev = e.Hash
	}
	previous := map[string]string{}
	count := map[string]int{}
	for _, b := range l.batches {
		count[b.License]++
		want := previous[b.License]
		if want == "" {
			want = Zero
		}
		if b.Seq != count[b.License] || b.Previous != want {
			return fmt.Errorf("usage: batch %d of license %s does not follow the one before it", b.Seq, b.License)
		}
		hash, err := b.computeHash()
		if err != nil || hash != b.Hash {
			return fmt.Errorf("usage: batch %d of license %s does not match its hash", b.Seq, b.License)
		}
		previous[b.License] = b.Hash
	}
	return nil
}

// Verify re-checks the whole chain.
func (l *Log) Verify() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.verify()
}

// Append adds an event, filling in its sequence number, link and hash, and writes it to disk.
func (l *Log) Append(e Event) (Event, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e.Seq = len(l.events) + 1
	e.Prev = Zero
	if len(l.events) > 0 {
		e.Prev = l.events[len(l.events)-1].Hash
	}
	if e.At == "" {
		e.At = time.Now().UTC().Format(time.RFC3339)
	}
	hash, err := e.computeHash()
	if err != nil {
		return Event{}, err
	}
	e.Hash = hash
	if err := appendLine(l.path, e); err != nil {
		return Event{}, err
	}
	l.events = append(l.events, e)
	return e, nil
}

func appendLine(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

// Events returns a copy of every event.
func (l *Log) Events() []Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]Event(nil), l.events...)
}

// Count returns how many allowed consultations a license has been used for. Denied attempts do
// not use up a license's quota.
func (l *Log) Count(license string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, e := range l.events {
		if e.License == license && e.Allowed {
			n++
		}
	}
	return n
}

// Batches returns the sealed batches of a license, oldest first.
func (l *Log) Batches(license string) []Batch {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []Batch
	for _, b := range l.batches {
		if b.License == license {
			out = append(out, b)
		}
	}
	return out
}

// Seal groups the allowed events of a license that are not yet in a batch into one batch, links
// it to the previous batch of that license, and records it.
func (l *Log) Seal(license string) (*Batch, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	sealed := map[string]bool{}
	previous, seq := Zero, 0
	for _, b := range l.batches {
		if b.License != license {
			continue
		}
		seq++
		previous = b.Hash
		for _, h := range b.Events {
			sealed[h] = true
		}
	}
	batch := Batch{License: license, Seq: seq + 1, Previous: previous}
	for _, e := range l.events {
		if e.License != license || !e.Allowed || sealed[e.Hash] {
			continue
		}
		batch.Events = append(batch.Events, e.Hash)
		at, err := time.Parse(time.RFC3339, e.At)
		if err != nil {
			return nil, fmt.Errorf("usage: event %d has an invalid time", e.Seq)
		}
		if batch.PeriodStart == 0 || at.Unix() < batch.PeriodStart {
			batch.PeriodStart = at.Unix()
		}
		if at.Unix() > batch.PeriodEnd {
			batch.PeriodEnd = at.Unix()
		}
	}
	if len(batch.Events) == 0 {
		return nil, errors.New("usage: there is no new usage to seal for this license")
	}
	batch.Count = len(batch.Events)
	hash, err := batch.computeHash()
	if err != nil {
		return nil, err
	}
	batch.Hash = hash
	if err := appendLine(l.path+".batches", batch); err != nil {
		return nil, err
	}
	l.batches = append(l.batches, batch)
	return &batch, nil
}
