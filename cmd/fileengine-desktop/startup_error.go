package main

import (
	"html"
	"log/slog"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// startupErrorHTML renders a self-contained bilingual page; it must not depend
// on the server or the SPA, which are exactly what failed to start.
func startupErrorHTML(paths desktopPaths, startupErr error) string {
	esc := html.EscapeString
	var b strings.Builder
	b.WriteString(`<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<title>FileEngine 启动失败 / Startup failed</title>
<style>
body { font-family: system-ui, sans-serif; margin: 0; padding: 32px; background: #f6f7f9; color: #1c1e21; }
main { max-width: 640px; margin: 0 auto; background: #fff; border: 1px solid #e0e3e8; border-radius: 8px; padding: 24px 28px; }
h1 { font-size: 18px; margin: 0 0 12px; }
h2 { font-size: 14px; margin: 20px 0 6px; color: #555; }
pre { white-space: pre-wrap; word-break: break-all; background: #f0f1f4; border-radius: 6px; padding: 10px 12px; font-size: 13px; margin: 0; }
p { margin: 6px 0; }
@media (prefers-color-scheme: dark) {
  body { background: #17181a; color: #e4e6eb; }
  main { background: #1f2023; border-color: #33353a; }
  h2 { color: #a8abb2; }
  pre { background: #2a2c30; }
}
</style>
</head>
<body>
<main>
<h1>FileEngine 启动失败 / FileEngine failed to start</h1>
<h2>错误 / Error</h2>
<pre>`)
	b.WriteString(esc(startupErr.Error()))
	b.WriteString(`</pre>
<h2>数据目录 / Data directory</h2>
<pre>`)
	b.WriteString(esc(paths.DataDir))
	b.WriteString(`</pre>
<h2>日志 / Log file</h2>
<pre>`)
	b.WriteString(esc(paths.LogFile))
	b.WriteString(`</pre>
<p>请检查 config.yaml 与数据库后重新启动。/ Check config.yaml and the database, then restart FileEngine.</p>
</main>
</body>
</html>`)
	return b.String()
}

// showStartupError replaces the main window with a standalone page
// explaining why the desktop could not start, so a failure is visible instead
// of the process exiting silently.
func showStartupError(app *application.App, paths desktopPaths, startupErr error, logger *slog.Logger) int {
	logger.Error("startup failed", "error", startupErr)
	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:  "FileEngine",
		HTML:   startupErrorHTML(paths, startupErr),
		Width:  720,
		Height: 520,
	})
	window.Show()
	if err := app.Run(); err != nil {
		logger.Error("show startup error window", "error", err)
	}
	return 1
}
