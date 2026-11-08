package db

import (
	"strings"
	"testing"
	"time"
)

// decisionFixture:
//
//	proj/ (dir)  proj/main.go  proj/pkg/ (dir)  proj/pkg/u.go
//	loose.txt
func decisionFixture(t *testing.T) *Repository {
	r := setupBrainRepo(t)
	for _, e := range []struct{ p, typ string }{
		{"proj", "directory"}, {"proj/main.go", "file"}, {"proj/pkg", "directory"}, {"proj/pkg/u.go", "file"}, {"loose.txt", "file"},
	} {
		seedEntry(t, r, 1, e.p, e.typ, 1)
	}
	return r
}

func entry(t *testing.T, r *Repository, p string) *FileEntry {
	t.Helper()
	e, err := r.GetFileByPath(1, p)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestDecideUnitMakesMembersAndBlocksDescendants(t *testing.T) {
	r := decisionFixture(t)
	res, err := r.Decide(1, Decision{Path: "/proj", Role: RoleUnit, NewPath: "代码/proj", Reason: "Go 项目"})
	if err != nil || res.NewPath != "/代码/proj" || res.Members != 3 {
		t.Fatalf("unit: %+v %v", res, err)
	}
	m := entry(t, r, "proj/pkg/u.go")
	if m.Role != RoleMember || !m.Tagged || m.Reason != "随「proj」整体移动" {
		t.Fatalf("member: %+v", m)
	}
	_, err = r.Decide(1, Decision{Path: "proj/pkg/u.go", Role: RoleFile, NewPath: "/x/u.go"})
	if !IsRejection(err) || !strings.Contains(err.Error(), "split_unit") || !strings.Contains(err.Error(), "/代码/proj") {
		t.Fatalf("decision under a unit must be rejected with split guidance: %v", err)
	}
	// Re-deciding the unit itself is fine (members are not "decisions").
	if _, err := r.Decide(1, Decision{Path: "proj", Role: RoleUnit, NewPath: "/代码/p2"}); err != nil {
		t.Fatalf("re-decide unit: %v", err)
	}

	n, err := r.SplitUnit(1, "proj")
	if err != nil || n != 3 {
		t.Fatalf("split: %d %v", n, err)
	}
	if p := entry(t, r, "proj"); p.Role != RoleContainer || p.NewPath != "" {
		t.Fatalf("split dir: %+v", p)
	}
	if m := entry(t, r, "proj/main.go"); m.Role != RoleUndecided || m.Tagged {
		t.Fatalf("released member: %+v", m)
	}
	if _, err := r.SplitUnit(1, "proj"); !IsRejection(err) {
		t.Fatal("splitting a container is rejected")
	}
}

func TestDecideUnitOverDecidedDescendantsNeedsOverride(t *testing.T) {
	r := decisionFixture(t)
	if _, err := r.Decide(1, Decision{Path: "proj/main.go", Role: RoleFile, NewPath: "/代码/main.go"}); err != nil {
		t.Fatal(err)
	}
	_, err := r.Decide(1, Decision{Path: "proj", Role: RoleUnit, NewPath: "/代码/proj"})
	if !IsRejection(err) || !strings.Contains(err.Error(), "override") || !strings.Contains(err.Error(), "proj/main.go") {
		t.Fatalf("must require override: %v", err)
	}
	if p := entry(t, r, "proj"); p.Role != RoleUndecided {
		t.Fatal("rejected decision writes nothing")
	}
	res, err := r.Decide(1, Decision{Path: "proj", Role: RoleUnit, NewPath: "/代码/proj", Override: true})
	if err != nil || res.OverriddenCount != 1 || res.Overridden[0] != "proj/main.go" {
		t.Fatalf("override: %+v %v", res, err)
	}
	if m := entry(t, r, "proj/main.go"); m.Role != RoleMember || m.NewPath != "" {
		t.Fatalf("overridden decision cleared: %+v", m)
	}
}

func TestDecideRolesAndReview(t *testing.T) {
	r := decisionFixture(t)
	if _, err := r.Decide(1, Decision{Path: "loose.txt", Role: RoleContainer}); !IsRejection(err) {
		t.Fatal("a file cannot be a container")
	}
	if _, err := r.Decide(1, Decision{Path: "loose.txt", Role: RoleFile}); !IsRejection(err) {
		t.Fatal("file needs a target")
	}
	if _, err := r.Decide(1, Decision{Path: "loose.txt", Role: "bogus", NewPath: "/a"}); !IsRejection(err) {
		t.Fatal("unknown role")
	}
	if _, err := r.Decide(1, Decision{Path: "nope", Role: RoleSkip}); !IsRejection(err) {
		t.Fatal("missing entry")
	}
	if _, err := r.Decide(1, Decision{Path: "loose.txt", Role: RoleFile, NewPath: "/../x"}); !IsRejection(err) {
		t.Fatal("illegal target")
	}
	res, err := r.Decide(1, Decision{Path: "loose.txt", Role: RoleUnit, NewPath: "/文档/loose.txt", Reason: "说明", Confidence: 0.4})
	if err != nil || res.Role != RoleFile || !res.NeedsReview {
		t.Fatalf("unit on a file maps to file; low confidence needs review: %+v %v", res, err)
	}
	e := entry(t, r, "loose.txt")
	if !e.NeedsReview || e.Confidence != 0.4 || e.Reason != "说明" || e.Operation != "planned" {
		t.Fatalf("stored: %+v", e)
	}
	if planned, _ := r.GetPlannedFiles(1); len(planned) != 0 {
		t.Fatal("needs_review entries are not executable")
	}
	if n, _ := r.ConfirmReview(1, []uint{e.ID}); n != 1 {
		t.Fatal("confirm")
	}
	if planned, _ := r.GetPlannedFiles(1); len(planned) != 1 {
		t.Fatal("confirmed entries are executable")
	}

	// A skipped directory governs its subtree like a unit.
	if _, err := r.Decide(1, Decision{Path: "proj", Role: RoleSkip, Reason: "不动"}); err != nil {
		t.Fatal(err)
	}
	if m := entry(t, r, "proj/main.go"); m.Role != RoleMember || m.Reason != "随「proj」整体保持原位" {
		t.Fatalf("skip members: %+v", m)
	}
	if _, err := r.Decide(1, Decision{Path: "proj/main.go", Role: RoleSkip}); !IsRejection(err) || !strings.Contains(err.Error(), "保持原位") {
		t.Fatalf("under skipped dir: %v", err)
	}
	// Undeciding the owner releases its members.
	if _, err := r.Decide(1, Decision{Path: "proj", Role: RoleUndecided}); err != nil {
		t.Fatal(err)
	}
	if m := entry(t, r, "proj/main.go"); m.Role != RoleUndecided {
		t.Fatalf("released: %+v", m)
	}
}

func TestDecideBatchReportsPerItem(t *testing.T) {
	r := decisionFixture(t)
	out, err := r.DecideBatch(1, []Decision{
		{Path: "proj", Role: RoleUnit, NewPath: "/代码/proj"},
		{Path: "proj/main.go", Role: RoleFile, NewPath: "/x"},
		{Path: "loose.txt", Role: RoleFile, NewPath: "/文档/loose.txt"},
	})
	if err != nil || out[0].Err != nil || !IsRejection(out[1].Err) || out[2].Err != nil {
		t.Fatalf("batch: %+v %v", out, err)
	}
}

func TestScopeAndSensitive(t *testing.T) {
	r := setupBrainRepo(t)
	s := &ScanSession{FilesystemID: 1, ScopeExts: "JPG, .png"}
	if err := r.CreateSession(s); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"a.jpg", "b.PNG", "c.txt", "id_rsa", "我的身份证.jpg", "notes.md", "taxonomy.pdf"} {
		seedEntry(t, r, s.ID, p, "file", 1)
	}
	if n, err := r.ApplyScope(s); err != nil || n != 4 {
		t.Fatalf("scope skip: %d %v", n, err)
	}
	if e, _ := r.GetFileByPath(s.ID, "c.txt"); e.Role != RoleSkip || e.Reason != "不在整理范围" || !e.Tagged {
		t.Fatalf("out of scope: %+v", e)
	}
	if e, _ := r.GetFileByPath(s.ID, "b.PNG"); e.Role != RoleUndecided {
		t.Fatal("in scope stays undecided")
	}
	if n, err := r.MarkSensitive(s.ID); err != nil || n != 2 {
		t.Fatalf("sensitive: %d %v", n, err)
	}
	sens := true
	files, _, _ := r.ListFiles(FileQuery{SessionID: s.ID, Sensitive: &sens})
	if len(files) != 2 {
		t.Fatalf("sensitive filter: %+v", files)
	}
	for _, name := range []string{"id_ed25519", "server.pem", ".env", "aws_credentials.csv", "2023年个税.pdf", "W-2 2023.pdf", "passport scan.jpg"} {
		if !IsSensitiveName(name) {
			t.Fatalf("%s must be sensitive", name)
		}
	}
	for _, name := range []string{"slides.key", "tokenizer.py", "sw2.jpg", "photo_1099x.jpg", "taxonomy.pdf"} {
		if IsSensitiveName(name) {
			t.Fatalf("%s must not be sensitive", name)
		}
	}
	if FileExt("a.TAR.GZ") != "tar.gz" || FileExt(".bashrc") != "" || FileExt("x") != "" {
		t.Fatal("FileExt")
	}
}

