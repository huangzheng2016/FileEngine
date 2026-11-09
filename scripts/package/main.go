// Command package turns built binaries into the files a user downloads:
//
//	server:  FileEngine-Server-<ver>-<os>-<arch>.tar.gz / .zip
//	desktop: FileEngine-<ver>-Linux-<arch>.deb, FileEngine-<ver>-macOS-<chip>.dmg,
//	         FileEngine-<ver>-Windows-<arch>-Setup.exe
//	syso:    the Windows icon, manifest and version resource linked into the desktop exe
//
// Desktop packages are built with the platform's own tools: dpkg-deb (Linux),
// codesign and hdiutil (macOS host), makensis and the Wails CLI (any host).
//
//	go run ./scripts/package -kind desktop -os darwin -arch arm64 -version 1.2.3 -bin bin/fileengine-desktop -out dist
package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	appID       = "com.fileengine.desktop"
	brandDir    = "build/brand"
	nsisScript  = "build/windows/installer.nsi"
	description = "用 AI 整理 NAS 上的文件"
)

type target struct {
	kind, goos, arch, version, bin, out string
}

func main() {
	var t target
	flag.StringVar(&t.kind, "kind", "desktop", "server, desktop or syso")
	flag.StringVar(&t.goos, "os", "", "target OS")
	flag.StringVar(&t.arch, "arch", "", "target architecture")
	flag.StringVar(&t.version, "version", "", "release version (git describe output is accepted)")
	flag.StringVar(&t.bin, "bin", "", "built binary")
	flag.StringVar(&t.out, "out", "dist", "output directory")
	flag.Parse()
	if t.goos == "" || t.arch == "" || t.bin == "" {
		fail(errors.New("-os, -arch and -bin are required (for syso, -bin is the .syso to write)"))
	}
	if err := os.MkdirAll(t.out, 0o755); err != nil {
		fail(err)
	}
	var (
		file string
		err  error
	)
	switch {
	case t.kind == "server":
		file, err = server(t)
	case t.kind == "syso":
		file, err = syso(t)
	case t.goos == "linux":
		file, err = deb(t)
	case t.goos == "darwin":
		file, err = dmg(t)
	case t.goos == "windows":
		file, err = installer(t)
	default:
		err = fmt.Errorf("no package for %s %s", t.kind, t.goos)
	}
	if err != nil {
		fail(err)
	}
	fmt.Println(file)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "package:", err)
	os.Exit(1)
}

var semver = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(.*)$`)

// core returns x.y.z and the rest (pre-release or git describe suffix).
func core(version string) (string, string) {
	m := semver.FindStringSubmatch(version)
	if m == nil {
		return "0.0.0", ""
	}
	return m[1] + "." + m[2] + "." + m[3], strings.TrimLeft(m[4], "-+")
}

func display(version string) string {
	c, rest := core(version)
	if rest == "" {
		return c
	}
	return c + "-" + rest
}

func debVersion(version string) string {
	c, rest := core(version)
	if rest == "" {
		return c
	}
	return c + "~" + regexp.MustCompile(`[^A-Za-z0-9.+~]`).ReplaceAllString(rest, ".")
}

func osLabel(goos string) string {
	return map[string]string{"linux": "linux", "darwin": "macos", "windows": "windows"}[goos]
}

func server(t target) (string, error) {
	name := fmt.Sprintf("FileEngine-Server-%s-%s-%s", display(t.version), osLabel(t.goos), t.arch)
	exe := "fileengine"
	if t.goos == "windows" {
		exe += ".exe"
	}
	files := []archiveFile{{t.bin, exe, 0o755}, {"LICENSE", "LICENSE", 0o644}}
	if t.goos == "windows" {
		file := filepath.Join(t.out, name+".zip")
		return file, writeZip(file, name, files)
	}
	file := filepath.Join(t.out, name+".tar.gz")
	return file, writeTarGz(file, name, files)
}

func deb(t target) (string, error) {
	work, err := os.MkdirTemp("", "fe-deb-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(work)
	root := filepath.Join(work, "root")
	copies := map[string]string{
		"usr/bin/fileengine-desktop":                           t.bin,
		"usr/share/icons/hicolor/256x256/apps/fileengine.png":  filepath.Join(brandDir, "icon-256.png"),
		"usr/share/icons/hicolor/scalable/apps/fileengine.svg": filepath.Join(brandDir, "logo.svg"),
		"usr/share/doc/fileengine/copyright":                   "LICENSE",
	}
	for dst, src := range copies {
		mode := os.FileMode(0o644)
		if strings.HasPrefix(dst, "usr/bin/") {
			mode = 0o755
		}
		if err := copyFile(src, filepath.Join(root, dst), mode); err != nil {
			return "", err
		}
	}
	entry := "[Desktop Entry]\nType=Application\nName=FileEngine\nComment=" + description +
		"\nExec=fileengine-desktop\nIcon=fileengine\nTerminal=false\nCategories=Utility;FileTools;\nStartupWMClass=" + appID + "\n"
	if err := writeFile(filepath.Join(root, "usr/share/applications/fileengine.desktop"), entry, 0o644); err != nil {
		return "", err
	}
	control := fmt.Sprintf(`Package: fileengine
