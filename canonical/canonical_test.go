package canonical

import (
	"encoding/json"
	"os"
	"testing"
)

type vectorFile struct {
	Valid []struct {
		Name      string `json:"name"`
		Input     string `json:"input"`
		Canonical string `json:"canonical"`
		SHA256    string `json:"sha256"`
	} `json:"valid"`
	Invalid []struct {
		Name  string `json:"name"`
		Input string `json:"input"`
	} `json:"invalid"`
}

func loadVectors(t *testing.T) vectorFile {
	t.Helper()
	data, err := os.ReadFile("../testdata/vectors/canonical.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors vectorFile
	if err := json.Unmarshal(data, &vectors); err != nil {
		t.Fatal(err)
	}
	if len(vectors.Valid) == 0 || len(vectors.Invalid) == 0 {
		t.Fatal("the vector file is empty")
	}
	return vectors
}

func TestValidVectors(t *testing.T) {
	for _, vector := range loadVectors(t).Valid {
		t.Run(vector.Name, func(t *testing.T) {
			var value any
			decoder := json.NewDecoder(stringsReader(vector.Input))
			decoder.UseNumber()
			if err := decoder.Decode(&value); err != nil {
				t.Fatal(err)
			}
			got, err := Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != vector.Canonical {
				t.Fatalf("canonical form\n got  %s\n want %s", got, vector.Canonical)
			}
			if hash := HashBytes(got); hash != vector.SHA256 {
				t.Fatalf("hash %s, want %s", hash, vector.SHA256)
			}
		})
	}
}

func TestInvalidVectorsAreRejected(t *testing.T) {
	for _, vector := range loadVectors(t).Invalid {
		t.Run(vector.Name, func(t *testing.T) {
			var value any
			decoder := json.NewDecoder(stringsReader(vector.Input))
			decoder.UseNumber()
			if err := decoder.Decode(&value); err != nil {
				t.Fatal(err)
			}
			if out, err := Marshal(value); err == nil {
				t.Fatalf("expected an error, got %s", out)
			}
		})
	}
}

func TestStructsAreCanonicalizedThroughTheirJSONTags(t *testing.T) {
	type inner struct {
		Z int    `json:"z"`
		A string `json:"a"`
	}
	type outer struct {
		Name  string `json:"name"`
		Inner inner  `json:"inner"`
		Skip  string `json:"skip,omitempty"`
	}
	got, err := Marshal(outer{Name: "n", Inner: inner{Z: 2, A: "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"inner":{"a":"x","z":2},"name":"n"}`; string(got) != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestTheSameValueAlwaysHashesTheSame(t *testing.T) {
	a, _ := Hash(map[string]any{"x": 1, "y": []string{"a", "b"}})
	b, _ := Hash(map[string]any{"y": []string{"a", "b"}, "x": 1})
	if a != b {
		t.Fatal("key order changed the hash")
	}
	c, _ := Hash(map[string]any{"x": 1, "y": []string{"b", "a"}})
	if a == c {
		t.Fatal("array order must change the hash")
	}
}

func TestInvalidUTF8FromGoValuesIsReplacedDeterministically(t *testing.T) {
	// encoding/json replaces invalid UTF-8 with U+FFFD before canonicalization sees the string,
	// so the result is valid UTF-8 and the same on every run. (Other implementations must
	// reject invalid UTF-8 in their input, as SPEC.md requires.)
	got, err := Marshal(map[string]any{"s": string([]byte{0xff, 0xfe})})
	if err != nil {
		t.Fatal(err)
	}
	if want := "{\"s\":\"��\"}"; string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestFloatsAreRejected(t *testing.T) {
	if _, err := Marshal(map[string]any{"f": 0.5}); err == nil {
		t.Fatal("expected an error for a float")
	}
}
