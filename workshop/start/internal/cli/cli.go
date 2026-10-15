// Package cli turns the arguments of `mycontainer run` into a spec.Spec.
// Nothing here touches the kernel, so it is tested on any OS.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"go.ajitem.com/cds-crs/internal/caps"
	"go.ajitem.com/cds-crs/internal/cgroup"
	"go.ajitem.com/cds-crs/internal/namespace"
	"go.ajitem.com/cds-crs/internal/spec"
)

// Usage is printed on -h and on a bad invocation.
const Usage = `usage: mycontainer run [flags] <image> <command> [args...]

Runs <command> as a container in rootfs/<image>, or in a directory if
<image> has a slash in it.

flags:
  -ns pid,mnt,uts,ipc,net,cgroup
                            namespaces to create (default: all but user)
  -rootless                 add a user namespace; implied when not root
  -hostname NAME            hostname inside the container (default: the id)
  -memory 64m               memory.max
  -cpus 0.5                 cpu.max, as a fraction of one core
  -pids 32                  pids.max
  -caps SPEC                capabilities to keep: default, all, none,
                            plus +CAP_X to add and -CAP_X to drop
  -init=false               exec the command as PID 1 instead of running
                            a reaping init in front of it
  -e KEY=VALUE              environment (repeatable)
  -v                        narrate each step to stderr
`

// defaultEnv is what the container sees when nothing is passed. The host's
// environment is deliberately not inherited: the container has its own
// filesystem, so the host's PATH is meaningless inside it.
var defaultEnv = []string{
	"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	"HOME=/root",
	"TERM=xterm",
}

type envFlag []string

func (e *envFlag) String() string { return strings.Join(*e, ",") }
func (e *envFlag) Set(v string) error {
	if !strings.Contains(v, "=") {
		return fmt.Errorf("environment %q: want KEY=VALUE", v)
	}
	*e = append(*e, v)
	return nil
}

// ParseRun parses everything after "run". rootless says whether the caller
// is an ordinary user, in which case a user namespace is added whether or
// not -rootless was given, because nothing else would work.
func ParseRun(args []string, rootless bool) (spec.Spec, error) {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var (
		ns       = fs.String("ns", "pid,mnt,uts,ipc,net,cgroup", "")
		userNS   = fs.Bool("rootless", false, "")
		hostname = fs.String("hostname", "", "")
		memory   = fs.String("memory", "", "")
		cpus     = fs.Float64("cpus", 0, "")
		pids     = fs.Int("pids", 0, "")
		capSpec  = fs.String("caps", "default", "")
		init     = fs.Bool("init", true, "")
		verbose  = fs.Bool("v", false, "")
		env      envFlag
	)
	fs.Var(&env, "e", "")

	if err := fs.Parse(args); err != nil {
		return spec.Spec{}, err
	}
	rest := fs.Args()
	if len(rest) < 2 {
		return spec.Spec{}, errors.New("want an image and a command")
	}

	s := spec.Spec{
		ID:      spec.NewID(),
		Init:    *init,
		Verbose: *verbose,
		Args:    rest[1:],
	}

	var err error
	// A bare name is an image under rootfs/, where make rootfs pulls it;
	// anything with a slash in it is a path.
	root := rest[0]
	if !strings.ContainsRune(root, '/') {
		root = filepath.Join("rootfs", root)
	}
	if s.Rootfs, err = filepath.Abs(root); err != nil {
		return spec.Spec{}, err
	}

	if s.Namespaces, err = namespace.Parse(*ns); err != nil {
		return spec.Spec{}, err
	}
	if rootless || *userNS {
		s.Namespaces.User = true
	}
	// A hostname is only meaningful with a UTS namespace. Given one
	// explicitly, add the namespace rather than refuse; given none, default
	// to the id only when there is a namespace to set it in.
	if *hostname != "" {
		s.Namespaces.UTS = true
		s.Hostname = *hostname
	} else if s.Namespaces.UTS {
		s.Hostname = s.ID
	}

	if *memory != "" {
		if s.Limits.Memory, err = cgroup.ParseMemory(*memory); err != nil {
			return spec.Spec{}, err
		}
	}
	s.Limits.CPUs = *cpus
	s.Limits.Pids = *pids

	if s.Caps, err = caps.Parse(*capSpec); err != nil {
		return spec.Spec{}, err
	}

	s.Env = mergeEnv(defaultEnv, env)

	if err := s.Validate(); err != nil {
		return spec.Spec{}, err
	}
	return s, nil
}

// mergeEnv lays overrides on top of base, replacing a variable that both
// set rather than listing it twice.
func mergeEnv(base, overrides []string) []string {
	out := append([]string(nil), base...)
	for _, kv := range overrides {
		key, _, _ := strings.Cut(kv, "=")
		replaced := false
		for i, existing := range out {
			if k, _, _ := strings.Cut(existing, "="); k == key {
				out[i] = kv
				replaced = true
				break
			}
		}
		if !replaced {
			out = append(out, kv)
		}
	}
	return out
}

// IsRoot reports whether the process runs as UID 0.
func IsRoot() bool { return os.Geteuid() == 0 }
