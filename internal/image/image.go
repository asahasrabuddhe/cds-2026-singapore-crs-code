// Package image pulls a container image from a registry and unpacks it into
// a directory, with nothing but the standard library. That directory is
// what `mycontainer run` takes as its rootfs.
//
// An image is a manifest listing layers, each layer a tarball, applied in
// order. Files deleted by a later layer are marked with "whiteout" entries
// rather than removed. That is the whole format. The registry protocol on
// top is three GETs and a bearer token.
package image

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// Media types, from the OCI image spec and Docker's older equivalents. A
// registry may answer with either family and the pull has to accept both.
const (
	mediaOCIIndex        = "application/vnd.oci.image.index.v1+json"
	mediaOCIManifest     = "application/vnd.oci.image.manifest.v1+json"
	mediaOCILayerGzip    = "application/vnd.oci.image.layer.v1.tar+gzip"
	mediaOCILayer        = "application/vnd.oci.image.layer.v1.tar"
	mediaDockerList      = "application/vnd.docker.distribution.manifest.list.v2+json"
	mediaDockerManifest  = "application/vnd.docker.distribution.manifest.v2+json"
	mediaDockerLayerGzip = "application/vnd.docker.image.rootfs.diff.tar.gzip"
)

// ------------------------------------------------------------- reference ---

// Ref is a parsed image reference.
type Ref struct {
	Registry   string // host to talk to, e.g. registry-1.docker.io
	Repository string // e.g. library/alpine
	Tag        string // e.g. 3.22; empty when Digest is set
	Digest     string // sha256:... when pinned by digest
}

var (
	refComponent = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)
	refTag       = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)
	refDigest    = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
)

// ParseRef reads "alpine", "alpine:3.22", "ghcr.io/foo/bar:v2" or
// "repo@sha256:...". Docker Hub's conventions apply: no registry means
// Docker Hub, and a single-component name lives under library/.
func ParseRef(s string) (Ref, error) {
	if s == "" {
		return Ref{}, errors.New("empty image reference")
	}
	var ref Ref

	// Digest, then tag, then the rest. The digest is split first because
	// it contains a colon of its own.
	name := s
	if i := strings.Index(name, "@"); i >= 0 {
		ref.Digest = name[i+1:]
		name = name[:i]
		if !refDigest.MatchString(ref.Digest) {
			return Ref{}, fmt.Errorf("image %q: bad digest", s)
		}
	} else if i := strings.LastIndex(name, ":"); i >= 0 && !strings.Contains(name[i+1:], "/") {
		ref.Tag = name[i+1:]
		name = name[:i]
		if !refTag.MatchString(ref.Tag) {
			return Ref{}, fmt.Errorf("image %q: bad tag", s)
		}
	} else {
		ref.Tag = "latest"
	}

	// The first path component is a registry host if it looks like one:
	// contains a dot or a port, or is "localhost". That is Docker's rule.
	parts := strings.Split(name, "/")
	if len(parts) > 1 && (strings.ContainsAny(parts[0], ".:") || parts[0] == "localhost") {
		ref.Registry = parts[0]
		parts = parts[1:]
	} else {
		ref.Registry = "docker.io"
	}
	if ref.Registry == "docker.io" {
		ref.Registry = "registry-1.docker.io"
		if len(parts) == 1 {
			parts = []string{"library", parts[0]}
		}
	}

	for _, p := range parts {
		if !refComponent.MatchString(p) {
			return Ref{}, fmt.Errorf("image %q: bad repository component %q", s, p)
		}
	}
	ref.Repository = strings.Join(parts, "/")
	return ref, nil
}

// Name is the last component of the repository: "alpine" for
// library/alpine. The default output directory is named after it.
func (r Ref) Name() string {
	return path.Base(r.Repository)
}

// reference is what goes in the manifests URL.
func (r Ref) reference() string {
	if r.Digest != "" {
		return r.Digest
	}
	return r.Tag
}

// ---------------------------------------------------------------- client ---

// Client pulls images. Zero value is not usable; set HTTP, OS and Arch.
type Client struct {
	HTTP *http.Client
	OS   string // "linux"
	Arch string // Go's spelling: "arm64", "amd64"
	// Insecure talks plain HTTP. Only for tests and a local registry.
	Insecure bool

	token string
}

type descriptor struct {
	MediaType string    `json:"mediaType"`
	Digest    string    `json:"digest"`
	Size      int64     `json:"size"`
	Platform  *platform `json:"platform,omitempty"`
}

