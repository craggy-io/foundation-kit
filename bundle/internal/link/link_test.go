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
	bindings, diags := Resolve(base())
	if len(diags) != 0 {
		t.Fatalf("diagnostics = %+v", diags)
	}
	want := map[string]string{"app#helper": "app#helper", "shared#prompt": "shared#prompt"}
	if !maps.Equal(bindings, want) {
		t.Errorf("bindings = %v, want %v", bindings, want)
	}
}

func TestResolveAppliesOverrides(t *testing.T) {
	in := base()
	in.Overrides = []Override{{Target: "shared#prompt", Replacement: "defaults#prompt"}}
	bindings, diags := Resolve(in)
	if len(diags) != 0 {
		t.Fatalf("diagnostics = %+v", diags)
	}
	if bindings["shared#prompt"] != "defaults#prompt" {
		t.Errorf("bindings = %v", bindings)
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := base()
			tt.change(&in)
			bindings, diags := Resolve(in)
			if bindings != nil {
				t.Errorf("bindings = %v, want nil on failure", bindings)
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
