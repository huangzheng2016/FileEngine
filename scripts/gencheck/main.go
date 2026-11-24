// Command gencheck runs a code generator and fails when it changed any file
// under the given paths, i.e. when generated code is stale or hand-edited.
//
//	go run ./scripts/gencheck -paths biz,container -- task gen
package main

import (
	"crypto/sha256"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

func main() {
	paths := flag.String("paths", "", "comma-separated files and directories that hold generated code")
	flag.Parse()
	cmdline := flag.Args()
	if *paths == "" || len(cmdline) == 0 {
		fmt.Fprintln(os.Stderr, "usage: gencheck -paths a,b -- command [args...]")
		os.Exit(2)
	}
	roots := strings.Split(*paths, ",")
	before, err := snapshot(roots)
	if err != nil {
		fatal(err)
	}
	cmd := exec.Command(cmdline[0], cmdline[1:]...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		fatal(fmt.Errorf("generator failed: %w", err))
	}
	after, err := snapshot(roots)
	if err != nil {
		fatal(err)
	}
	var changed []string
	for p, h := range after {
		if before[p] != h {
			changed = append(changed, p)
		}
	}
	for p := range before {
		if _, ok := after[p]; !ok {
			changed = append(changed, p)
		}
	}
	if len(changed) > 0 {
		sort.Strings(changed)
		fmt.Fprintf(os.Stderr, "generated code is out of date; regenerate (task gen) and commit:\n  %s\n", strings.Join(changed, "\n  "))
		os.Exit(1)
	}
}

func snapshot(roots []string) (map[string][sha256.Size]byte, error) {
	out := map[string][sha256.Size]byte{}
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
			out[filepath.ToSlash(p)] = sha256.Sum256(data)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "gencheck:", err)
	os.Exit(1)
}
