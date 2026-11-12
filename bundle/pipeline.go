package bundle

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/craggy-io/foundation-kit/bundle/internal/digest"
	"github.com/craggy-io/foundation-kit/bundle/internal/link"
	"github.com/craggy-io/foundation-kit/bundle/internal/manifest"
	"github.com/craggy-io/foundation-kit/bundle/internal/snapshot"
	"github.com/craggy-io/foundation-kit/kind"
)

// limits bounds each bundle. They are generous, and exist to stop a mistaken Source,
// such as a home directory, from being read into memory.
var limits = snapshot.Limits{
	MaxFiles:      10_000,
	MaxFileBytes:  16 << 20,
	MaxTotalBytes: 256 << 20,
}

// builtinOrigin is the Origin of every provider.
const builtinOrigin = "builtin"

// loader holds the state of one Load call.
type loader struct {
	diags     []Diagnostic
	kinds     map[kind.ID]kind.AnyKind // every kind seen, to catch two kinds with one ID
	compilers map[kind.ID]AnyCompiler
	prev      *Catalog // compiled values to reuse, or nil
}

// loadedBundle is one bundle that passed the Read and Validate stages.
type loadedBundle struct {
	source   Source
	identity Identity
	metadata Metadata
	manifest *manifest.Manifest
	snap     *snapshot.Snapshot
	defs     []compiledDefinition
}

func metadataOf(m *manifest.Manifest) Metadata {
	out := Metadata{Title: m.Title, Description: m.Description, Tags: slices.Clone(m.Tags)}
	if m.Trigger != nil {
		out.Trigger, _ = json.Marshal(m.Trigger) // Parse admits JSON-compatible values only.
	}
	return out
}

// compiledDefinition is one contribution after the Compile and Check stages.
type compiledDefinition struct {
	ref      Ref
	kind     kind.AnyKind
	value    any
	refs     []Ref
	assets   []Asset
	metadata Metadata
	key      compileKey
}

// compileKey identifies everything a compiler can see, so that a value compiled
// from the same inputs can be reused by a later load. A compiler may read any file
// in its bundle and in its declared dependencies, so the key uses whole-bundle
// digests rather than the contribution's own files. Origin is included because a
// compiled value may hold the Source, through an Asset.
type compileKey struct {
	origin       string
	bundle       string // the bundle's digest
	dependencies string // declared dependencies' IDs and digests
	kind         kind.ID
	contribution string
}

type loadedProvider struct {
	identity Identity
	builtins []kind.AnyBuiltin
}

func (l *loader) add(d Diagnostic) {
	if d.Severity == "" {
		d.Severity = SeverityError
	}
	l.diags = append(l.diags, d)
}

func (l *loader) failed() bool {
	return slices.ContainsFunc(l.diags, func(d Diagnostic) bool { return d.Severity == SeverityError })
}

func (l *loader) loadError() *LoadError {
	diags := slices.Clone(l.diags)
	sort.SliceStable(diags, func(i, j int) bool {
		a, b := diags[i], diags[j]
		return cmp.Or(
			cmp.Compare(a.Bundle, b.Bundle),
			cmp.Compare(a.Contribution, b.Contribution),
			cmp.Compare(a.Path, b.Path),
			cmp.Compare(a.Line, b.Line),
			cmp.Compare(a.Column, b.Column),
			cmp.Compare(a.Code, b.Code),
			cmp.Compare(a.Message, b.Message),
		) < 0
	})
	return &LoadError{Diagnostics: diags}
}

// useKind records k, and reports false if it is invalid or another kind has its ID.
func (l *loader) useKind(k kind.AnyKind, d Diagnostic) bool {
	d.Code = CodeKindInvalid
	if k == nil {
		d.Message = "kind is nil"
		l.add(d)
		return false
	}
	id := k.ID()
	if err := id.Validate(); err != nil {
		d.Message = err.Error()
		l.add(d)
		return false
	}
	if seen, ok := l.kinds[id]; ok && seen != k {
		d.Message = fmt.Sprintf("two different kinds are declared as %s; declare each kind once and share it", id)
		l.add(d)
		return false
	}
	l.kinds[id] = k
	return true
}