func TestRuleValidation(t *testing.T) {
	r := setupBrainRepo(t)
	for _, bad := range []PlanRule{
		{SessionID: 1, Target: "a/{name}"},
		{SessionID: 1, Name: "x"},
		{SessionID: 1, Name: "x", Target: "a/{nope}"},
		{SessionID: 1, Name: "x", Target: "../{name}"},
		{SessionID: 1, Name: "x", Target: "a", NameRegex: "("},
		{SessionID: 1, Name: "x", Target: "a", Role: "container"},
		{SessionID: 1, Name: "x", Target: "a", MinSize: 10, MaxSize: 5},
	} {
		b := bad
		if err := r.CreateRule(&b); !IsRejection(err) {
			t.Fatalf("rule %+v must be rejected: %v", bad, err)
		}
	}
	a := &PlanRule{SessionID: 1, Name: "a", Exts: " .JPG,png ", Target: "照片/{year}"}
	if err := r.CreateRule(a); err != nil || a.Position != 1 || a.Exts != "jpg,png" || a.Role != RoleFile {
		t.Fatalf("create: %+v %v", a, err)
	}
	b := &PlanRule{SessionID: 1, Name: "b", Target: "其他", Role: RoleUnit}
	if err := r.CreateRule(b); err != nil || b.Position != 2 || b.FileType != "directory" {
		t.Fatalf("append + unit implies directory: %+v %v", b, err)
	}
	if _, err := r.UpdateRule(a.ID, func(p *PlanRule) { p.Position = 5 }); err != nil {
		t.Fatal(err)
	}
	rules, _ := r.ListRules(1)
	if rules[0].Name != "b" {
		t.Fatalf("position order: %+v", rules)
	}
	if err := r.DeleteRule(b.ID); err != nil || r.DeleteRule(b.ID) != ErrRuleNotFound {
		t.Fatal("delete")
	}
}

