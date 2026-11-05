package digest

import "testing"

// These are golden tests, cross-checked against an independent implementation of
// the encoding documented on Bundle: digests are persisted by consumers, so these values must
// never change. A change here changes every bundle's and catalog's identity.

func TestBundleGolden(t *testing.T) {
	tests := []struct {
		name   string
		files  map[string][]byte
		assets map[string]string
		want   string
	}{
		{name: "empty", want: "sha256:f89c8ceeb05a684fcb9ded73c6c64c80f802c6ec37b0110d72ae00bee581d58d"},
		{
			name: "files and assets",
			files: map[string][]byte{
				"bundle.yaml":        []byte("apiVersion: craggy.io/bundle/v1\n"),
				"scripts/hello.star": []byte("def run():\n    return \"hello\"\n"),
			},
			assets: map[string]string{
				"assets/tone.md": "sha256:2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824",
			},
			want: "sha256:d998554fbda00937125e65ab67af557f4de3070a0d4d6ad481c77cbf3ee234f8",
		},
	}
	for _, tt := range tests {
		if got := Bundle(tt.files, tt.assets); got != tt.want {
			t.Errorf("%s: Bundle = %s, want %s", tt.name, got, tt.want)
		}
	}
}

func TestCatalogGolden(t *testing.T) {
	got := Catalog(CatalogInput{
		BundleDigests: map[string]string{"example.hello": "sha256:2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"},
		Providers:     map[string]string{"example.defaults": "1.0.0"},
		Contributions: []string{"example.hello#hello", "example.defaults#default"},
		Bindings:      map[string]string{"example.hello#hello": "example.defaults#default"},
		Kinds:         []string{"example.greeting@1"},
	})
	if want := "sha256:3b693f16018692c632c98cdd7f932447c602fb38a098b7395cdfd6b87977c756"; got != want {
		t.Errorf("Catalog = %s, want %s", got, want)
	}
	if empty, want := Catalog(CatalogInput{}), "sha256:026e6b470d0dede10b939341bce912a4fcdfd192dd29932730b55de5037ccc9d"; empty != want {
		t.Errorf("empty Catalog = %s, want %s", empty, want)
	}
}

func TestBundleSeparatesFilesFromAssets(t *testing.T) {
	const d = "sha256:2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	asFile := Bundle(map[string][]byte{"assets/a": []byte(d)}, nil)
	asAsset := Bundle(nil, map[string]string{"assets/a": d})
	if asFile == asAsset {
		t.Error("a file and an asset with the same path and content give the same digest")
	}
}

func TestBundleIsLengthDelimited(t *testing.T) {
	a := Bundle(map[string][]byte{"ab": []byte("c")}, nil)
	b := Bundle(map[string][]byte{"a": []byte("bc")}, nil)
	if a == b {
		t.Error("moving bytes between a path and its content does not change the digest")
	}
}

func TestCatalogIgnoresListOrder(t *testing.T) {
	a := Catalog(CatalogInput{Contributions: []string{"x#a", "x#b"}, Kinds: []string{"a.b@1", "a.c@1"}})
	b := Catalog(CatalogInput{Contributions: []string{"x#b", "x#a"}, Kinds: []string{"a.c@1", "a.b@1"}})
	if a != b {
		t.Error("the order of Contributions or Kinds changes the digest")
	}
}