// checkCompilers is part of the Validate stage, for Options.Compilers.
func (l *loader) checkCompilers(compilers []AnyCompiler) {
	for _, c := range compilers {
		if c == nil {
			l.add(Diagnostic{Code: CodeKindInvalid, Message: "Options.Compilers contains a nil compiler"})
			continue
		}
		k := c.Kind()
		if !l.useKind(k, Diagnostic{}) {
			continue
		}
		if _, dup := l.compilers[k.ID()]; dup {
			l.add(Diagnostic{Code: CodeDuplicateID, Message: fmt.Sprintf("more than one compiler is given for kind %s", k.ID())})
			continue
		}
		l.compilers[k.ID()] = c
	}
}

// read runs the Locate, Read and Validate stages for one Source. It returns nil if
// the bundle can't be compiled.
func (l *loader) read(ctx context.Context, src Source) *loadedBundle {
	if src == nil {
		l.add(Diagnostic{Code: CodeSourceUnavailable, Message: "Options.Sources contains a nil Source"})
		return nil
	}
	origin := src.Origin()
	fsys, closeFn, err := src.Open(ctx)
	if err != nil {
		l.add(Diagnostic{Code: CodeSourceUnavailable, Message: fmt.Sprintf("source %s: %v", origin, err)})
		return nil
	}
	snap, snapDiags := snapshot.Take(ctx, fsys, limits)
	if err := closeFn(); err != nil {
		l.add(Diagnostic{Code: CodeSourceUnavailable, Message: fmt.Sprintf("source %s: closing: %v", origin, err)})
	}
	for _, d := range snapDiags {
		l.add(Diagnostic{Code: Code(d.Code), Path: d.Path, Message: fmt.Sprintf("source %s: %s", origin, d.Message)})
	}
	if snap == nil {
		return nil
	}

	data, ok := snap.Files()[manifest.FileName]
	if !ok {
		l.add(Diagnostic{Code: CodeManifestMissing, Message: fmt.Sprintf("source %s has no %s at its root", origin, manifest.FileName)})
		return nil
	}
	m, manifestDiags := manifest.Parse(data)
	label := m.Metadata.ID
	for _, d := range manifestDiags {
		msg := d.Message
		if label == "" {
			msg = fmt.Sprintf("source %s: %s", origin, msg)
		}
		l.add(Diagnostic{Code: Code(d.Code), Bundle: label, Contribution: d.Contribution,
			Path: manifest.FileName, Line: d.Line, Column: d.Column, Message: msg})
	}
	if len(manifestDiags) > 0 && label == "" {
		return nil
	}

	assetDigests := make(map[string]string, len(snap.Assets()))
	for p, info := range snap.Assets() {
		assetDigests[p] = info.Digest
	}
	b := &loadedBundle{
		source:   src,
		metadata: metadataOf(m),
		identity: Identity{
			ID:      label,
			Version: m.Metadata.Version,
			Digest:  digest.Bundle(snap.Files(), assetDigests),
			Origin:  origin,
		},
		manifest: m,
		snap:     snap,
	}

	valid := len(manifestDiags) == 0
	referenced := map[string]bool{}
	for _, c := range m.Entries() {
		at := Diagnostic{Bundle: label, Contribution: c.ID, Path: manifest.FileName, Line: c.Line}
		if c.Kind != "" {
			id, err := kind.ParseID(c.Kind)
			switch {
			case err != nil:
				d := at
				d.Code, d.Message = CodeKindInvalid, err.Error()
				l.add(d)
				valid = false
			case l.compilers[id] == nil:
				d := at
				d.Code = CodeKindUnknown
				d.Message = fmt.Sprintf("no compiler is given for kind %s; leave this bundle out of Options.Sources to disable it", id)
				l.add(d)
				valid = false
			}
		}
		for _, a := range c.Assets {
			referenced[a] = true
			if _, ok := snap.Assets()[a]; !ok {
				d := at
				d.Code, d.Message = CodeAssetMissing, fmt.Sprintf("asset %s doesn't exist", a)
				l.add(d)
				valid = false
			}
		}
	}
	// A partial manifest would make listed assets look unreferenced.
	if len(manifestDiags) == 0 {
		for _, p := range sortedKeys(snap.Assets()) {
			if !referenced[p] {
				l.add(Diagnostic{Code: CodeAssetUnreferenced, Bundle: label, Path: p,
					Message: fmt.Sprintf("%s is not listed by any contribution; list it under a contribution's assets, or remove it", p)})
				valid = false
			}
		}
	}
	if !valid {
		return nil
	}
	return b
}