type platform struct {
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
	Variant      string `json:"variant,omitempty"`
}

type index struct {
	MediaType string       `json:"mediaType"`
	Manifests []descriptor `json:"manifests"`
}

type manifest struct {
	MediaType string       `json:"mediaType"`
	Config    descriptor   `json:"config"`
	Layers    []descriptor `json:"layers"`
}

// Pull fetches ref and unpacks it into dest, which is created if needed
// and must be empty or absent. Progress goes to log.
func (c *Client) Pull(ctx context.Context, ref Ref, dest string, log io.Writer) error {
	if entries, err := os.ReadDir(dest); err == nil && len(entries) > 0 {
		return fmt.Errorf("%s exists and is not empty", dest)
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}

	fmt.Fprintf(log, "pulling %s/%s:%s for %s/%s\n", ref.Registry, ref.Repository, ref.reference(), c.OS, c.Arch)

	// 1. The manifest for the tag. It may be an index (one entry per
	//    platform) or already a single-platform manifest.
	body, mediaType, err := c.get(ctx, ref, "manifests/"+ref.reference(),
		strings.Join([]string{mediaOCIIndex, mediaDockerList, mediaOCIManifest, mediaDockerManifest}, ", "))
	if err != nil {
		return fmt.Errorf("fetch manifest: %w", err)
	}

	var m manifest
	switch mediaType {
	case mediaOCIIndex, mediaDockerList:
		var idx index
		if err := json.Unmarshal(body, &idx); err != nil {
			return fmt.Errorf("decode index: %w", err)
		}
		d, err := pickManifest(idx, c.OS, c.Arch)
		if err != nil {
			return err
		}
		fmt.Fprintf(log, "index has %d platforms, using %s/%s (%s)\n", len(idx.Manifests), c.OS, c.Arch, short(d.Digest))
		body, _, err = c.get(ctx, ref, "manifests/"+d.Digest, mediaOCIManifest+", "+mediaDockerManifest)
		if err != nil {
			return fmt.Errorf("fetch platform manifest: %w", err)
		}
		if err := json.Unmarshal(body, &m); err != nil {
			return fmt.Errorf("decode manifest: %w", err)
		}
	case mediaOCIManifest, mediaDockerManifest:
		if err := json.Unmarshal(body, &m); err != nil {
			return fmt.Errorf("decode manifest: %w", err)
		}
	default:
		return fmt.Errorf("unexpected manifest media type %q", mediaType)
	}

	// 2. The layers, in order. Each is verified against its digest and
	//    applied on top of the previous ones.
	fmt.Fprintf(log, "%d layers\n", len(m.Layers))
	for i, layer := range m.Layers {
		fmt.Fprintf(log, "layer %d/%d %s (%d bytes)\n", i+1, len(m.Layers), short(layer.Digest), layer.Size)
		if err := c.applyBlob(ctx, ref, layer, dest); err != nil {
			return fmt.Errorf("layer %d (%s): %w", i+1, short(layer.Digest), err)
		}
	}
	fmt.Fprintf(log, "unpacked into %s\n", dest)
	return nil
}

// pickManifest chooses the entry for os/arch. The variant is ignored: an
// arm64 host runs v8 and that is the only variant published for it.
func pickManifest(idx index, osName, arch string) (descriptor, error) {
	for _, d := range idx.Manifests {
		if d.Platform != nil && d.Platform.OS == osName && d.Platform.Architecture == arch {
			return d, nil
		}
	}
	var have []string
	for _, d := range idx.Manifests {
		if d.Platform != nil {
			have = append(have, d.Platform.OS+"/"+d.Platform.Architecture)
		}
	}
	return descriptor{}, fmt.Errorf("no manifest for %s/%s (image has %s)", osName, arch, strings.Join(have, ", "))
}

