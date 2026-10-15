package image

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseRef(t *testing.T) {
	tests := []struct {
		in      string
		want    Ref
		wantErr bool
	}{
		{in: "alpine", want: Ref{Registry: "registry-1.docker.io", Repository: "library/alpine", Tag: "latest"}},
		{in: "alpine:3.22", want: Ref{Registry: "registry-1.docker.io", Repository: "library/alpine", Tag: "3.22"}},
		{in: "library/alpine:3.22", want: Ref{Registry: "registry-1.docker.io", Repository: "library/alpine", Tag: "3.22"}},
		{in: "docker.io/library/alpine", want: Ref{Registry: "registry-1.docker.io", Repository: "library/alpine", Tag: "latest"}},
		{in: "asahasrabuddhe/demo:1.0", want: Ref{Registry: "registry-1.docker.io", Repository: "asahasrabuddhe/demo", Tag: "1.0"}},
		{in: "ghcr.io/foo/bar:v2", want: Ref{Registry: "ghcr.io", Repository: "foo/bar", Tag: "v2"}},
		{in: "localhost:5000/x", want: Ref{Registry: "localhost:5000", Repository: "x", Tag: "latest"}},
		{in: "quay.io/a/b@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", want: Ref{Registry: "quay.io", Repository: "a/b", Digest: "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}},
		{in: "", wantErr: true},
		{in: "alpine:", wantErr: true},
		{in: ":3.22", wantErr: true},
		{in: "alpine@sha256:short", wantErr: true},
		{in: "Alpine", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseRef(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("want error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

func TestRefName(t *testing.T) {
	for in, want := range map[string]string{
		"alpine":          "alpine",
		"alpine:3.22":     "alpine",
		"ghcr.io/foo/bar": "bar",
	} {
		ref, err := ParseRef(in)
		if err != nil {
			t.Fatal(err)
		}
		if got := ref.Name(); got != want {
			t.Errorf("%s: Name() = %q, want %q", in, got, want)
		}
	}
}

// entry describes one tar entry for the test helpers.
type entry struct {
	name    string
	mode    int64
	typ     byte
	content string
	link    string
}

func makeTar(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		typ := e.typ
		if typ == 0 {
			typ = tar.TypeReg
			if strings.HasSuffix(e.name, "/") {
				typ = tar.TypeDir
			}
		}
		mode := e.mode
		if mode == 0 {
			mode = 0o644
			if typ == tar.TypeDir {
				mode = 0o755
			}
		}
		hdr := &tar.Header{Name: e.name, Mode: mode, Typeflag: typ, Linkname: e.link, Size: int64(len(e.content))}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(e.content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func TestApplyLayerExtractsFilesDirsAndSymlinks(t *testing.T) {
	dest := t.TempDir()
	layer := makeTar(t,
		entry{name: "bin/"},
		entry{name: "bin/dash", mode: 0o755, content: "ELF"},
		entry{name: "bin/sh", typ: tar.TypeSymlink, link: "dash"},
		entry{name: "etc/passwd", content: "root:x:0:0\n"},
		entry{name: "bin/ls", typ: tar.TypeLink, link: "bin/dash"},
	)
	if err := ApplyLayer(dest, bytes.NewReader(layer)); err != nil {
		t.Fatal(err)
	}

	if got := readFile(t, filepath.Join(dest, "bin", "dash")); got != "ELF" {
		t.Errorf("dash = %q", got)
	}
	info, err := os.Stat(filepath.Join(dest, "bin", "dash"))
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Errorf("dash mode = %v, err %v", info.Mode(), err)
	}
	if link, _ := os.Readlink(filepath.Join(dest, "bin", "sh")); link != "dash" {
		t.Errorf("sh -> %q", link)
	}
	// etc/ was never listed as a directory entry; it still has to exist.
	if got := readFile(t, filepath.Join(dest, "etc", "passwd")); got != "root:x:0:0\n" {
		t.Errorf("passwd = %q", got)
	}
	// The hard link has the same content as its target.
	if got := readFile(t, filepath.Join(dest, "bin", "ls")); got != "ELF" {
		t.Errorf("ls (hard link) = %q", got)
	}
}

func TestApplyLayerWhiteoutRemovesAFile(t *testing.T) {
	dest := t.TempDir()
	base := makeTar(t, entry{name: "etc/"}, entry{name: "etc/old", content: "x"}, entry{name: "etc/keep", content: "y"})
	upper := makeTar(t, entry{name: "etc/.wh.old", content: ""})

	for _, l := range [][]byte{base, upper} {
		if err := ApplyLayer(dest, bytes.NewReader(l)); err != nil {
			t.Fatal(err)
		}
	}
	if exists(filepath.Join(dest, "etc", "old")) {
		t.Error("etc/old survived its whiteout")
	}
	if exists(filepath.Join(dest, "etc", ".wh.old")) {
		t.Error("the whiteout marker itself was written")
	}
	if !exists(filepath.Join(dest, "etc", "keep")) {
		t.Error("etc/keep was removed")
	}
}

func TestApplyLayerOpaqueWhiteoutEmptiesADirectory(t *testing.T) {
	dest := t.TempDir()
	base := makeTar(t, entry{name: "cfg/"}, entry{name: "cfg/a", content: "a"}, entry{name: "cfg/b", content: "b"})
	upper := makeTar(t, entry{name: "cfg/"}, entry{name: "cfg/.wh..wh..opq", content: ""}, entry{name: "cfg/c", content: "c"})

	for _, l := range [][]byte{base, upper} {
		if err := ApplyLayer(dest, bytes.NewReader(l)); err != nil {
			t.Fatal(err)
		}
	}
	for _, gone := range []string{"a", "b", ".wh..wh..opq"} {
		if exists(filepath.Join(dest, "cfg", gone)) {
			t.Errorf("cfg/%s should be gone", gone)
		}
	}
	if got := readFile(t, filepath.Join(dest, "cfg", "c")); got != "c" {
		t.Errorf("cfg/c = %q", got)
	}
}

func TestApplyLayerReplacesAFileWithADirectoryAndBack(t *testing.T) {
	dest := t.TempDir()
	first := makeTar(t, entry{name: "thing", content: "file"})
	second := makeTar(t, entry{name: "thing/"}, entry{name: "thing/inner", content: "x"})
	third := makeTar(t, entry{name: "thing", content: "file again"})

	for _, l := range [][]byte{first, second, third} {
		if err := ApplyLayer(dest, bytes.NewReader(l)); err != nil {
			t.Fatal(err)
		}
	}
	if got := readFile(t, filepath.Join(dest, "thing")); got != "file again" {
		t.Errorf("thing = %q", got)
	}
}

func TestApplyLayerRejectsPathTraversal(t *testing.T) {
	dest := t.TempDir()
	outside := filepath.Join(filepath.Dir(dest), "escaped")
	defer os.Remove(outside)

	for _, name := range []string{"../escaped", "a/../../escaped", "/abs/../../escaped"} {
		layer := makeTar(t, entry{name: name, content: "boo"})
		err := ApplyLayer(dest, bytes.NewReader(layer))
		if err == nil && exists(outside) {
			t.Fatalf("%q escaped the destination", name)
		}
		if err == nil {
			t.Errorf("%q was accepted", name)
		}
	}
	// A symlink pointing outside is fine to create (it is just a string),
	// but a later entry must not be written through it.
	layer := makeTar(t,
		entry{name: "out", typ: tar.TypeSymlink, link: "../"},
		entry{name: "out/escaped", content: "boo"},
	)
	if err := ApplyLayer(dest, bytes.NewReader(layer)); err == nil {
		t.Error("write through an escaping symlink was accepted")
	}
	if exists(outside) {
		t.Error("write through symlink escaped the destination")
	}
}

func TestApplyLayerSkipsDeviceNodes(t *testing.T) {
	dest := t.TempDir()
	layer := makeTar(t, entry{name: "dev/"}, entry{name: "dev/null", typ: tar.TypeChar}, entry{name: "etc/hostname", content: "x"})
	if err := ApplyLayer(dest, bytes.NewReader(layer)); err != nil {
		t.Fatalf("a device node should be skipped, not fatal: %v", err)
	}
	if exists(filepath.Join(dest, "dev", "null")) {
		t.Error("device node was created")
	}
	if !exists(filepath.Join(dest, "etc", "hostname")) {
		t.Error("extraction stopped at the device node")
	}
}

// ---------------------------------------------------------- fake registry ---

type blob struct {
	mediaType string
	data      []byte
}

func digestOf(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func gzipped(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(b); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// fakeRegistry serves one repository with an index of two platforms, each
// pointing at a manifest with two gzip layers, behind bearer auth that
// hands out a token to anyone who asks. The shape Docker Hub has.
func fakeRegistry(t *testing.T, repo string) (*httptest.Server, map[string]blob, string) {
	t.Helper()
	blobs := map[string]blob{}
	put := func(mt string, b []byte) string {
		d := digestOf(b)
		blobs[d] = blob{mt, b}
		return d
	}

	layer1 := gzipped(t, makeTar(t, entry{name: "bin/"}, entry{name: "bin/dash", mode: 0o755, content: "ELF"}, entry{name: "etc/old", content: "old"}))
	layer2 := gzipped(t, makeTar(t, entry{name: "etc/.wh.old"}, entry{name: "etc/new", content: "new"}))

	manifestFor := func(arch string) string {
		m := map[string]any{
			"schemaVersion": 2,
			"mediaType":     mediaOCIManifest,
			"config":        map[string]any{"mediaType": "application/vnd.oci.image.config.v1+json", "digest": put("application/vnd.oci.image.config.v1+json", []byte(`{"architecture":"`+arch+`"}`)), "size": 1},
			"layers": []map[string]any{
				{"mediaType": mediaOCILayerGzip, "digest": put(mediaOCILayerGzip, layer1), "size": len(layer1)},
				{"mediaType": mediaOCILayerGzip, "digest": put(mediaOCILayerGzip, layer2), "size": len(layer2)},
			},
		}
		b, _ := json.Marshal(m)
		return put(mediaOCIManifest, b)
	}
	arm := manifestFor("arm64")
	amd := manifestFor("amd64")
	index, _ := json.Marshal(map[string]any{
		"schemaVersion": 2,
		"mediaType":     mediaOCIIndex,
		"manifests": []map[string]any{
			{"mediaType": mediaOCIManifest, "digest": amd, "size": 1, "platform": map[string]string{"os": "linux", "architecture": "amd64"}},
			{"mediaType": mediaOCIManifest, "digest": arm, "size": 1, "platform": map[string]string{"os": "linux", "architecture": "arm64"}},
		},
	})
	indexDigest := put(mediaOCIIndex, index)

	const token = "anonymous-token"
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token":
			_ = json.NewEncoder(w).Encode(map[string]string{"token": token})
			return
		case r.Header.Get("Authorization") != "Bearer "+token:
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer realm="%s/token",service="fake",scope="repository:%s:pull"`, srv.URL, repo))
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		prefix := "/v2/" + repo + "/"
		if !strings.HasPrefix(r.URL.Path, prefix) {
			http.NotFound(w, r)
			return
		}
		rest := strings.TrimPrefix(r.URL.Path, prefix)
		switch {
		case rest == "manifests/1.0" || rest == "manifests/"+indexDigest:
			w.Header().Set("Content-Type", mediaOCIIndex)
			_, _ = w.Write(blobs[indexDigest].data)
		case strings.HasPrefix(rest, "manifests/sha256:"):
			d := strings.TrimPrefix(rest, "manifests/")
			b, ok := blobs[d]
			if !ok {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", b.mediaType)
			_, _ = w.Write(b.data)
		case strings.HasPrefix(rest, "blobs/"):
			d := strings.TrimPrefix(rest, "blobs/")
			b, ok := blobs[d]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(b.data)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, blobs, indexDigest
}

func TestPullUnpacksTheRightPlatform(t *testing.T) {
	srv, _, _ := fakeRegistry(t, "library/demo")
	host := strings.TrimPrefix(srv.URL, "http://")

	dest := t.TempDir()
	c := &Client{HTTP: srv.Client(), OS: "linux", Arch: "arm64", Insecure: true}
	var log bytes.Buffer
	ref := Ref{Registry: host, Repository: "library/demo", Tag: "1.0"}
	if err := c.Pull(t.Context(), ref, dest, &log); err != nil {
		t.Fatalf("Pull: %v\n%s", log.String(), err)
	}

	if got := readFile(t, filepath.Join(dest, "bin", "dash")); got != "ELF" {
		t.Errorf("dash = %q", got)
	}
	if exists(filepath.Join(dest, "etc", "old")) {
		t.Error("layer 2's whiteout was not applied")
	}
	if got := readFile(t, filepath.Join(dest, "etc", "new")); got != "new" {
		t.Errorf("etc/new = %q", got)
	}
	if !strings.Contains(log.String(), "arm64") {
		t.Errorf("log does not say which platform was chosen:\n%s", log.String())
	}
	if !strings.Contains(log.String(), "2 layers") && !strings.Contains(log.String(), "layer 2/2") {
		t.Errorf("log does not count layers:\n%s", log.String())
	}
}

func TestPullRefusesAnUnknownPlatform(t *testing.T) {
	srv, _, _ := fakeRegistry(t, "library/demo")
	host := strings.TrimPrefix(srv.URL, "http://")
	c := &Client{HTTP: srv.Client(), OS: "linux", Arch: "riscv64", Insecure: true}
	err := c.Pull(t.Context(), Ref{Registry: host, Repository: "library/demo", Tag: "1.0"}, t.TempDir(), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "riscv64") {
		t.Errorf("want an error naming the platform, got %v", err)
	}
}

func TestPullVerifiesLayerDigests(t *testing.T) {
	srv, blobs, _ := fakeRegistry(t, "library/demo")
	host := strings.TrimPrefix(srv.URL, "http://")
	// Swap every layer blob for a well-formed one with different content,
	// so the gunzip succeeds and only the digest in the manifest disagrees
	// with what the server sent.
	other := gzipped(t, makeTar(t, entry{name: "etc/other", content: "swapped"}))
	for d, b := range blobs {
		if b.mediaType == mediaOCILayerGzip {
			blobs[d] = blob{b.mediaType, other}
		}
	}
	c := &Client{HTTP: srv.Client(), OS: "linux", Arch: "arm64", Insecure: true}
	err := c.Pull(t.Context(), Ref{Registry: host, Repository: "library/demo", Tag: "1.0"}, t.TempDir(), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "digest") {
		t.Errorf("want a digest mismatch, got %v", err)
	}
}

func TestPickManifest(t *testing.T) {
	idx := index{Manifests: []descriptor{
		{Digest: "sha256:a", Platform: &platform{OS: "linux", Architecture: "amd64"}},
		{Digest: "sha256:b", Platform: &platform{OS: "linux", Architecture: "arm64", Variant: "v8"}},
		{Digest: "sha256:c", Platform: &platform{OS: "unknown", Architecture: "unknown"}},
	}}
	if d, err := pickManifest(idx, "linux", "arm64"); err != nil || d.Digest != "sha256:b" {
		t.Errorf("arm64: got %v, %v", d, err)
	}
	if d, err := pickManifest(idx, "linux", "amd64"); err != nil || d.Digest != "sha256:a" {
		t.Errorf("amd64: got %v, %v", d, err)
	}
	if _, err := pickManifest(idx, "linux", "s390x"); err == nil {
		t.Error("s390x: want error")
	}
}

func TestParseChallenge(t *testing.T) {
	realm, params := parseChallenge(`Bearer realm="https://auth.docker.io/token",service="registry.docker.io",scope="repository:library/alpine:pull"`)
	if realm != "https://auth.docker.io/token" {
		t.Errorf("realm = %q", realm)
	}
	if params["service"] != "registry.docker.io" || params["scope"] != "repository:library/alpine:pull" {
		t.Errorf("params = %v", params)
	}
	if realm, _ := parseChallenge(`Basic realm="x"`); realm != "" {
		t.Errorf("basic auth should not be treated as bearer, got realm %q", realm)
	}
}
