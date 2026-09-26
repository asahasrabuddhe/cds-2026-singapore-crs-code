// Package spec is the description of a container that the parent hands to
// the child it re-executes. runc has a JSON document for this too; it calls
// it config.json and it runs to several hundred lines. This one has ten
// fields, and that is the whole difference between a runtime and an
// educational one.
package spec

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"go.ajitem.com/cds-crs/internal/caps"
	"go.ajitem.com/cds-crs/internal/cgroup"
	"go.ajitem.com/cds-crs/internal/namespace"
)

// Spec is everything the child needs to become a container.
type Spec struct {
	ID         string        `json:"id"`
	Rootfs     string        `json:"rootfs"`
	Hostname   string        `json:"hostname"`
	Namespaces namespace.Set `json:"namespaces"`
	Limits     cgroup.Limits `json:"limits"`
	Caps       caps.Set      `json:"caps"`
	// Init makes the child stay resident as PID 1, reaping orphans and
	// forwarding signals, and run the command as its child. Off, the child execs
	// the command directly and the command is PID 1, with everything that
	// entails.
	Init bool     `json:"init"`
	Args []string `json:"args"`
	Env  []string `json:"env"`
	// Verbose makes both halves narrate what they do to stderr. For the
	// stage, and for the first time you read the code.
	Verbose bool `json:"verbose"`
}

// NewID is twelve hex characters, the same shape as a short Docker ID.
func NewID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// Encode writes the spec as JSON.
func (s Spec) Encode(w io.Writer) error {
	return json.NewEncoder(w).Encode(s)
}

// Decode reads a spec written by Encode.
func Decode(r io.Reader) (Spec, error) {
	var s Spec
	if err := json.NewDecoder(r).Decode(&s); err != nil {
		return Spec{}, fmt.Errorf("decode container spec: %w", err)
	}
	return s, nil
}

// Validate catches what the kernel would only report as an opaque EINVAL.
func (s Spec) Validate() error {
	switch {
	case s.ID == "":
		return errors.New("spec: empty id")
	case s.Rootfs == "":
		return errors.New("spec: empty rootfs")
	case !filepath.IsAbs(s.Rootfs):
		return fmt.Errorf("spec: rootfs %q must be absolute", s.Rootfs)
	case len(s.Args) == 0:
		return errors.New("spec: no command")
	case s.Limits.Pids < 0:
		return errors.New("spec: pids limit must not be negative")
	case s.Limits.CPUs < 0:
		return errors.New("spec: cpus limit must not be negative")
	case s.Limits.Memory < 0:
		return errors.New("spec: memory limit must not be negative")
	case s.Hostname != "" && !s.Namespaces.UTS:
		return errors.New("spec: a hostname needs a uts namespace, or it renames the host")
	}
	return nil
}