// applyBlob streams one layer from the registry through a digest check and
// a gunzip into ApplyLayer. The digest is checked on the compressed bytes,
// which is what the manifest's digest covers.
func (c *Client) applyBlob(ctx context.Context, ref Ref, layer descriptor, dest string) error {
	resp, err := c.do(ctx, ref, "blobs/"+layer.Digest, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	hasher := sha256.New()
	var r io.Reader = io.TeeReader(resp.Body, hasher)
	switch layer.MediaType {
	case mediaOCILayerGzip, mediaDockerLayerGzip:
		gz, err := gzip.NewReader(r)
		if err != nil {
			return fmt.Errorf("gunzip: %w", err)
		}
		defer gz.Close()
		r = gz
	case mediaOCILayer:
		// already a plain tar
	default:
		return fmt.Errorf("unsupported layer media type %q (zstd layers are not handled)", layer.MediaType)
	}

	if err := ApplyLayer(dest, r); err != nil {
		return err
	}
	// Drain whatever the tar reader did not consume, so the hash covers
	// the whole blob.
	if _, err := io.Copy(io.Discard, r); err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return err
	}
	if got := "sha256:" + hex.EncodeToString(hasher.Sum(nil)); got != layer.Digest {
		return fmt.Errorf("digest mismatch: manifest says %s, blob is %s", short(layer.Digest), short(got))
	}
	return nil
}

// get performs a GET and returns the body and its content type.
func (c *Client) get(ctx context.Context, ref Ref, p, accept string) ([]byte, string, error) {
	resp, err := c.do(ctx, ref, p, accept)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	mt, _, _ := strings.Cut(resp.Header.Get("Content-Type"), ";")
	return body, strings.TrimSpace(mt), nil
}

// do performs a registry GET, answering a bearer challenge once. Docker
// Hub, ghcr.io and quay.io all hand anonymous pull tokens to anyone who
// asks for the right scope, so "auth" here is one extra round trip.
func (c *Client) do(ctx context.Context, ref Ref, p, accept string) (*http.Response, error) {
	scheme := "https"
	if c.Insecure {
		scheme = "http"
	}
	url := fmt.Sprintf("%s://%s/v2/%s/%s", scheme, ref.Registry, ref.Repository, p)

	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		if accept != "" {
			req.Header.Set("Accept", accept)
		}
		if c.token != "" {
			req.Header.Set("Authorization", "Bearer "+c.token)
		}
		resp, err := c.HTTP.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			challenge := resp.Header.Get("WWW-Authenticate")
			_ = resp.Body.Close()
			if err := c.authenticate(ctx, challenge); err != nil {
				return nil, err
			}
			continue
		}
		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
		}
		return resp, nil
	}
	return nil, fmt.Errorf("GET %s: unauthorized after fetching a token", url)
}

// authenticate follows a "Bearer realm=..." challenge to its token
// endpoint and remembers the token it gets back.
func (c *Client) authenticate(ctx context.Context, challenge string) error {
	realm, params := parseChallenge(challenge)
	if realm == "" {
		return fmt.Errorf("registry wants authentication this tool cannot do: %q", challenge)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, realm, nil)
	if err != nil {
		return err
	}
	q := req.URL.Query()
	for _, k := range []string{"service", "scope"} {
		if v, ok := params[k]; ok {
			q.Set(k, v)
		}
	}
	req.URL.RawQuery = q.Encode()

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("token endpoint %s: %s", realm, resp.Status)
	}
	var tok struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return fmt.Errorf("decode token: %w", err)
	}
	c.token = tok.Token
	if c.token == "" {
		c.token = tok.AccessToken
	}
	if c.token == "" {
		return errors.New("token endpoint returned no token")
	}
	return nil
}

var challengeParam = regexp.MustCompile(`(\w+)="([^"]*)"`)

// parseChallenge reads a WWW-Authenticate Bearer header into its realm and
// the remaining parameters. Anything but Bearer returns an empty realm.
func parseChallenge(h string) (string, map[string]string) {
	if !strings.HasPrefix(h, "Bearer ") {
		return "", nil
	}
	params := map[string]string{}
	for _, m := range challengeParam.FindAllStringSubmatch(h, -1) {
		params[m[1]] = m[2]
	}
	return params["realm"], params
}

func short(digest string) string {
	if i := strings.Index(digest, ":"); i >= 0 && len(digest) > i+13 {
		return digest[i+1 : i+13]
	}
	return digest
}

// ----------------------------------------------------------------- layer ---

const (
	whiteoutPrefix = ".wh."
	whiteoutOpaque = ".wh..wh..opq"
)