// checkProviders runs the Validate and Check stages for built-ins.
func (l *loader) checkProviders(providers []kind.Provider) []loadedProvider {
	var out []loadedProvider
	for _, p := range providers {
		at := Diagnostic{Bundle: p.ID}
		ok := true
		bad := func(msg string) {
			d := at
			d.Code, d.Message = CodeProviderInvalid, msg
			l.add(d)
			ok = false
		}
		if !manifest.ValidID(p.ID) {
			bad(fmt.Sprintf("provider ID %q is invalid: use letters, digits, '.', '_' and '-', starting and ending with a letter or digit", p.ID))
		}
		if p.Version == "" {
			bad(fmt.Sprintf("provider %q has no Version, which is all that identifies its built-ins' content", p.ID))
		}
		seen := map[string]bool{}
		for _, b := range p.Builtins {
			if b == nil {
				bad("Builtins contains a nil built-in")
				continue
			}
			d := at
			d.Contribution = b.ID()
			switch {
			case !manifest.ValidID(b.ID()):
				bad(fmt.Sprintf("built-in ID %q is invalid", b.ID()))
				continue
			case seen[b.ID()]:
				d.Code, d.Message = CodeDuplicateID, fmt.Sprintf("provider %q has more than one built-in with ID %q", p.ID, b.ID())
				l.add(d)
				ok = false
				continue
			}
			seen[b.ID()] = true
			if !l.useKind(b.Kind(), d) {
				ok = false
				continue
			}
			if err := b.Kind().Validate(b.Value()); err != nil {
				d.Code, d.Message = CodeSchemaInvalid, err.Error()
				l.add(d)
				ok = false
			}
		}
		if ok {
			out = append(out, loadedProvider{
				identity: Identity{ID: p.ID, Version: p.Version, Origin: builtinOrigin},
				builtins: p.Builtins,
			})
		}
	}
	return out
}

