// Package canonjson renders JSON in one canonical byte form, so that equal values
// hash equally however they were written.
//
// The canonical form is compact JSON with:
//
//   - object members sorted by key, in bytewise order of the UTF-8 key;
//   - numbers as exact decimals: no exponent, no trailing fractional zeros, no plus
//     sign, and -0 written as 0, so 1, 1.0, 1e0 and 10e-1 are all 1, and no digit
//     is ever lost to floating point;
//   - strings escaped as encoding/json escapes them, including <, > and & as
//     \u003c, \u003e and \u0026.
//
// This is not RFC 8785 (JCS). JCS writes numbers as IEEE 754 doubles, which loses
// precision beyond 2^53, and sorts keys by UTF-16 code units. Don't compare these
// digests with a JCS implementation's.
//
// The form is stable: a change to it is a new package, because digests computed from
// it are stored. It's byte-for-byte compatible with artifact-services' v1 canonical
// JSON.
//
// # Invariants
//
//   - Normalize(Normalize(x)) equals Normalize(x).
//   - Input with a duplicate object member, invalid UTF-8, trailing content or a
//     syntax error fails with ErrInvalid. Nothing is silently dropped.
//   - Input beyond MaxBytes or MaxDepth, or a number whose exact form would be
//     enormous, fails with ErrLimit, so hostile input can't exhaust memory.
//
// # Concurrency
//
// Normalize and Marshal are safe for concurrent use.
//
// # Exported names
//
//	Normalize, Marshal       produce the canonical form
//	MaxBytes, MaxDepth       limits on input and output
//	ErrInvalid, ErrLimit     errors to match with errors.Is
package canonjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	// MaxBytes bounds both the input and the canonical output.
	MaxBytes = 8 << 20
	// MaxDepth bounds how deeply arrays and objects may nest.
	MaxDepth = 64

	maxNumber   = 8192 // characters in a number, as written or as rendered
	maxExponent = 4096 // magnitude of a number's decimal exponent
)

var (
	// ErrInvalid reports input that isn't a single well-formed JSON value, or
	// that has a duplicate object member.
	ErrInvalid = errors.New("canonjson: invalid JSON")
	// ErrLimit reports input or output beyond this package's limits.
	ErrLimit = errors.New("canonjson: limit exceeded")
)

// Marshal encodes v with encoding/json and returns its canonical form.
func Marshal(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return Normalize(b)
}

// Normalize returns the canonical form of one JSON value.
func Normalize(data []byte) ([]byte, error) {
	if len(data) > MaxBytes {
		return nil, fmt.Errorf("%w: input exceeds %d bytes", ErrLimit, MaxBytes)
	}
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("%w: invalid UTF-8", ErrInvalid)
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	v, err := value(d, 0, &budget{})
	if err != nil {
		return nil, err
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, fmt.Errorf("%w: trailing content", ErrInvalid)
	}
	return json.Marshal(v)
}

func invalid(err error) error {
	if errors.Is(err, io.EOF) {
		err = io.ErrUnexpectedEOF
	}
	return fmt.Errorf("%w: %w", ErrInvalid, err)
}

type budget struct{ used int }

func (b *budget) add(n int) error {
	b.used += n
	if b.used > MaxBytes {
		return fmt.Errorf("%w: output exceeds %d bytes", ErrLimit, MaxBytes)
	}
	return nil
}
func (b *budget) encoded(v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return b.add(len(raw))
}

func value(d *json.Decoder, depth int, b *budget) (any, error) {
	if depth > MaxDepth {
		return nil, fmt.Errorf("%w: nesting deeper than %d", ErrLimit, MaxDepth)
	}
	token, err := d.Token()
	if err != nil {
		return nil, invalid(err)
	}
	switch t := token.(type) {
	case json.Delim:
		switch t {
		case '{':
			if err := b.add(2); err != nil {
				return nil, err
			}
			m := map[string]any{}
			for d.More() {
				k, err := d.Token()
				if err != nil {
					return nil, invalid(err)
				}
				key, ok := k.(string)
				if !ok {
					return nil, fmt.Errorf("%w: object key", ErrInvalid)
				}
				if err := b.encoded(key); err != nil {
					return nil, err
				}
				punctuation := 1
				if len(m) > 0 {
					punctuation++
				}
				if err := b.add(punctuation); err != nil {
					return nil, err
				}
				if _, exists := m[key]; exists {
					return nil, fmt.Errorf("%w: duplicate member %q", ErrInvalid, key)
				}
				v, err := value(d, depth+1, b)
				if err != nil {
					return nil, err
				}
				m[key] = v
			}
			if _, err = d.Token(); err != nil {
				return nil, invalid(err)
			}
			return m, nil
		case '[':
			if err := b.add(2); err != nil {
				return nil, err
			}
			a := []any{}
			for d.More() {
				if len(a) > 0 {
					if err := b.add(1); err != nil {
						return nil, err
					}
				}
				v, err := value(d, depth+1, b)
				if err != nil {
					return nil, err
				}
				a = append(a, v)
			}
			if _, err = d.Token(); err != nil {
				return nil, invalid(err)
			}
			return a, nil
		}
		return nil, fmt.Errorf("%w: unexpected delimiter", ErrInvalid)
	case json.Number:
		raw := string(t)
		if len(raw) > maxNumber {
			return nil, fmt.Errorf("%w: number longer than %d characters", ErrLimit, maxNumber)
		}
		if i := strings.IndexAny(raw, "eE"); i >= 0 {
			exp, e := strconv.Atoi(raw[i+1:])
			if e != nil || exp < -maxExponent || exp > maxExponent {
				return nil, fmt.Errorf("%w: number exponent beyond ±%d", ErrLimit, maxExponent)
			}
		}
		r, ok := new(big.Rat).SetString(raw)
		if !ok {
			return nil, fmt.Errorf("%w: number %q", ErrInvalid, raw)
		}
		// Finite JSON decimals have denominators containing only factors 2 and 5.
		den := new(big.Int).Set(r.Denom())
		two, five := big.NewInt(2), big.NewInt(5)
		counts := [2]int{}
		for i, f := range []*big.Int{two, five} {
			for new(big.Int).Mod(den, f).Sign() == 0 {
				den.Div(den, f)
				counts[i]++
				if counts[i] > maxExponent {
					return nil, fmt.Errorf("%w: number exponent beyond ±%d", ErrLimit, maxExponent)
				}
			}
		}
		digits := counts[0]
		if counts[1] > digits {
			digits = counts[1]
		}
		s := r.FloatString(digits)
		if strings.Contains(s, ".") {
			s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
		}
		if s == "-0" {
			s = "0"
		}
		if len(s) > maxNumber {
			return nil, fmt.Errorf("%w: number longer than %d characters", ErrLimit, maxNumber)
		}
		if err := b.add(len(s)); err != nil {
			return nil, err
		}
		return json.Number(s), nil
	default:
		if err := b.encoded(t); err != nil {
			return nil, err
		}
		return t, nil
	}
}
