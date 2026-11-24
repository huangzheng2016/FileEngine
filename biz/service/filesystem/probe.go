package filesystem

import (
	"context"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"FileEngine/biz/errs"
	"FileEngine/internal/config"
	"FileEngine/internal/remotefs"
)

const maxProbeDirs = 2000

type ProbeInput struct {
	// 0 probes unsaved settings; otherwise the stored secrets may be reused.
	ID uint
	Conn
}

type ProbeDir struct {
	Name string
	// The base path value that selects (or opens) this folder.
	Path string
}

// ProbeResult: a failed probe is a normal answer (Success false, Error set).
type ProbeResult struct {
	Success        bool
	Error          string
	Detail         string
	HostKey        string
	HostKeyChanged bool
	Dirs           []ProbeDir
}

func (r *ProbeResult) fail(err error) {
	r.Error = remotefs.Explain(err)
	if detail := err.Error(); detail != r.Error {
		r.Detail = detail
	}
}

func childBasePath(protocol, base, name string) string {
	switch protocol {
	case "local":
		return filepath.Join(base, name)
	case "smb":
		return strings.Trim(strings.Trim(base, "/")+"/"+name, "/")
	default:
		return path.Join(base, name)
	}
}

// Test checks a connection and lists the folders at base path, so one call both
// validates the settings and drives the folder picker.
func (s *Service) Test(ctx context.Context, in ProbeInput) (*ProbeResult, error) {
	res := &ProbeResult{Dirs: []ProbeDir{}}
	if !validProtocols[in.Protocol] {
		return nil, errs.Invalid("不支持的协议：" + in.Protocol)
	}
	if err := validateConn(&in.Conn); err != nil {
		res.Error = err.Error()
		return res, nil
	}

	if in.Protocol == "local" && in.BasePath == "" {
		roots := remotefs.LocalRoots()
		if len(roots) == 0 {
			res.fail(remotefs.ErrLocalDisabled)
			return res, nil
		}
		res.Success = true
		for _, r := range roots {
			res.Dirs = append(res.Dirs, ProbeDir{Name: r, Path: r})
		}
		return res, nil
	}

	stored, err := s.storedFor(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	sec, ok := resolveSecrets(stored, &in.Conn)
	if !ok {
		return nil, errs.Invalid(msgReenterPassword)
	}
	cfg := config.RemoteFSConfig{
		Protocol: in.Protocol, BasePath: in.BasePath, Host: in.Host, Port: in.Port, Username: in.Username,
		Password: sec.password, PrivateKey: sec.privateKey, KeyPassphrase: sec.passphrase,
	}

	if in.Protocol == "sftp" {
		fp, err := remotefs.FetchSSHHostKey(in.Host, in.Port)
		if err != nil {
			res.fail(err)
			return res, nil
		}
		res.HostKey = fp
		expected := fp
		switch {
		case in.HostKey != nil && *in.HostKey != "":
			expected = *in.HostKey
		case stored != nil && stored.HostKey != "" && stored.Host == in.Host && stored.Port == in.Port:
			expected = stored.HostKey
		}
		if expected != fp {
			res.HostKeyChanged = true
			res.fail(&remotefs.HostKeyMismatchError{Expected: expected, Got: fp})
			return res, nil
		}
		cfg.HostKey = expected
	}

	if in.Protocol == "smb" && strings.Trim(in.BasePath, "/\\") == "" {
		shares, err := remotefs.ListSMBShares(ctx, cfg)
		if err != nil {
			res.fail(err)
			return res, nil
		}
		sort.Strings(shares)
		for _, n := range shares {
			res.Dirs = append(res.Dirs, ProbeDir{Name: n, Path: n})
		}
		res.Success = true
		return res, nil
	}

	rfs, err := remotefs.NewFromConfig(cfg)
	if err != nil {
		res.fail(err)
		return res, nil
	}
	defer rfs.Close()
	entries, err := rfs.List(ctx, ".")
	if err != nil {
		res.fail(err)
		return res, nil
	}
	for _, e := range entries {
		if !e.IsDir || strings.HasPrefix(e.Name, ".") {
			continue
		}
		res.Dirs = append(res.Dirs, ProbeDir{Name: e.Name, Path: childBasePath(in.Protocol, in.BasePath, e.Name)})
	}
	sort.Slice(res.Dirs, func(i, j int) bool { return strings.ToLower(res.Dirs[i].Name) < strings.ToLower(res.Dirs[j].Name) })
	if len(res.Dirs) > maxProbeDirs {
		res.Dirs = res.Dirs[:maxProbeDirs]
	}
	res.Success = true
	return res, nil
}
