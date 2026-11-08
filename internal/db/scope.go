package db

import (
	"path"
	"regexp"
	"strings"
)

// FileExt returns the lower-case extension of a file name without the dot
// ("tar.gz" for compressed tarballs, "" for none and for dot-files).
func FileExt(name string) string {
	lower := strings.ToLower(name)
	if strings.HasSuffix(lower, ".tar.gz") && len(lower) > len(".tar.gz") {
		return "tar.gz"
	}
	ext := path.Ext(lower)
	if ext == lower || ext == "" {
		return ""
	}
	return ext[1:]
}

// ParseExts parses a comma/space separated extension list ("jpg, .PNG").
func ParseExts(s string) map[string]bool {
	out := map[string]bool{}
	for _, f := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return r == ',' || r == ' ' || r == '，' || r == ';' }) {
		if f = strings.TrimPrefix(strings.TrimSpace(f), "."); f != "" {
			out[f] = true
		}
	}
	return out
}

var sensitiveNameRe = regexp.MustCompile(`(?i)(^id_(rsa|dsa|ecdsa|ed25519)(\.pub)?$|\.(pem|p12|pfx|kdbx|keystore|jks|ovpn|gpg)$|^\.env(\..*)?$|^\.netrc$|^\.pgpass$|^wallet\.dat$|credential|secret|password|passwd|private[_-]?key|passport|身份证|护照|驾驶证|户口|社保|银行卡|密码|口令|密钥|证书|纳税|税单|个税|完税|tax[_ -]?(return|form)|(^|[^a-z0-9])(w-?2|1099)([^a-z0-9]|$))`)

// IsSensitiveName reports whether a file name looks like a credential, key or
// identity/tax document. Such files are flagged and never read by the agent.
func IsSensitiveName(name string) bool {
	return sensitiveNameRe.MatchString(path.Base(name))
}

// scanFiles walks the session's files with the given extra condition in id
// pages, handing each page to fn.
func (r *Repository) scanFiles(sessionID uint, cond string, args []any, fn func([]FileEntry) error) error {
	var after uint
	for {
		var rows []FileEntry
		q := r.db.Select("id", "name", "original_path").Where("scan_session_id = ? AND file_type = 'file' AND id > ?", sessionID, after)
		if cond != "" {
			q = q.Where(cond, args...)
		}
		if err := q.Order("id").Limit(5000).Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		if err := fn(rows); err != nil {
			return err
		}
		after = rows[len(rows)-1].ID
	}
}

func (r *Repository) updateIDs(ids []uint, fields map[string]any) error {
	for start := 0; start < len(ids); start += 500 {
		end := min(start+500, len(ids))
		if err := WithRetry(func() error {
			return r.db.Model(&FileEntry{}).Where("id IN ?", ids[start:end]).Updates(fields).Error
		}); err != nil {
			return err
		}
	}
	return nil
}

// ApplyScope marks undecided files whose extension is outside the session's
// ScopeExts as skip; an empty scope keeps everything. Returns the count.
func (r *Repository) ApplyScope(session *ScanSession) (int64, error) {
	scope := ParseExts(session.ScopeExts)
	if len(scope) == 0 {
		return 0, nil
	}
	defer r.touchCounts(session.ID)
	var ids []uint
	err := r.scanFiles(session.ID, "role = ?", []any{RoleUndecided}, func(rows []FileEntry) error {
		for _, f := range rows {
			if !scope[FileExt(f.Name)] {
				ids = append(ids, f.ID)
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return int64(len(ids)), r.updateIDs(ids, map[string]any{"role": RoleSkip, "tagged": true, "reason": "不在整理范围"})
}

// MarkSensitive flags the session's files whose names look sensitive.
func (r *Repository) MarkSensitive(sessionID uint) (int64, error) {
	var ids []uint
	err := r.scanFiles(sessionID, "sensitive = ?", []any{false}, func(rows []FileEntry) error {
		for _, f := range rows {
			if IsSensitiveName(f.Name) {
				ids = append(ids, f.ID)
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return int64(len(ids)), r.updateIDs(ids, map[string]any{"sensitive": true})
}
