// Package canonical produces the one byte sequence that represents a JSON value, so that a hash
// of it can be reproduced by any implementation in any language.
//
// The rules (also written down in SPEC.md):
//   - no whitespace between tokens;
//   - object keys are sorted by their UTF-8 bytes;
//   - strings are UTF-8, and only the double quote, the backslash and control characters below
//     U+0020 are escaped (\b \f \n \r \t, otherwise \u00xx with lower-case hex). Everything else,
//     including <, > and & and non-ASCII text, is written as is;
//   - numbers must be integers in the range +/-(2^53 - 1). Floats are rejected, so that no two
//     languages can disagree about how to print one;
//   - true, false and null as usual.
package canonical

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"unicode/utf8"
)

const maxSafeInteger = 1<<53 - 1

// Marshal returns the canonical JSON encoding of v. v can be anything encoding/json accepts.
func Marshal(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var tree any
	if err := decoder.Decode(&tree); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := write(&out, tree); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// Hash returns the lower-case hex SHA-256 of the canonical encoding of v.
func Hash(v any) (string, error) {
	data, err := Marshal(v)
	if err != nil {
		return "", err
	}
	return HashBytes(data), nil
}

// HashBytes returns the lower-case hex SHA-256 of data.
func HashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func write(out *bytes.Buffer, value any) error {
	switch typed := value.(type) {
	case nil:
		out.WriteString("null")
	case bool:
		if typed {
			out.WriteString("true")
		} else {
			out.WriteString("false")
		}
	case json.Number:
		return writeNumber(out, typed)
	case string:
		return writeString(out, typed)
	case []any:
		out.WriteByte('[')
		for i, item := range typed {
			if i > 0 {
				out.WriteByte(',')
			}
			if err := write(out, item); err != nil {
				return err
			}
		}
		out.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		out.WriteByte('{')
		for i, key := range keys {
			if i > 0 {
				out.WriteByte(',')
			}
			if err := writeString(out, key); err != nil {
				return err
			}
			out.WriteByte(':')
			if err := write(out, typed[key]); err != nil {
				return err
			}
		}
		out.WriteByte('}')
	default:
		return fmt.Errorf("canonical: unsupported value of type %T", value)
	}
	return nil
}

func writeNumber(out *bytes.Buffer, number json.Number) error {
	text := number.String()
	if strings.ContainsAny(text, ".eE") {
		// "1.0" and "1e3" are integers to some languages and floats to others. Refuse both.
		return fmt.Errorf("canonical: %s is not an integer; only integers are allowed", text)
	}
	value, ok := new(big.Int).SetString(text, 10)
	if !ok {
		return fmt.Errorf("canonical: %s is not a valid integer", text)
	}
	if value.CmpAbs(big.NewInt(maxSafeInteger)) > 0 {
		return errors.New("canonical: integer is outside +/-(2^53 - 1)")
	}
	out.WriteString(value.String())
	return nil
}

func writeString(out *bytes.Buffer, text string) error {
	if !utf8.ValidString(text) {
		return errors.New("canonical: string is not valid UTF-8")
	}
	out.WriteByte('"')
	for _, r := range text {
		switch {
		case r == '"':
			out.WriteString(`\"`)
		case r == '\\':
			out.WriteString(`\\`)
		case r == '\b':
			out.WriteString(`\b`)
		case r == '\f':
			out.WriteString(`\f`)
		case r == '\n':
			out.WriteString(`\n`)
		case r == '\r':
			out.WriteString(`\r`)
		case r == '\t':
			out.WriteString(`\t`)
		case r < 0x20:
			fmt.Fprintf(out, `\u%04x`, r)
		default:
			out.WriteRune(r)
		}
	}
	out.WriteByte('"')
	return nil
}