func TestRenderRuleTarget(t *testing.T) {
	mt := time.Date(2021, 3, 4, 0, 0, 0, 0, time.UTC)
	taken := time.Date(2019, 11, 2, 0, 0, 0, 0, time.UTC)
	e := &FileEntry{OriginalPath: "dcim/trip/IMG_1.JPG", Name: "IMG_1.JPG", ParentPath: "dcim/trip", ModTime: mt, TakenAt: &taken, Camera: "Canon/EOS"}
	cases := map[string]string{
		"照片/{year}/{month}":                    "/照片/2019/11/IMG_1.JPG",
		"照片/{taken_year}-{taken_month}/{name}": "/照片/2019-11/IMG_1.JPG",
		"相机/{camera}/{stem}.{ext}":             "/相机/Canon_EOS/IMG_1.jpg",
		"按目录/{parent}/{day}":                   "/按目录/trip/02/IMG_1.JPG",
		"保留/{rel}":                             "/保留/trip/IMG_1.JPG",
	}
	for tpl, want := range cases {
		rule := &PlanRule{Name: "x", Target: tpl, PathPrefix: "dcim"}
		got, err := RenderRuleTarget(rule, e)
		if err != nil || got != want {
			t.Fatalf("%s: got %q %v, want %q", tpl, got, err, want)
		}
	}
	noTaken := &FileEntry{OriginalPath: "a.jpg", Name: "a.jpg", ModTime: mt}
	if got, _ := RenderRuleTarget(&PlanRule{Target: "照片/{year}/{taken_year}/{camera}"}, noTaken); got != "/照片/2021/未知日期/未知相机/a.jpg" {
		t.Fatalf("fallbacks: %s", got)
	}
}

