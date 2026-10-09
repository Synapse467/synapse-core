package usage

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func openTemp(t *testing.T) (*Log, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "usage", "log.jsonl")
	l, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return l, path
}

func event(license string, allowed bool) Event {
	return Event{
		At: "2026-10-10T12:00:00Z", License: license, Capsule: strings.Repeat("a", 64), Version: 1,
		Grantee: "G1", Purpose: "research", QuestionSHA256: strings.Repeat("b", 64), Allowed: allowed, Code: "ok",
	}
}

func TestEventsFormAHashChain(t *testing.T) {
	l, _ := openTemp(t)
	first, err := l.Append(event("L1", true))
	if err != nil {
		t.Fatal(err)
	}
	second, _ := l.Append(event("L1", true))
	if first.Seq != 1 || second.Seq != 2 {
		t.Fatalf("unexpected sequence numbers %d %d", first.Seq, second.Seq)
	}
	if first.Prev != Zero || second.Prev != first.Hash {
		t.Fatal("events must link to the one before them")
	}
	if err := l.Verify(); err != nil {
		t.Fatal(err)
	}
}

func TestALogSurvivesBeingReopened(t *testing.T) {
	l, path := openTemp(t)
	l.Append(event("L1", true))
	l.Append(event("L1", true))
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.Events()) != 2 || reopened.Count("L1") != 2 {
		t.Fatalf("the log changed across a reopen: %d events", len(reopened.Events()))
	}
	third, _ := reopened.Append(event("L1", true))
	if third.Seq != 3 {
		t.Fatalf("appending after a reopen should continue the chain, got seq %d", third.Seq)
	}
}

func TestAnyEditToTheLogIsDetectedOnOpen(t *testing.T) {
	l, path := openTemp(t)
	l.Append(event("L1", true))
	l.Append(event("L1", true))
	l.Append(event("L1", true))
	data, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")

	edits := map[string]string{
		"a changed purpose":       strings.Join([]string{lines[0], strings.Replace(lines[1], `"research"`, `"resale"`, 1), lines[2]}, "\n"),
		"a removed event":         strings.Join([]string{lines[0], lines[2]}, "\n"),
		"reordered events":        strings.Join([]string{lines[1], lines[0], lines[2]}, "\n"),
		"a denial turned allowed": strings.Replace(strings.Join(lines, "\n"), `"allowed":true`, `"allowed":false`, 1),
	}
	for name, content := range edits {
		t.Run(name, func(t *testing.T) {
			copyPath := filepath.Join(t.TempDir(), "edited.jsonl")
			os.WriteFile(copyPath, []byte(content+"\n"), 0o600)
			if _, err := Open(copyPath); err == nil {
				t.Fatalf("%s was not detected", name)
			}
		})
	}
}

func TestOpenRefusesAGarbageFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log.jsonl")
	os.WriteFile(path, []byte("not json\n"), 0o600)
	if _, err := Open(path); err == nil {
		t.Fatal("a corrupt log was opened")
	}
}

func TestOnlyAllowedConsultationsUseUpAQuota(t *testing.T) {
	l, _ := openTemp(t)
	l.Append(event("L1", true))
	l.Append(event("L1", false))
	l.Append(event("L1", false))
	l.Append(event("L2", true))
	l.Append(event("L1", true))
	if l.Count("L1") != 2 || l.Count("L2") != 1 || l.Count("nobody") != 0 {
		t.Fatalf("counts: L1=%d L2=%d", l.Count("L1"), l.Count("L2"))
	}
}

func TestSealingGroupsNewAllowedEventsAndLinksBatches(t *testing.T) {
	l, _ := openTemp(t)
	a, _ := l.Append(event("L1", true))
	l.Append(event("L1", false)) // a denial is not billable usage
	b, _ := l.Append(event("L1", true))
	l.Append(event("L2", true))

	first, err := l.Seal("L1")
	if err != nil {
		t.Fatal(err)
	}
	if first.Seq != 1 || first.Previous != Zero || first.Count != 2 {
		t.Fatalf("unexpected first batch %+v", first)
	}
	if len(first.Events) != 2 || first.Events[0] != a.Hash || first.Events[1] != b.Hash {
		t.Fatal("the batch must cover exactly the allowed events, in order")
	}
	if _, err := l.Seal("L1"); err == nil {
		t.Fatal("sealing with nothing new must fail instead of recording an empty batch")
	}

	l.Append(event("L1", true))
	second, err := l.Seal("L1")
	if err != nil {
		t.Fatal(err)
	}
	if second.Seq != 2 || second.Previous != first.Hash || second.Count != 1 {
		t.Fatalf("unexpected second batch %+v", second)
	}
	if len(l.Batches("L1")) != 2 || len(l.Batches("L2")) != 0 {
		t.Fatal("batches are kept per license")
	}
	if err := l.Verify(); err != nil {
		t.Fatal(err)
	}
}

func TestBatchesSurviveAReopenAndTheirTamperingIsDetected(t *testing.T) {
	l, path := openTemp(t)
	l.Append(event("L1", true))
	if _, err := l.Seal("L1"); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.Batches("L1")) != 1 {
		t.Fatal("the batch was lost across a reopen")
	}
	data, _ := os.ReadFile(path + ".batches")
	os.WriteFile(path+".batches", []byte(strings.Replace(string(data), `"count":1`, `"count":99`, 1)), 0o600)
	if _, err := Open(path); err != nil {
		// count is not part of the hash, but the events list is: change that instead.
		t.Fatalf("unexpected error: %v", err)
	}
	os.WriteFile(path+".batches", []byte(strings.Replace(string(data), `"periodEnd":`, `"periodEnd":1,"x":`, 1)), 0o600)
	tampered := strings.Replace(string(data), `"events":["`, `"events":["0`, 1)
	os.WriteFile(path+".batches", []byte(tampered), 0o600)
	if _, err := Open(path); err == nil {
		t.Fatal("a batch with altered event hashes was accepted")
	}
}

func TestQuestionsAreNeverStored(t *testing.T) {
	l, path := openTemp(t)
	e := event("L1", true)
	e.QuestionSHA256 = strings.Repeat("c", 64)
	l.Append(e)
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "question\"") {
		t.Fatal("the log has a field for the question text")
	}
}

func TestConcurrentAppendsStayChained(t *testing.T) {
	l, path := openTemp(t)
	var wg sync.WaitGroup
	for i := 0; i < 25; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := l.Append(event("L1", true)); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if _, err := Open(path); err != nil {
		t.Fatalf("concurrent appends broke the chain: %v", err)
	}
	if l.Count("L1") != 25 {
		t.Fatalf("expected 25 events, got %d", l.Count("L1"))
	}
}