// ApplyLayer unpacks one layer tar into dest on top of whatever is there.
//
// Whiteouts, from the OCI image spec: an entry named ".wh.<name>" means
// "delete <name> from the lower layers", and ".wh..wh..opq" in a directory
// means "delete everything in this directory from the lower layers". The
// markers themselves are never written.
//
// Every path is confined to dest. Entries that would land outside it, or
// that would be written through a symlink that points outside it, are an
// error rather than a silent skip, because a rootfs with a hole in it is
// worse than no rootfs.
func ApplyLayer(dest string, r io.Reader) error {
	dest, err := filepath.Abs(dest)
	if err != nil {
		return err
	}
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read tar: %w", err)
		}

		rel := filepath.Clean("/" + hdr.Name)[1:] // strip any leading /, resolve ..
		if rel == "" || rel == "." {
			continue
		}
		if strings.HasPrefix(hdr.Name, "..") || strings.Contains(hdr.Name, "/../") || strings.HasSuffix(hdr.Name, "/..") {
			return fmt.Errorf("entry %q escapes the destination", hdr.Name)
		}

		dir, base := filepath.Split(rel)
		target, err := safeJoin(dest, dir)
		if err != nil {
			return fmt.Errorf("entry %q: %w", hdr.Name, err)
		}

		switch {
		case base == whiteoutOpaque:
			if err := removeChildren(target); err != nil {
				return err
			}
			continue
		case strings.HasPrefix(base, whiteoutPrefix):
			if err := os.RemoveAll(filepath.Join(target, strings.TrimPrefix(base, whiteoutPrefix))); err != nil {
				return err
			}
			continue
		}

		if err := os.MkdirAll(target, 0o755); err != nil {
			return err
		}
		full := filepath.Join(target, base)

		// Whatever is there now, the layer's entry replaces it. A directory
		// only replaces a non-directory.
		if info, err := os.Lstat(full); err == nil {
			if !(info.IsDir() && hdr.Typeflag == tar.TypeDir) {
				if err := os.RemoveAll(full); err != nil {
					return err
				}
			}
		}

		mode := os.FileMode(hdr.Mode & 0o7777)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(full, 0o755); err != nil {
				return err
			}
			// Red Hat and SUSE images ship directories such as /root as
			// 0550. As root that is no obstacle; as you, the next entry
			// inside would fail, so keep the owner's write bit.
			if os.Geteuid() != 0 {
				mode |= 0o700
			}
			if err := os.Chmod(full, mode); err != nil {
				return err
			}
		case tar.TypeReg:
			f, err := os.OpenFile(full, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				_ = f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
			// umask may have trimmed the create mode; set it explicitly.
			if err := os.Chmod(full, mode); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := os.Symlink(hdr.Linkname, full); err != nil {
				return err
			}
		case tar.TypeLink:
			src, err := safeJoin(dest, hdr.Linkname)
			if err != nil {
				return fmt.Errorf("hard link %q: %w", hdr.Name, err)
			}
			if err := os.Link(src, full); err != nil {
				return err
			}
		default:
			// Character and block devices, FIFOs: mknod needs CAP_MKNOD and
			// the runtime bind-mounts the devices a container needs anyway.
			continue
		}

		// Ownership only when we could. Unprivileged pulls leave everything
		// owned by the caller, which is fine for a rootfs that runs as root
		// inside a user namespace.
		if os.Geteuid() == 0 {
			_ = os.Lchown(full, hdr.Uid, hdr.Gid)
		}
	}
}

// safeJoin joins rel onto dest and refuses a result outside dest, after
// resolving any symlinks already on disk. A layer that plants a symlink to
// / and then writes "through" it must not reach the host.
func safeJoin(dest, rel string) (string, error) {
	full := filepath.Join(dest, rel)
	if full != dest && !strings.HasPrefix(full, dest+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes the destination", rel)
	}
	// Resolve what exists so far. The last components may not exist yet.
	resolved, err := evalExisting(full)
	if err != nil {
		return "", err
	}
	realDest, err := filepath.EvalSymlinks(dest)
	if err != nil {
		return "", err
	}
	if resolved != realDest && !strings.HasPrefix(resolved, realDest+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q resolves outside the destination through a symlink", rel)
	}
	return full, nil
}

// evalExisting resolves symlinks in the longest existing prefix of p and
// appends the rest unchanged.
func evalExisting(p string) (string, error) {
	existing, rest := p, ""
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		}
		parent, base := filepath.Split(filepath.Clean(existing))
		if parent == existing || parent == "" {
			return "", fmt.Errorf("no existing prefix for %q", p)
		}
		existing = filepath.Clean(parent)
		rest = filepath.Join(base, rest)
	}
	resolved, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolved, rest), nil
}

func removeChildren(dir string) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}
