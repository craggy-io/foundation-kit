// Package manifest parses and validates bundle.yaml. It knows the manifest's fields
// only: it never interprets a kind or opens a contribution's file.
package manifest

const (
	// FileName is the manifest's name. It must be at the root of the bundle.
	FileName = "bundle.yaml"

	// APIVersion is the only manifest format version supported so far.
	APIVersion = "craggy.io/bundle/v1"
)

// Manifest is the parsed form of bundle.yaml.
type Manifest struct {
	APIVersion    string         `yaml:"apiVersion"`
	Metadata      Metadata       `yaml:"metadata"`
	Dependencies  []Dependency   `yaml:"dependencies"`
	Contributions []Contribution `yaml:"contributions"`
}

// Metadata identifies the bundle.
type Metadata struct {
	ID      string `yaml:"id"`      // required; the bundle's identity
	Version string `yaml:"version"` // required; the author's release label
	Title   string `yaml:"title"`   // optional
}

// Dependency declares another bundle that this one's contributions may reference.
type Dependency struct {
	ID     string `yaml:"id"`     // required
	Digest string `yaml:"digest"` // optional pin to exact content
}

// Contribution declares one entry in the bundle.
type Contribution struct {
	ID     string   `yaml:"id"`     // required; unique within the bundle
	Kind   string   `yaml:"kind"`   // required; kind.ParseID validates its form
	Path   string   `yaml:"path"`   // required; relative to the bundle root, outside assets/
	Assets []string `yaml:"assets"` // optional; relative to the bundle root, under assets/
}

// Diagnostic is a manifest problem. The bundle package converts it to a
// bundle.Diagnostic.
type Diagnostic struct {
	Code    string // one of the code strings declared in bundle/diagnostic.go
	Line    int
	Column  int
	Message string
}

// Parse decodes and validates a manifest, reporting every problem it can find.
//
// TODO: implement. Choose the YAML library when this is written, and keep the
// dependency inside this package so it can be swapped without an API change.
//
//   - Accept a restricted, JSON-compatible subset of YAML: reject duplicate keys,
//     custom tags, aliases, merge keys and unknown fields.
//   - Check that APIVersion matches the constant above.
//   - Require the fields marked required, and check that contribution IDs are unique.
//   - Require each Path to be local (fs.ValidPath and not ".") and outside assets/.
//   - Require each entry in Assets to be local and under assets/, with no duplicates
//     within a contribution. Whether the file exists is checked by Load, because this
//     package never opens a contribution's files.
//   - Keep line and column numbers for diagnostics.
func Parse(data []byte) (*Manifest, []Diagnostic) {
	panic("manifest.Parse: not implemented")
}
