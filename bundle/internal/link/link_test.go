package link

import (
	"maps"
	"testing"
)

const pin = "sha256:2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"

func base() Input {
	return Input{
		Owners: []Owner{
			{ID: "app", Bundle: true, Digest: "sha256:app", Dependencies: []Dependency{{ID: "shared", Digest: pin}}},
			{ID: "shared", Bundle: true, Digest: pin},
			{ID: "defaults"},
		},
		Nodes: []Node{
			{Ref: "app#team", Kind: "a.team@1", Owner: "app", Refs: []string{"app#helper", "shared#prompt"}},
			{Ref: "app#helper", Kind: "a.script@1", Owner: "app"},
			{Ref: "shared#prompt", Kind: "a.prompt@1", Owner: "shared"},
			{Ref: "defaults#prompt", Kind: "a.prompt@1", Owner: "defaults"},
		},
	}
}

func TestResolve(t *testing.T) {
	res, diags := Resolve(base())
	if len(diags) != 0 {
		t.Fatalf("diagnostics = %+v", diags)
	}
	bindings := res.Bindings
	want := map[string]string{"app#helper": "app#helper", "shared#prompt": "shared#prompt"}
	if !maps.Equal(bindings, want) {
		t.Errorf("bindings = %v, want %v", bindings, want)
	}
}

func TestResolveAppliesOverrides(t *testing.T) {
	in := base()
	in.Overrides = []Override{{Target: "shared#prompt", Replacement: "defaults#prompt"}}
	res, diags := Resolve(in)
	if len(diags) != 0 {
		t.Fatalf("diagnostics = %+v", diags)
	}
	if res.Bindings["shared#prompt"] != "defaults#prompt" || res.Overrides["shared#prompt"] != "defaults#prompt" {
		t.Errorf("result = %+v", res)
	}
}

func TestResolveRejects(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Input)
		code   string
	}{
		{"duplicate owner", func(in *Input) { in.Owners = append(in.Owners, Owner{ID: "shared", Bundle: true}) }, codeDuplicateID},
		{"duplicate ref", func(in *Input) { in.Nodes = append(in.Nodes, Node{Ref: "app#helper", Kind: "a.x@1", Owner: "app"}) }, codeDuplicateID},
		{"missing ref", func(in *Input) { in.Nodes[0].Refs = append(in.Nodes[0].Refs, "app#nothing") }, codeReferenceMissing},
		{"malformed ref", func(in *Input) { in.Nodes[0].Refs = append(in.Nodes[0].Refs, "nothing") }, codeReferenceMissing},
		{"undeclared dependency", func(in *Input) { in.Nodes[0].Refs = append(in.Nodes[0].Refs, "defaults#prompt") }, codeDependencyUndeclared},
		{"missing dependency", func(in *Input) { in.Owners[0].Dependencies = append(in.Owners[0].Dependencies, Dependency{ID: "gone"}) }, codeDependencyMissing},
		{"pin mismatch", func(in *Input) { in.Owners[1].Digest = "sha256:other" }, codeDependencyMismatch},
		{"pinned provider", func(in *Input) {
			in.Owners[0].Dependencies = append(in.Owners[0].Dependencies, Dependency{ID: "defaults", Digest: pin})
		}, codeDependencyMismatch},
		{"override of itself", func(in *Input) { in.Overrides = []Override{{"app#helper", "app#helper"}} }, codeOverrideInvalid},
		{"override target missing", func(in *Input) { in.Overrides = []Override{{"app#gone", "app#helper"}} }, codeOverrideInvalid},
		{"override replacement missing", func(in *Input) { in.Overrides = []Override{{"app#helper", "app#gone"}} }, codeOverrideInvalid},
		{"override kind mismatch", func(in *Input) { in.Overrides = []Override{{"app#helper", "shared#prompt"}} }, codeOverrideInvalid},
		{"target overridden twice", func(in *Input) {
			in.Nodes = append(in.Nodes, Node{Ref: "app#other", Kind: "a.prompt@1", Owner: "app"})
			in.Overrides = []Override{{"shared#prompt", "defaults#prompt"}, {"shared#prompt", "app#other"}}
		}, codeOverrideInvalid},
		{"chained override", func(in *Input) {
			in.Nodes = append(in.Nodes, Node{Ref: "app#other", Kind: "a.prompt@1", Owner: "app"})
			in.Overrides = []Override{{"shared#prompt", "defaults#prompt"}, {"defaults#prompt", "app#other"}}
		}, codeOverrideInvalid},
		{"bundle override of itself", func(in *Input) { in.OwnerOverrides = []Override{{"shared", "shared"}} }, codeOverrideInvalid},
		{"bundle override target unknown", func(in *Input) { in.OwnerOverrides = []Override{{"gone", "shared"}} }, codeOverrideInvalid},
		{"bundle override replacement unknown", func(in *Input) { in.OwnerOverrides = []Override{{"shared", "gone"}} }, codeOverrideInvalid},
		{"bundle override target twice", func(in *Input) {
			in.OwnerOverrides = []Override{{"shared", "defaults"}, {"shared", "app"}}
		}, codeOverrideInvalid},
		{"bundle override cycle", func(in *Input) {
			in.OwnerOverrides = []Override{{"shared", "defaults"}, {"defaults", "app"}, {"app", "shared"}}
		}, codeOverrideInvalid},
		{"bundle override kind mismatch", func(in *Input) {
			in.Nodes = append(in.Nodes, Node{Ref: "defaults#helper", Kind: "a.prompt@1", Owner: "defaults"})
			in.OwnerOverrides = []Override{{"app", "defaults"}}
		}, codeOverrideInvalid},
		{"bundle override and explicit override of one target", func(in *Input) {
			in.Nodes = append(in.Nodes, Node{Ref: "app#prompt", Kind: "a.prompt@1", Owner: "app"})
			in.Overrides = []Override{{"shared#prompt", "app#prompt"}}
			in.OwnerOverrides = []Override{{"shared", "defaults"}}
		}, codeOverrideInvalid},
		{"global ID conflict", func(in *Input) { in.Global = map[string]bool{"a.prompt@1": true} }, codeIDConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := base()
			tt.change(&in)
			res, diags := Resolve(in)
			if res.Bindings != nil || res.Overrides != nil {
				t.Errorf("result = %+v, want none on failure", res)
			}
			for _, d := range diags {
				if d.Code == tt.code {
					return
				}
			}
			t.Errorf("want %s, got %+v", tt.code, diags)
		})
	}
}

