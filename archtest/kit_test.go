package archtest_test

import (
	"testing"

	"github.com/craggy-io/foundation-kit/archtest"
)

// The layers of this kit, as described in AGENTS.md. Add every new package here.
// adapters/sqlite is a nested Go module with its own archtest.Check in its arch_test.go.
var (
	contracts   = []string{"./kind"}
	engines     = []string{"./bundle", "./bundle/internal/...", "./settings", "./canonjson", "./fsx", "./retryafter", "./feed"}
	stdOnly     = []string{"./canonjson", "./fsx", "./retryafter", "./feed"}
	extensions  = []string{"./bundle/compile", "./kind/jsonschema"}
	testSupport = []string{"./archtest", "./archtest/internal/...", "./bundle/bundletest"}
	shared      = []string{"./internal/..."} // module-root internal/, decided by ADR-0003
)

func TestKitLayers(t *testing.T) {
	archtest.Check(t, archtest.Config{
		Packages: concat(contracts, engines, extensions, testSupport, shared),
		Rules: []archtest.Rule{
			{
				Name:   "contracts stay cheap",
				Why:    "a contract imports only the standard library, so depending on it costs nothing",
				From:   contracts,
				Forbid: []string{"./...", archtest.External},
			},
			{
				Name:   "shared internal code is a leaf",
				Why:    "module-root internal/ is shared by several layers, so it imports no other kit package (ADR-0003)",
				From:   shared,
				Forbid: []string{"./..."},
				Allow:  shared,
			},
			{
				Name:   "engines stay below extensions",
				Why:    "imports point down only, so move the shared code into the engine or a contract",
				From:   engines,
				Forbid: extensions,
			},
			{
				Name:   "engines hide their dependencies",
				Why:    "an engine's public package must not expose a third-party type, so put the dependency in internal/",
				From:   []string{"./bundle", "./settings"},
				Forbid: []string{archtest.External},
			},
			{
				Name:   "small engines use only the standard library",
				Why:    "canonjson, fsx, retryafter and feed use nothing else, so any module can take them without pulling in anything else",
				From:   stdOnly,
				Forbid: []string{"./...", archtest.External},
			},
			{
				Name:   "bundle doesn't use settings",
				Why:    "an engine importing another engine needs an ADR",
				From:   []string{"./bundle", "./bundle/internal/..."},
				Forbid: []string{"./settings"},
			},
			{
				Name:   "settings doesn't use bundle",
				Why:    "an engine importing another engine needs an ADR",
				From:   []string{"./settings"},
				Forbid: []string{"./bundle", "./bundle/internal/..."},
			},
			{
				Name:   "bundle/compile is independent",
				Why:    "an extension must not import another extension",
				From:   []string{"./bundle/compile"},
				Forbid: []string{"./kind/jsonschema"},
			},
			{
				Name:   "kind/jsonschema is independent",
				Why:    "an extension must not import another extension",
				From:   []string{"./kind/jsonschema"},
				Forbid: []string{"./bundle/compile"},
			},
			{
				Name:   "test support stays in tests",
				Why:    "test support imports testing, so only test files and other test support may import it",
				From:   []string{"./..."},
				Except: testSupport,
				Forbid: testSupport,
			},
		},
	})
}

func concat(groups ...[]string) []string {
	var all []string
	for _, g := range groups {
		all = append(all, g...)
	}
	return all
}

// The kit's own repository rules, in the root archtest.yaml.
func TestKitRepositoryRules(t *testing.T) {
	archtest.CheckFile(t.Context(), t, "../archtest.yaml")
}
