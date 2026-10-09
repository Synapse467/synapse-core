package home

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSynapseHomeOverridesTheDefault(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SYNAPSE_HOME", dir)
	if Dir() != dir {
		t.Fatalf("got %s, want %s", Dir(), dir)
	}
}

func TestDefaultNeedsNoConfiguration(t *testing.T) {
	t.Setenv("SYNAPSE_HOME", "")
	if Dir() == "" || filepath.Base(Dir()) == "" {
		t.Fatal("no default directory")
	}
}

func TestEnsureCreatesNestedDirectories(t *testing.T) {
	t.Setenv("SYNAPSE_HOME", filepath.Join(t.TempDir(), "state"))
	path, err := Ensure("usage", "a")
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		t.Fatalf("directory not created: %v", err)
	}
}