// compileAll runs the Compile and Check stages, calling compilers concurrently. A
// contribution whose compileKey matches one in l.prev reuses that value instead of
// calling its compiler, but is still checked against its kind.
func (l *loader) compileAll(ctx context.Context, bundles []*loadedBundle) {
	type job struct {
		bundle *loadedBundle
		index  int
		c      Contribution
		comp   AnyCompiler
		reuse  *compiledValue
		diags  []Diagnostic
	}

	// Bundles by ID, so that a contribution can read its declared dependencies. An
	// ID that more than one bundle has is left out, because order must not pick one,
	// and the Link stage rejects it anyway.
	byID := map[string]*loadedBundle{}
	count := map[string]int{}
	for _, b := range bundles {
		byID[b.identity.ID] = b
		count[b.identity.ID]++
	}

	var jobs []*job
	for _, b := range bundles {
		files := b.snap.FS()
		deps := map[string]dependency{}
		var depKey strings.Builder
		for _, d := range b.manifest.Dependencies {
			fmt.Fprintf(&depKey, "%d:%s", len(d.ID), d.ID)
			if dep, ok := byID[d.ID]; ok && count[d.ID] == 1 {
				deps[d.ID] = dependency{files: dep.snap.FS(), identity: dep.identity}
				fmt.Fprintf(&depKey, "=%s", dep.identity.Digest)
			}
			depKey.WriteString(";")
		}
		entries := b.manifest.Entries()
		b.defs = make([]compiledDefinition, len(entries))
		for i, mc := range entries {
			id, _ := kind.ParseID(mc.Kind) // checked by read
			assets := make([]Asset, 0, len(mc.Assets))
			for _, p := range mc.Assets {
				info := b.snap.Assets()[p]
				assets = append(assets, Asset{source: b.source, path: p, size: info.Size, digest: info.Digest})
			}
			ref := Ref{Owner: b.identity.ID, ID: mc.ID}
			key := compileKey{origin: b.identity.Origin, bundle: b.identity.Digest,
				dependencies: depKey.String(), kind: id, contribution: mc.ID}
			b.defs[i] = compiledDefinition{ref: ref, kind: l.compilers[id].Kind(), assets: assets, metadata: b.metadata, key: key}
			j := &job{bundle: b, index: i, comp: l.compilers[id], c: Contribution{
				Ref: ref, Kind: id, Bundle: b.identity, Metadata: b.metadata.clone(), Path: mc.Path, Files: files, Assets: slices.Clone(assets),
				dependencies: deps,
			}}
			if l.prev != nil {
				if v, ok := l.prev.compiled[key]; ok {
					j.reuse = &v
				}
			}
			jobs = append(jobs, j)
		}
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, runtime.GOMAXPROCS(0))
	for _, j := range jobs {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			if ctx.Err() != nil {
				return
			}
			def := &j.bundle.defs[j.index]
			var out compiledValue
			if j.reuse != nil {
				out = *j.reuse
			} else {
				var diags []Diagnostic
				out, diags = safeCompile(ctx, j.comp, j.c)
				for _, d := range diags {
					d.Bundle = cmp.Or(d.Bundle, j.c.Bundle.ID)
					d.Contribution = cmp.Or(d.Contribution, j.c.Ref.ID)
					d.Code = cmp.Or(d.Code, CodeCompileFailed)
					d.Severity = cmp.Or(d.Severity, SeverityError)
					j.diags = append(j.diags, d)
				}
				if slices.ContainsFunc(j.diags, func(d Diagnostic) bool { return d.Severity == SeverityError }) {
					return
				}
			}
			if err := def.kind.Validate(out.value); err != nil {
				j.diags = append(j.diags, Diagnostic{Code: CodeSchemaInvalid, Bundle: j.c.Bundle.ID,
					Contribution: j.c.Ref.ID, Path: j.c.Path, Message: err.Error()})
				return
			}
			def.value, def.refs = out.value, slices.Clone(out.refs)
		}()
	}
	wg.Wait()
	for _, j := range jobs {
		for _, d := range j.diags {
			l.add(d)
		}
	}
}

// safeCompile calls a compiler, turning a panic into a diagnostic so that one bad
// compiler can't take down the host.
func safeCompile(ctx context.Context, comp AnyCompiler, c Contribution) (out compiledValue, diags []Diagnostic) {
	defer func() {
		if r := recover(); r != nil {
			out = compiledValue{}
			diags = []Diagnostic{{Code: CodeCompileFailed, Path: c.Path, Message: fmt.Sprintf("compiler for %s panicked: %v", c.Kind, r)}}
		}
	}()
	return comp.compile(ctx, c)
}

// link runs the Link stage.
func (l *loader) link(bundles []*loadedBundle, providers []loadedProvider, o Options) link.Result {
	in := link.Input{Global: map[string]bool{}}
	for id, k := range l.kinds {
		if k.Global() {
			in.Global[id.String()] = true
		}
	}
	for _, b := range bundles {
		owner := link.Owner{ID: b.identity.ID, Digest: b.identity.Digest, Bundle: true}
		for _, d := range b.manifest.Dependencies {
			owner.Dependencies = append(owner.Dependencies, link.Dependency{ID: d.ID, Digest: d.Digest})
		}
		in.Owners = append(in.Owners, owner)
		for _, def := range b.defs {
			node := link.Node{Ref: def.ref.String(), Kind: def.kind.ID().String(), Owner: b.identity.ID}
			for _, r := range def.refs {
				node.Refs = append(node.Refs, r.String())
			}
			in.Nodes = append(in.Nodes, node)
		}
	}
	for _, p := range providers {
		in.Owners = append(in.Owners, link.Owner{ID: p.identity.ID})
		for _, b := range p.builtins {
			in.Nodes = append(in.Nodes, link.Node{
				Ref:   Ref{Owner: p.identity.ID, ID: b.ID()}.String(),
				Kind:  b.Kind().ID().String(),
				Owner: p.identity.ID,
			})
		}
	}
	for _, ov := range o.Overrides {
		in.Overrides = append(in.Overrides, link.Override{Target: ov.Target.String(), Replacement: ov.Replacement.String()})
	}
	for _, bo := range o.BundleOverrides {
		in.OwnerOverrides = append(in.OwnerOverrides, link.Override{Target: bo.Target, Replacement: bo.Replacement})
	}

	res, diags := link.Resolve(in)
	for _, d := range diags {
		var contribution string
		if ref, err := ParseRef(d.Ref); err == nil {
			contribution = ref.ID
		}
		l.add(Diagnostic{Code: Code(d.Code), Bundle: d.Owner, Contribution: contribution, Message: d.Message, Related: d.Related})
	}
	return res
}