func TestApplyRulesFirstMatchPreviewAndCollisions(t *testing.T) {
	r := setupBrainRepo(t)
	mt := time.Date(2022, 7, 1, 0, 0, 0, 0, time.UTC)
	for _, p := range []string{"a/x.jpg", "b/x.jpg", "c/y.jpg", "d/z.png", "e/doc.pdf"} {
		f := &FileEntry{ScanSessionID: 1, OriginalPath: p, Name: p[2:], ParentPath: p[:1], FileType: "file", ModTime: mt}
		if err := r.CreateFile(f); err != nil {
			t.Fatal(err)
		}
	}
	seedEntry(t, r, 1, "proj", "directory", 0)
	seedEntry(t, r, 1, "proj/node.jpg", "file", 1)
	rules := []*PlanRule{
		{SessionID: 1, Name: "项目", NameRegex: "^proj$", Target: "代码", Role: RoleUnit},
		{SessionID: 1, Name: "jpg", Exts: "jpg", Target: "照片/{year}"},
		{SessionID: 1, Name: "图片兜底", Exts: "jpg,png", Target: "图片"},
	}
	for _, rule := range rules {
		if err := r.CreateRule(rule); err != nil {
			t.Fatal(err)
		}
	}
	preview, err := r.ApplyRules(1, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, st := range preview {
		counts[st.Name] = st.Matched
	}
	if counts["项目"] != 1 || counts["jpg"] != 4 || counts["图片兜底"] != 1 {
		t.Fatalf("preview counts (first match wins): %+v", preview)
	}
	if e := entry(t, r, "a/x.jpg"); e.Role != RoleUndecided {
		t.Fatal("preview writes nothing")
	}

	stats, err := r.ApplyRules(1, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, st := range stats {
		got[st.Name] = st.Matched
	}
	// proj/node.jpg is a member of the unit decided by the first rule.
	if got["项目"] != 1 || got["jpg"] != 3 || got["图片兜底"] != 1 {
		t.Fatalf("apply counts: %+v", stats)
	}
	if e := entry(t, r, "proj/node.jpg"); e.Role != RoleMember {
		t.Fatalf("member: %+v", e)
	}
	a, b := entry(t, r, "a/x.jpg"), entry(t, r, "b/x.jpg")
	if a.NewPath != "/照片/2022/x.jpg" || b.NewPath != "/照片/2022/x (2).jpg" || a.Reason != "规则：jpg" {
		t.Fatalf("collision naming: %q %q %q", a.NewPath, b.NewPath, a.Reason)
	}
	if e := entry(t, r, "e/doc.pdf"); e.Role != RoleUndecided {
		t.Fatal("unmatched stays undecided")
	}
	stored, _ := r.ListRules(1)
	if stored[1].Matched != 3 {
		t.Fatalf("matched stored: %+v", stored[1])
	}
	// Re-applying finds nothing new.
	again, _ := r.ApplyRules(1, 0, false)
	for _, st := range again {
		if st.Matched != 0 {
			t.Fatalf("idempotent: %+v", again)
		}
	}
}