Version: %s
Section: utils
Priority: optional
Architecture: %s
Maintainer: FileEngine <https://github.com/huangzheng2016/FileEngine>
Homepage: https://github.com/huangzheng2016/FileEngine
Depends: libgtk-4-1 (>= 4.14), libwebkitgtk-6.0-4t64 | libwebkitgtk-6.0-4
Description: %s
 FileEngine scans a NAS or local folder, lets an AI model propose where every
 file belongs, and moves or copies them only after you review the plan.
`, debVersion(t.version), t.arch, "Organize NAS files with AI")
	if err := writeFile(filepath.Join(root, "DEBIAN/control"), control, 0o644); err != nil {
		return "", err
	}
	file := filepath.Join(t.out, fmt.Sprintf("FileEngine-%s-linux-%s.deb", display(t.version), t.arch))
	return file, run("dpkg-deb", "--root-owner-group", "-Zxz", "--build", root, file)
}

func dmg(t target) (string, error) {
	work, err := os.MkdirTemp("", "fe-dmg-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(work)
	app := filepath.Join(work, "FileEngine.app")
	if err := copyFile(t.bin, filepath.Join(app, "Contents/MacOS/FileEngine"), 0o755); err != nil {
		return "", err
	}
	if err := copyFile(filepath.Join(brandDir, "icon.icns"), filepath.Join(app, "Contents/Resources/icon.icns"), 0o644); err != nil {
		return "", err
	}
	c, _ := core(t.version)
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleName</key><string>FileEngine</string>
	<key>CFBundleDisplayName</key><string>FileEngine</string>
	<key>CFBundleExecutable</key><string>FileEngine</string>
	<key>CFBundleIdentifier</key><string>` + appID + `</string>
	<key>CFBundleIconFile</key><string>icon.icns</string>
	<key>CFBundlePackageType</key><string>APPL</string>
	<key>CFBundleShortVersionString</key><string>` + c + `</string>
	<key>CFBundleVersion</key><string>` + c + `</string>
	<key>CFBundleDevelopmentRegion</key><string>zh_CN</string>
	<key>LSApplicationCategoryType</key><string>public.app-category.utilities</string>
	<key>LSMinimumSystemVersion</key><string>12.0</string>
	<key>NSHighResolutionCapable</key><true/>
	<key>NSLocalNetworkUsageDescription</key><string>FileEngine 需要访问局域网中的 NAS 和模型服务。</string>
</dict>
</plist>
`
	if err := writeFile(filepath.Join(app, "Contents/Info.plist"), plist, 0o644); err != nil {
		return "", err
	}
	// Ad-hoc signature: Apple silicon refuses unsigned code; a Developer ID can replace "-".
	identity := os.Getenv("FE_MACOS_SIGN_IDENTITY")
	if identity == "" {
		identity = "-"
	}
	if err := run("codesign", "--force", "--deep", "--sign", identity, app); err != nil {
		return "", err
	}
	if err := run("codesign", "--verify", "--deep", "--strict", app); err != nil {
		return "", err
	}
	if err := os.Symlink("/Applications", filepath.Join(work, "Applications")); err != nil {
		return "", err
	}
	chip := map[string]string{"arm64": "apple-silicon", "amd64": "intel"}[t.arch]
	file := filepath.Join(t.out, fmt.Sprintf("FileEngine-%s-macos-%s.dmg", display(t.version), chip))
	_ = os.Remove(file)
	if err := run("hdiutil", "create", "-volname", "FileEngine", "-srcfolder", work, "-ov", "-format", "UDZO", file); err != nil {
		return "", err
	}
	return file, run("hdiutil", "verify", file)
}