// accept runs the Accept stage. Everything it receives has passed every check.
func accept(bundles []*loadedBundle, providers []loadedProvider, o Options, res link.Result) *Catalog {
	c := &Catalog{
		definitions: map[Ref]storedDefinition{},
		overrides:   map[Ref]Ref{},
		explicit:    map[Ref]bool{},
		global:      map[kind.ID]map[string]Ref{},
		compiled:    map[compileKey]compiledValue{},
		layers:      map[string]string{},
		files:       map[string]fs.FS{},
	}
	in := digest.CatalogInput{
		BundleDigests: map[string]string{},
		Providers:     map[string]string{},
		Bindings:      res.Bindings,
	}
	kinds := map[string]bool{}
	globalKinds := map[kind.ID]bool{}
	for _, b := range bundles {
		c.owners = append(c.owners, b.identity)
		c.files[b.identity.ID] = b.snap.FS()
		in.BundleDigests[b.identity.ID] = b.identity.Digest
		for _, def := range b.defs {
			c.definitions[def.ref] = storedDefinition{kind: def.kind.ID(), owner: b.identity, value: def.value, assets: def.assets, metadata: def.metadata}
			c.compiled[def.key] = compiledValue{value: def.value, refs: def.refs}
			kinds[def.kind.ID().String()] = true
			globalKinds[def.kind.ID()] = def.kind.Global()
		}
	}
	for _, p := range providers {
		c.owners = append(c.owners, p.identity)
		in.Providers[p.identity.ID] = p.identity.Version
		for _, b := range p.builtins {
			ref := Ref{Owner: p.identity.ID, ID: b.ID()}
			c.definitions[ref] = storedDefinition{kind: b.Kind().ID(), owner: p.identity, value: b.Value()}
			kinds[b.Kind().ID().String()] = true
			globalKinds[b.Kind().ID()] = b.Kind().Global()
		}
	}
	for ref := range c.definitions {
		c.order = append(c.order, ref)
		in.Contributions = append(in.Contributions, ref.String())
	}

	// Apply overrides after every definition is stored, so that a replacement's own
	// definition is the one copied. Link has checked that every reference parses.
	for _, ov := range o.Overrides {
		c.explicit[ov.Target] = true
	}
	for _, bo := range o.BundleOverrides {
		c.layers[bo.Target] = bo.Replacement
	}
	replaced := map[Ref]storedDefinition{}
	for t, r := range res.Overrides {
		target, _ := ParseRef(t)
		replacement, _ := ParseRef(r)
		replaced[target] = c.definitions[replacement]
		c.overrides[target] = replacement
	}
	for target, def := range replaced {
		c.definitions[target] = def
	}

	// Index the effective definitions of global kinds. Link has checked that their
	// contribution IDs are unique.
	for ref, def := range c.definitions {
		if _, overridden := c.overrides[ref]; overridden || !globalKinds[def.kind] {
			continue
		}
		if c.global[def.kind] == nil {
			c.global[def.kind] = map[string]Ref{}
		}
		c.global[def.kind][ref.ID] = ref
	}

	slices.SortFunc(c.order, compareRefs)
	slices.SortFunc(c.owners, func(a, b Identity) int { return strings.Compare(a.ID, b.ID) })
	for k := range kinds {
		in.Kinds = append(in.Kinds, k)
	}
	c.digest = digest.Catalog(in)
	return c
}

func compareRefs(a, b Ref) int {
	return cmp.Or(strings.Compare(a.Owner, b.Owner), strings.Compare(a.ID, b.ID))
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
