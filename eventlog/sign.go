package eventlog

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// algorithm is the only signature algorithm a head may use.
const algorithm = "ed25519"

// headDomain starts every signed statement, so a head signature can never be
// passed off as a signature of anything else made with the same key.
const headDomain = "foundation-kit/eventlog-head/v1\n"

// Signer signs heads. A key kept in a file is a PrivateKey's Signer; a key kept
// in a key-management service is the host's own Signer, so it never leaves it.
type Signer interface {
	// KeyID names the key, as readers' root keys name it.
	KeyID() string
	// Sign returns the raw Ed25519 signature of statement.
	Sign(ctx context.Context, statement []byte) ([]byte, error)
}

// Key is a public key a reader trusts, with the ID heads name it by.
type Key struct {
	ID        string `json:"id"`
	Algorithm string `json:"algorithm"`
	// Public is the key in standard base64.
	Public string `json:"public"`
}

// Signature is the signature carried beside a head.
type Signature struct {
	Key       string `json:"key"`
	Algorithm string `json:"algorithm"`
	Value     string `json:"value"`
}

// PrivateKey is a signing key as it is kept in a file. Whoever holds the file
// can sign as its owner, so it never goes in a repository or a log.
type PrivateKey struct {
	ID        string `json:"id"`
	Algorithm string `json:"algorithm"`
	// Private is the key's seed and Public its public half, in standard base64.
	Private string `json:"private"`
	Public  string `json:"public"`
}

// GenerateKey makes a new signing key with an ID.
func GenerateKey(id string) (PrivateKey, error) {
	if id == "" || strings.ContainsAny(id, ":, \t\n") {
		return PrivateKey{}, fmt.Errorf("eventlog: a key needs an ID without spaces, colons or commas")
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return PrivateKey{}, err
	}
	return PrivateKey{ID: id, Algorithm: algorithm,
		Private: base64.StdEncoding.EncodeToString(priv.Seed()),
		Public:  base64.StdEncoding.EncodeToString(pub)}, nil
}

// Key is the public half, as a reader holds it.
func (k PrivateKey) Key() Key { return Key{ID: k.ID, Algorithm: k.Algorithm, Public: k.Public} }

// Signer signs with the key.
func (k PrivateKey) Signer() (Signer, error) {
	seed, err := base64.StdEncoding.DecodeString(k.Private)
	if k.Algorithm != algorithm || err != nil || len(seed) != ed25519.SeedSize || k.ID == "" {
		return nil, fmt.Errorf("eventlog: the key %q is not a usable %s signing key", k.ID, algorithm)
	}
	return keySigner{id: k.ID, key: ed25519.NewKeyFromSeed(seed)}, nil
}

type keySigner struct {
	id  string
	key ed25519.PrivateKey
}

func (s keySigner) KeyID() string { return s.id }
func (s keySigner) Sign(_ context.Context, statement []byte) ([]byte, error) {
	return ed25519.Sign(s.key, statement), nil
}

// ParseRoots reads root keys written as "id:base64" separated by commas: how a
// reader is told which keys to trust.
func ParseRoots(text string) ([]Key, error) {
	var keys []Key
	for _, part := range strings.Split(text, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, public, ok := strings.Cut(part, ":")
		if !ok || id == "" || public == "" {
			return nil, fmt.Errorf("eventlog: a root key is written id:base64, not %q", part)
		}
		keys = append(keys, Key{ID: id, Algorithm: algorithm, Public: public})
	}
	return keys, nil
}

func (k Key) verify(statement []byte, value string) error {
	pub, err := base64.StdEncoding.DecodeString(k.Public)
	if k.Algorithm != algorithm || err != nil || len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: the key %q is not a usable %s key", ErrUntrusted, k.ID, algorithm)
	}
	sig, err := base64.StdEncoding.DecodeString(value)
	if err != nil || !ed25519.Verify(ed25519.PublicKey(pub), statement, sig) {
		return fmt.Errorf("%w: the signature by the key %q does not match", ErrUntrusted, k.ID)
	}
	return nil
}

// signedHead is head.json: the head's exact bytes and their signature in one
// object, so a reader can never see a head with another head's signature.
type signedHead struct {
	Signature Signature       `json:"signature"`
	Head      json.RawMessage `json:"head"`
}

func signHead(ctx context.Context, s Signer, h Head) ([]byte, error) {
	doc, err := json.Marshal(h)
	if err != nil {
		return nil, err
	}
	sig, err := s.Sign(ctx, append([]byte(headDomain), doc...))
	if err != nil {
		return nil, fmt.Errorf("eventlog: signing the head: %w", err)
	}
	signature, err := json.Marshal(Signature{Key: s.KeyID(), Algorithm: algorithm, Value: base64.StdEncoding.EncodeToString(sig)})
	if err != nil {
		return nil, err
	}
	// Written by hand so the signed bytes are exactly the bytes published.
	var out bytes.Buffer
	out.WriteString(`{"signature":`)
	out.Write(signature)
	out.WriteString(`,"head":`)
	out.Write(doc)
	out.WriteString("}\n")
	return out.Bytes(), nil
}

// decodeHead reads head.json without checking its signature: for the writer,
// which reads its own store.
func decodeHead(data []byte) (signedHead, Head, error) {
	var sh signedHead
	if err := json.Unmarshal(data, &sh); err != nil || len(sh.Head) == 0 {
		return sh, Head{}, fmt.Errorf("%w: the head can't be read", ErrUntrusted)
	}
	var h Head
	dec := json.NewDecoder(bytes.NewReader(sh.Head))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&h); err != nil {
		return sh, Head{}, fmt.Errorf("%w: the head can't be read: %v", ErrUntrusted, err)
	}
	return sh, h, h.check()
}

// VerifyHead checks head.json against the root keys and returns the head. A
// head signed by no root, malformed, or expired at now is ErrUntrusted.
func VerifyHead(data []byte, roots []Key, now time.Time) (Head, error) {
	var sh signedHead
	if err := json.Unmarshal(data, &sh); err != nil {
		return Head{}, fmt.Errorf("%w: the head can't be read: %v", ErrUntrusted, err)
	}
	var root *Key
	for i := range roots {
		if roots[i].ID == sh.Signature.Key {
			root = &roots[i]
		}
	}
	if root == nil {
		return Head{}, fmt.Errorf("%w: the head is signed by the key %q, which is not a trusted root", ErrUntrusted, sh.Signature.Key)
	}
	if sh.Signature.Algorithm != algorithm {
		return Head{}, fmt.Errorf("%w: the head is signed with %q", ErrUntrusted, sh.Signature.Algorithm)
	}
	if err := root.verify(append([]byte(headDomain), sh.Head...), sh.Signature.Value); err != nil {
		return Head{}, err
	}
	// Only now is the head read: nothing in it is acted on unsigned.
	_, h, err := decodeHead(data)
	if err != nil {
		return Head{}, err
	}
	if !now.Before(h.Expires) {
		return Head{}, fmt.Errorf("%w: the head expired at %s", ErrUntrusted, h.Expires.Format(time.RFC3339))
	}
	return h, nil
}