func installer(t target) (string, error) {
	work, err := os.MkdirTemp("", "fe-nsis-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(work)
	if err := run("wails3", "generate", "webview2bootstrapper", "-dir", work); err != nil {
		return "", err
	}
	abs := func(p string) string { a, _ := filepath.Abs(p); return a }
	c, _ := core(t.version)
	file := filepath.Join(t.out, fmt.Sprintf("FileEngine-%s-windows-%s-setup.exe", display(t.version), t.arch))
	return file, run("makensis", "-V2",
		"-DFE_BINARY="+abs(t.bin),
		"-DFE_VERSION="+display(t.version),
		"-DFE_NUMERIC_VERSION="+c+".0",
		"-DFE_OUT="+abs(file),
		"-DFE_ICON="+abs(filepath.Join(brandDir, "icon.ico")),
		"-DFE_WEBVIEW2="+filepath.Join(work, "MicrosoftEdgeWebview2Setup.exe"),
		nsisScript)
}

func syso(t target) (string, error) {
	c, _ := core(t.version)
	info := fmt.Sprintf(`{"fixed":{"file_version":"%[1]s.0","product_version":"%[1]s.0"},"info":{"0000":{"ProductName":"FileEngine","ProductVersion":"%[2]s","FileVersion":"%[2]s","FileDescription":"FileEngine","CompanyName":"FileEngine","LegalCopyright":"MIT License"}}}`, c, display(t.version))
	f, err := os.CreateTemp("", "fe-info-*.json")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(info); err != nil {
		return "", err
	}
	f.Close()
	return t.bin, run("wails3", "generate", "syso", "-arch", t.arch,
		"-icon", filepath.Join(brandDir, "icon.ico"),
		"-manifest", "build/windows/fileengine.exe.manifest",
		"-info", f.Name(), "-out", t.bin)
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

func writeFile(path, content string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), mode)
}

func copyFile(src, dst string, mode os.FileMode) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(dst, data, mode); err != nil {
		return err
	}
	return os.Chmod(dst, mode)
}

type archiveFile struct {
	src, name string
	mode      os.FileMode
}

func writeTarGz(file, dir string, files []archiveFile) error {
	f, err := os.Create(file)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, a := range files {
		data, err := os.ReadFile(a.src)
		if err != nil {
			return err
		}
		h := &tar.Header{Name: dir + "/" + a.name, Mode: int64(a.mode), Size: int64(len(data)), ModTime: time.Unix(0, 0), Format: tar.FormatPAX}
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		if _, err := tw.Write(data); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	return f.Close()
}

func writeZip(file, dir string, files []archiveFile) error {
	f, err := os.Create(file)
	if err != nil {
		return err
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for _, a := range files {
		src, err := os.Open(a.src)
		if err != nil {
			return err
		}
		h := &zip.FileHeader{Name: dir + "/" + a.name, Method: zip.Deflate}
		h.SetMode(a.mode)
		w, err := zw.CreateHeader(h)
		if err == nil {
			_, err = io.Copy(w, src)
		}
		src.Close()
		if err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return f.Close()
}
