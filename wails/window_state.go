package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

const (
	defaultWindowWidth  = 1400
	defaultWindowHeight = 900
	minWindowWidth      = 1200
	minWindowHeight     = 720
	maxWindowWidth      = 7680
	maxWindowHeight     = 4320
)

type windowState struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

func validWindowState(s windowState) bool {
	return s.Width >= minWindowWidth && s.Width <= maxWindowWidth &&
		s.Height >= minWindowHeight && s.Height <= maxWindowHeight
}

// loadWindowState returns the persisted window size, falling back to the
// defaults when the file is missing, unreadable, or out of range.
func loadWindowState() windowState {
	state := windowState{Width: defaultWindowWidth, Height: defaultWindowHeight}
	dir, err := ensureDataDir()
	if err != nil {
		return state
	}
	data, err := os.ReadFile(filepath.Join(dir, "gui_state.json"))
	if err != nil {
		return state
	}
	var saved windowState
	if err := json.Unmarshal(data, &saved); err != nil || !validWindowState(saved) {
		return state
	}
	return saved
}

// saveWindowState persists the current window size; called on shutdown.
// Out-of-range sizes (e.g. zero values while minimized/closing) are ignored.
func (a *DesktopApp) saveWindowState(ctx context.Context) {
	w, h := wailsRuntime.WindowGetSize(ctx)
	state := windowState{Width: w, Height: h}
	if !validWindowState(state) {
		return
	}
	dir, err := ensureDataDir()
	if err != nil {
		return
	}
	data, err := json.Marshal(state)
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, "gui_state.json"), data, 0o644)
}
