package remotefs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// LocalPolicy bounds what a filesystem connection may read from the machine
// FileEngine itself runs on: LOCAL protocol base paths and SFTP private-key
// files. Without it, anyone able to create a filesystem could point it at
// /etc or at FileEngine's own database and read it back through the agent.
type LocalPolicy struct {
	// Roots are the host directories LOCAL base paths and SFTP key files must
	// live under. Empty disables the LOCAL protocol and key files entirely.
	Roots []string
	// Denied are host paths that stay unreachable even inside a root: the app
	// data dir, the DB file, the config file, the secret key file.
	Denied []string
}

// EnvLocalRoots names the comma-separated allowlist read by LocalRootsFromEnv.
const EnvLocalRoots = "FE_LOCAL_ROOTS"

var (
	policyMu sync.RWMutex
	policy   LocalPolicy // resolved (absolute, symlink-free) paths
)

// LocalRootsFromEnv parses FE_LOCAL_ROOTS (comma-separated, blanks ignored).
func LocalRootsFromEnv() []string {
	var roots []string
	for _, r := range strings.Split(os.Getenv(EnvLocalRoots), ",") {
		if r = strings.TrimSpace(r); r != "" {
			roots = append(roots, r)
		}
	}
	return roots
}

// SetLocalPolicy installs the process-wide policy. Paths are made absolute
// and symlink-resolved (nearest existing ancestor) so checks compare like
// with like. Server mode: Roots = FE_LOCAL_ROOTS. Desktop mode: the user's
// home directory is added by the embedder.
func SetLocalPolicy(p LocalPolicy) error {
	var out LocalPolicy
	for _, r := range p.Roots {
		abs, err := canonical(r)
		if err != nil {
			return fmt.Errorf("local root %q: %w", r, err)
		}
		out.Roots = append(out.Roots, abs)
	}
	for _, d := range p.Denied {
		if d == "" {
			continue
		}
		abs, err := canonical(d)
		if err != nil {
			return fmt.Errorf("denied path %q: %w", d, err)
		}
		out.Denied = append(out.Denied, abs)
	}
	policyMu.Lock()
	policy = out
	policyMu.Unlock()
	return nil
}

func currentPolicy() LocalPolicy {
	policyMu.RLock()
	defer policyMu.RUnlock()
	return policy
}

func canonical(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	return resolveExisting(abs)
}

// ErrLocalDisabled is returned when no local roots are configured.
var ErrLocalDisabled = errors.New("本机路径访问未启用：请在 FE_LOCAL_ROOTS 中配置允许的目录")

// CheckLocalPath reports whether host path p (a LOCAL base path or an SFTP
// key file) is allowed: inside some configured root and outside every denied
// path. A base path may still contain a denied path — LocalFS hides and
// refuses those per operation.
func CheckLocalPath(p string) error {
	pol := currentPolicy()
	if len(pol.Roots) == 0 {
		return ErrLocalDisabled
	}
	if p == "" {
		return errors.New("本机路径不能为空")
	}
	resolved, err := canonical(p)
	if err != nil {
		return fmt.Errorf("本机路径无效: %w", err)
	}
	for _, d := range pol.Denied {
		if withinBase(resolved, d) {
			return fmt.Errorf("本机路径 %q 位于 FileEngine 数据目录内，禁止访问", p)
		}
	}
	for _, r := range pol.Roots {
		if withinBase(resolved, r) {
			return nil
		}
	}
	return fmt.Errorf("本机路径 %q 不在允许的目录（FE_LOCAL_ROOTS）内", p)
}

// newPolicedLocalFS opens a LOCAL backend under the current policy.
func newPolicedLocalFS(basePath string) (*LocalFS, error) {
	if err := CheckLocalPath(basePath); err != nil {
		return nil, err
	}
	l, err := NewLocalFS(basePath)
	if err != nil {
		return nil, err
	}
	l.denied = currentPolicy().Denied
	return l, nil
}
