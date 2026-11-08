// Command stripcheck fails when a release artifact leaks build-host paths,
// debug sections or source maps.
package main

import (
	"bytes"
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: stripcheck <binary-or-dir>...")
		os.Exit(2)
	}
	needles := hostPaths()
	failed := false
	for _, target := range os.Args[1:] {
		for _, problem := range check(target, needles) {
			fmt.Fprintf(os.Stderr, "%s: %s\n", target, problem)
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
}

func hostPaths() []string {
	var out []string
	add := func(p string) {
		if p = strings.TrimSpace(p); len(p) > 1 {
			out = append(out, p)
		}
	}
	if wd, err := os.Getwd(); err == nil {
		add(wd)
	}
	if home, err := os.UserHomeDir(); err == nil {
		add(home)
	}
	for _, v := range []string{"GOMODCACHE", "GOROOT", "GOPATH"} {
		if b, err := exec.Command("go", "env", v).Output(); err == nil {
			add(string(b))
		}
	}
	return out
}

func check(target string, needles []string) []string {
	info, err := os.Stat(target)
	if err != nil {
		return []string{err.Error()}
	}
	if info.IsDir() {
		var problems []string
		_ = filepath.WalkDir(target, func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(p, ".map") {
				problems = append(problems, "source map shipped: "+p)
			}
			return nil
		})
		return problems
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return []string{err.Error()}
	}
	var problems []string
	for _, n := range needles {
		if bytes.Contains(data, []byte(n)) {
			problems = append(problems, "contains build-host path "+n)
		}
	}
	if sections := debugSections(target); len(sections) > 0 {
		problems = append(problems, "debug sections present: "+strings.Join(sections, ", "))
	}
	return problems
}

func debugSections(path string) []string {
	var names []string
	collect := func(name string) {
		if strings.HasPrefix(name, ".debug") || strings.HasPrefix(name, "__debug") || strings.HasPrefix(name, ".zdebug") || name == ".symtab" {
			names = append(names, name)
		}
	}
	if f, err := elf.Open(path); err == nil {
		defer f.Close()
		for _, s := range f.Sections {
			collect(s.Name)
		}
		return names
	}
	if f, err := macho.Open(path); err == nil {
		defer f.Close()
		for _, s := range f.Sections {
			collect(s.Name)
		}
		return names
	}
	if f, err := pe.Open(path); err == nil {
		defer f.Close()
		for _, s := range f.Sections {
			collect(s.Name)
		}
	}
	return names
}
