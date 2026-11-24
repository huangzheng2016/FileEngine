// Command gencheck detects stale or hand-edited generated code in two steps, so
// it needs no task runner of its own and works through `wails3 task` in CI:
//
//	go run ./scripts/gencheck snapshot -paths biz,container -state bin/gencheck.state
//	... run the generators ...
//	go run ./scripts/gencheck verify -paths biz,container -state bin/gencheck.state
//
// verify fails when any file under the paths differs from the snapshot.
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func main() {
	if len(os.Args) < 2 || (os.Args[1] != "snapshot" && os.Args[1] != "verify") {
		fmt.Fprintln(os.Stderr, "usage: gencheck snapshot|verify -paths a,b -state file")
		os.Exit(2)
	}
	flags := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
	paths := flags.String("paths", "", "comma-separated files and directories that hold generated code")
	state := flags.String("state", "", "snapshot file")
	_ = flags.Parse(os.Args[2:])
	if *paths == "" || *state == "" {
		fmt.Fprintln(os.Stderr, "gencheck: -paths and -state are required")
		os.Exit(2)
	}
	now, err := hashTree(strings.Split(*paths, ","))
	if err != nil {
		fatal(err)
	}
	if os.Args[1] == "snapshot" {
		if err := os.MkdirAll(filepath.Dir(*state), 0o755); err != nil {
			fatal(err)
		}
		if err := write(*state, now); err != nil {
			fatal(err)
		}
		return
	}
	before, err := read(*state)
	if err != nil {
		fatal(err)
	}
	var changed []string
	for p, h := range now {
		if before[p] != h {
			changed = append(changed, p)
		}
	}
	for p := range before {
		if _, ok := now[p]; !ok {
			changed = append(changed, p)
		}
	}
	if len(changed) > 0 {
		sort.Strings(changed)
		fmt.Fprintf(os.Stderr, "generated code is out of date; run `task gen` and commit:\n  %s\n", strings.Join(changed, "\n  "))
		os.Exit(1)
	}
}

func hashTree(roots []string) (map[string]string, error) {
	out := map[string]string{}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if os.IsNotExist(err) {
					return nil
				}
				return err
			}
			if d.IsDir() {
				return nil
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(data)
			out[filepath.ToSlash(p)] = hex.EncodeToString(sum[:])
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func write(path string, m map[string]string) error {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s %s\n", m[k], k)
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func read(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("no snapshot (run snapshot first): %w", err)
	}
	defer f.Close()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		hash, name, ok := strings.Cut(sc.Text(), " ")
		if ok {
			out[name] = hash
		}
	}
	return out, sc.Err()
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "gencheck:", err)
	os.Exit(1)
}
