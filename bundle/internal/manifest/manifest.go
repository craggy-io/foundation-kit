// Package manifest parses and validates bundle.yaml. It knows the manifest's fields
// only: it never interprets a kind or opens a contribution's file.
package manifest

import (
	"fmt"
	"io/fs"
	"regexp"
	"strings"

	"github.com/craggy-io/foundation-kit/bundle/internal/strictyaml"
)

const (
	// FileName is the manifest's name. It must be at the root of the bundle.
	FileName = "bundle.yaml"

	// APIVersion is the only manifest format version supported so far.
	APIVersion = "craggy.io/bundle/v1"

	// AssetsDir is the directory at the bundle root that holds assets.
	AssetsDir = "assets"
)

// Codes reported by this package. They match the codes declared in
// bundle/diagnostic.go.
const (
	codeInvalid       = "bundle.manifest_invalid"
	codePathForbidden = "bundle.path_forbidden"
	codeDuplicateID   = "bundle.duplicate_id"
)

// idPattern is the form of bundle, dependency and contribution IDs. It excludes "#",
// which separates the parts of a reference, and "/" and spaces, so that an ID can
// be used safely in paths and logs.
var idPattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9._-]*[A-Za-z0-9])?$`)

// digestPattern is the form produced by internal/digest.
var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// ValidID reports whether id can be a bundle, provider or contribution ID.
func ValidID(id string) bool { return idPattern.MatchString(id) }

// Manifest is the parsed form of bundle.yaml.
type Manifest struct {
	APIVersion    string         `yaml:"apiVersion"`
	Metadata      Metadata       `yaml:"metadata"`
	Dependencies  []Dependency   `yaml:"dependencies,omitempty"`
	Contributions []Contribution `yaml:"contributions"`
}

// Metadata identifies the bundle.
type Metadata struct {
	ID      string `yaml:"id"`              // required; the bundle's identity
	Version string `yaml:"version"`         // required; the author's release label
	Title   string `yaml:"title,omitempty"` // optional
}

// Dependency declares another bundle that this one's contributions may reference.
type Dependency struct {
	ID     string `yaml:"id"`               // required
	Digest string `yaml:"digest,omitempty"` // optional pin to exact content
}

// Contribution declares one entry in the bundle.
type Contribution struct {
	ID     string   `yaml:"id"`               // required; unique within the bundle
	Kind   string   `yaml:"kind"`             // required; kind.ParseID validates its form
	Path   string   `yaml:"path"`             // required; relative to the bundle root, outside assets/
	Assets []string `yaml:"assets,omitempty"` // optional; relative to the bundle root, under assets/

	// Line is where the contribution starts in bundle.yaml, for diagnostics.
	Line int `yaml:"-"`
}

// Diagnostic is a manifest problem. The bundle package converts it to a
// bundle.Diagnostic.
type Diagnostic struct {
	Code         string // one of the code strings declared in bundle/diagnostic.go
	Contribution string // the contribution's ID, if the problem is in one
	Line         int
	Column       int
	Message      string
}

// Parse decodes and validates a manifest, reporting every problem it can find.
//
// It accepts the restricted, JSON-compatible subset of YAML described in package
// strictyaml, and rejects unknown fields. Whether a listed asset exists is checked
// by Load, because this package never opens a contribution's files. If any
// diagnostic is returned, the Manifest may be partial and must not be used except to
// label diagnostics.
func Parse(data []byte) (*Manifest, []Diagnostic) {
	root, yamlDiags := strictyaml.Parse(data)
	p := &parser{}
	for _, d := range yamlDiags {
		p.diags = append(p.diags, Diagnostic{Code: codeInvalid, Line: d.Line, Column: d.Column, Message: d.Message})
	}
	if len(yamlDiags) > 0 {
		return &Manifest{}, p.diags
	}
	if root == nil {
		p.diags = append(p.diags, Diagnostic{Code: codeInvalid, Line: 1, Column: 1, Message: "bundle.yaml is empty"})
		return &Manifest{}, p.diags
	}

	m := &Manifest{}
	var sawAPIVersion, sawMetadata bool
	p.fields(root, "the manifest", map[string]func(*strictyaml.Node){
		"apiVersion": func(n *strictyaml.Node) { sawAPIVersion = true; m.APIVersion = p.str(n, "apiVersion") },
		"metadata": func(n *strictyaml.Node) {
			sawMetadata = true
			p.metadata(n, &m.Metadata)
		},
		"dependencies": func(n *strictyaml.Node) {
			p.each(n, "dependencies", func(item *strictyaml.Node, at string) {
				m.Dependencies = append(m.Dependencies, p.dependency(item, at))
			})
		},
		"contributions": func(n *strictyaml.Node) {
			p.each(n, "contributions", func(item *strictyaml.Node, at string) {
				m.Contributions = append(m.Contributions, p.contribution(item, at))
			})
		},
	})

	switch {
	case !sawAPIVersion:
		p.at(root, codeInvalid, "", "apiVersion is required")
	case m.APIVersion != APIVersion && m.APIVersion != "":
		p.at(root, codeInvalid, "", fmt.Sprintf("apiVersion %q is not supported, want %q", m.APIVersion, APIVersion))
	}
	if !sawMetadata {
		p.at(root, codeInvalid, "", "metadata is required")
	}
	p.checkDependencies(m)
	p.checkContributions(m)
	return m, p.diags
}

// Encode writes m as bundle.yaml, in the order Parse reads it. Comments and line
// numbers are not kept.
func Encode(m *Manifest) ([]byte, error) { return strictyaml.Marshal(m) }

type parser struct {
	diags []Diagnostic

	// lines remembers where each dependency and contribution started.
	depLines []int
}

func (p *parser) at(n *strictyaml.Node, code, contribution, msg string) {
	p.diags = append(p.diags, Diagnostic{Code: code, Contribution: contribution, Line: n.Line, Column: n.Column, Message: msg})
}

// fields decodes a mapping, calling the handler for each known key and reporting
// unknown ones.
func (p *parser) fields(n *strictyaml.Node, what string, handlers map[string]func(*strictyaml.Node)) {
	if n.Kind != strictyaml.Mapping {
		p.at(n, codeInvalid, "", fmt.Sprintf("%s must be a mapping, not %s", what, n.Kind))
		return
	}
	for _, pair := range n.Pairs {
		h, ok := handlers[pair.Key]
		if !ok {
			p.diags = append(p.diags, Diagnostic{Code: codeInvalid, Line: pair.Line, Column: pair.Column,
				Message: fmt.Sprintf("unknown field %q in %s", pair.Key, what)})
			continue
		}
		h(pair.Value)
	}
}

func (p *parser) str(n *strictyaml.Node, field string) string {
	if n.Kind != strictyaml.String {
		p.at(n, codeInvalid, "", fmt.Sprintf("%s must be a string, not %s; quote it if it should be text", field, n.Kind))
		return ""
	}
	return n.Value
}

func (p *parser) each(n *strictyaml.Node, field string, fn func(item *strictyaml.Node, at string)) {
	if n.Kind == strictyaml.Null {
		return
	}
	if n.Kind != strictyaml.Sequence {
		p.at(n, codeInvalid, "", fmt.Sprintf("%s must be a sequence, not %s", field, n.Kind))
		return
	}
	for i, item := range n.Items {
		fn(item, fmt.Sprintf("%s[%d]", field, i))
	}
}

func (p *parser) metadata(n *strictyaml.Node, md *Metadata) {
	var sawID, sawVersion bool
	p.fields(n, "metadata", map[string]func(*strictyaml.Node){
		"id":      func(v *strictyaml.Node) { sawID = true; md.ID = p.str(v, "metadata.id") },
		"version": func(v *strictyaml.Node) { sawVersion = true; md.Version = p.str(v, "metadata.version") },
		"title":   func(v *strictyaml.Node) { md.Title = p.str(v, "metadata.title") },
	})
	if n.Kind != strictyaml.Mapping {
		return
	}
	if !sawID {
		p.at(n, codeInvalid, "", "metadata.id is required")
	} else if md.ID != "" && !ValidID(md.ID) {
		p.at(n, codeInvalid, "", fmt.Sprintf("metadata.id %q is invalid: %s", md.ID, idRule))
	} else if md.ID == "" {
		p.at(n, codeInvalid, "", "metadata.id must not be empty")
	}
	if !sawVersion {
		p.at(n, codeInvalid, "", "metadata.version is required")
	} else if md.Version == "" {
		p.at(n, codeInvalid, "", "metadata.version must not be empty")
	}
}

const idRule = "use letters, digits, '.', '_' and '-', starting and ending with a letter or digit"

func (p *parser) dependency(n *strictyaml.Node, at string) Dependency {
	var d Dependency
	p.fields(n, at, map[string]func(*strictyaml.Node){
		"id":     func(v *strictyaml.Node) { d.ID = p.str(v, at+".id") },
		"digest": func(v *strictyaml.Node) { d.Digest = p.str(v, at+".digest") },
	})
	p.depLines = append(p.depLines, n.Line)
	if n.Kind != strictyaml.Mapping {
		return d
	}
	switch {
	case d.ID == "":
		p.at(n, codeInvalid, "", at+".id is required")
	case !ValidID(d.ID):
		p.at(n, codeInvalid, "", fmt.Sprintf("%s.id %q is invalid: %s", at, d.ID, idRule))
	}
	if d.Digest != "" && !digestPattern.MatchString(d.Digest) {
		p.at(n, codeInvalid, "", fmt.Sprintf("%s.digest %q is invalid: want sha256:<64 lower-case hex digits>", at, d.Digest))
	}
	return d
}

func (p *parser) contribution(n *strictyaml.Node, at string) Contribution {
	c := Contribution{Line: n.Line}
	var assetNodes []*strictyaml.Node
	p.fields(n, at, map[string]func(*strictyaml.Node){
		"id":   func(v *strictyaml.Node) { c.ID = p.str(v, at+".id") },
		"kind": func(v *strictyaml.Node) { c.Kind = p.str(v, at+".kind") },
		"path": func(v *strictyaml.Node) { c.Path = p.str(v, at+".path") },
		"assets": func(v *strictyaml.Node) {
			p.each(v, at+".assets", func(item *strictyaml.Node, itemAt string) {
				c.Assets = append(c.Assets, p.str(item, itemAt))
				assetNodes = append(assetNodes, item)
			})
		},
	})
	if n.Kind != strictyaml.Mapping {
		return c
	}
	switch {
	case c.ID == "":
		p.at(n, codeInvalid, "", at+".id is required")
	case !ValidID(c.ID):
		p.at(n, codeInvalid, c.ID, fmt.Sprintf("%s.id %q is invalid: %s", at, c.ID, idRule))
	}
	if c.Kind == "" {
		p.at(n, codeInvalid, c.ID, at+".kind is required")
	}
	switch {
	case c.Path == "":
		p.at(n, codeInvalid, c.ID, at+".path is required")
	case !local(c.Path):
		p.at(n, codePathForbidden, c.ID, fmt.Sprintf("%s.path %q must be a slash-separated path inside the bundle", at, c.Path))
	case inAssets(c.Path) || c.Path == AssetsDir:
		p.at(n, codePathForbidden, c.ID, fmt.Sprintf("%s.path %q must be outside %s/", at, c.Path, AssetsDir))
	case c.Path == FileName:
		p.at(n, codePathForbidden, c.ID, fmt.Sprintf("%s.path must not be the manifest", at))
	}
	seen := map[string]bool{}
	for i, a := range c.Assets {
		an := assetNodes[i]
		itemAt := fmt.Sprintf("%s.assets[%d]", at, i)
		switch {
		case a == "":
			// Already reported by str, or an empty string.
			if an.Kind == strictyaml.String {
				p.at(an, codeInvalid, c.ID, itemAt+" must not be empty")
			}
		case !local(a):
			p.at(an, codePathForbidden, c.ID, fmt.Sprintf("%s %q must be a slash-separated path inside the bundle", itemAt, a))
		case !inAssets(a):
			p.at(an, codePathForbidden, c.ID, fmt.Sprintf("%s %q must be under %s/", itemAt, a, AssetsDir))
		case seen[a]:
			p.at(an, codeDuplicateID, c.ID, fmt.Sprintf("%s %q is listed twice", itemAt, a))
		}
		seen[a] = true
	}
	return c
}

func (p *parser) checkDependencies(m *Manifest) {
	seen := map[string]bool{}
	for i, d := range m.Dependencies {
		if d.ID == "" {
			continue
		}
		line := p.depLines[i]
		if d.ID == m.Metadata.ID {
			p.diags = append(p.diags, Diagnostic{Code: codeInvalid, Line: line,
				Message: fmt.Sprintf("dependencies[%d]: a bundle cannot depend on itself", i)})
		}
		if seen[d.ID] {
			p.diags = append(p.diags, Diagnostic{Code: codeDuplicateID, Line: line,
				Message: fmt.Sprintf("dependency %q is declared twice", d.ID)})
		}
		seen[d.ID] = true
	}
}

func (p *parser) checkContributions(m *Manifest) {
	first := map[string]int{}
	for _, c := range m.Contributions {
		if c.ID == "" {
			continue
		}
		if line, dup := first[c.ID]; dup {
			p.diags = append(p.diags, Diagnostic{Code: codeDuplicateID, Contribution: c.ID, Line: c.Line,
				Message: fmt.Sprintf("contribution ID %q is already used on line %d", c.ID, line)})
			continue
		}
		first[c.ID] = c.Line
	}
}

// local reports whether path is a clean, slash-separated path inside the bundle.
func local(path string) bool {
	return fs.ValidPath(path) && path != "." && !strings.Contains(path, `\`)
}

func inAssets(path string) bool { return strings.HasPrefix(path, AssetsDir+"/") }