func TestResolveTwoOverridesOfOneTargetReportBoth(t *testing.T) {
	in := base()
	in.Nodes = append(in.Nodes, Node{Ref: "app#other", Kind: "a.prompt@1", Owner: "app"})
	in.Overrides = []Override{{"shared#prompt", "defaults#prompt"}, {"shared#prompt", "app#other"}}
	_, diags := Resolve(in)
	if len(diags) != 2 {
		t.Errorf("diagnostics = %+v, want one for each override", diags)
	}
}

func TestResolveIsDeterministic(t *testing.T) {
	in := base()
	in.Nodes[0].Refs = append(in.Nodes[0].Refs, "z#a", "a#z", "defaults#prompt")
	_, first := Resolve(in)
	for range 20 {
		_, again := Resolve(in)
		for i := range first {
			if first[i].Message != again[i].Message {
				t.Fatal("diagnostic order changed between runs")
			}
		}
	}
}

func TestResolveExpandsBundleOverrideChains(t *testing.T) {
	in := Input{
		Owners: []Owner{{ID: "shipped", Bundle: true}, {ID: "user", Bundle: true}, {ID: "project", Bundle: true}},
		Nodes: []Node{
			{Ref: "shipped#a", Kind: "k.p@1", Owner: "shipped"},
			{Ref: "shipped#b", Kind: "k.p@1", Owner: "shipped"},
			{Ref: "shipped#c", Kind: "k.p@1", Owner: "shipped"},
			{Ref: "user#a", Kind: "k.p@1", Owner: "user"},
			{Ref: "user#b", Kind: "k.p@1", Owner: "user"},
			{Ref: "project#a", Kind: "k.p@1", Owner: "project"},
			{Ref: "project#new", Kind: "k.p@1", Owner: "project"},
		},
		OwnerOverrides: []Override{{"shipped", "user"}, {"user", "project"}},
		Global:         map[string]bool{"k.p@1": true},
	}
	res, diags := Resolve(in)
	if len(diags) != 0 {
		t.Fatalf("diagnostics = %+v", diags)
	}
	want := map[string]string{"shipped#a": "project#a", "user#a": "project#a", "shipped#b": "user#b"}
	if !maps.Equal(res.Overrides, want) {
		t.Errorf("overrides = %v, want %v", res.Overrides, want)
	}
}

func TestResolveGlobalIgnoresOverriddenTargets(t *testing.T) {
	in := base()
	in.Global = map[string]bool{"a.prompt@1": true}
	in.Overrides = []Override{{"shared#prompt", "defaults#prompt"}}
	if _, diags := Resolve(in); len(diags) != 0 {
		t.Errorf("diagnostics = %+v", diags)
	}
	in.Overrides = nil
	_, diags := Resolve(in)
	if len(diags) != 2 {
		t.Errorf("diagnostics = %+v, want one for each conflicting definition", diags)
	}
}
