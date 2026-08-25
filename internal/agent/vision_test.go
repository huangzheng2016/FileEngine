package agent

import (
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"FileEngine/internal/config"
	"FileEngine/internal/db"
	"FileEngine/internal/remotefs"
)

// writeGradientJPEG writes a w×h non-uniform JPEG into dir.
func writeGradientJPEG(t *testing.T, dir, name string, w, h int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x % 256), uint8(y % 256), 128, 255})
		}
	}
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	defer f.Close()
	if err := jpeg.Encode(f, img, nil); err != nil {
		t.Fatalf("encode %s: %v", name, err)
	}
}

// writeSolidPNG writes a w×h solid PNG into dir.
func writeSolidPNG(t *testing.T, dir, name string, w, h int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{200, 30, 30, 255})
		}
	}
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatalf("encode %s: %v", name, err)
	}
}

func toolNames(t *testing.T, tools []tool.BaseTool) []string {
	t.Helper()
	names := make([]string, 0, len(tools))
	for _, bt := range tools {
		info, err := bt.Info(context.Background())
		if err != nil {
			t.Fatalf("tool info: %v", err)
		}
		names = append(names, info.Name)
	}
	return names
}

func hasTool(names []string, name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}

// The two vision switches gate view_image: model-level (ModelProvider.
// VisionEnabled, passed into NewToolBuilder) ∩ session-level (AllowVision).
// Both default off, so the tool is absent unless explicitly enabled twice.
func TestBuildToolsVisionGating(t *testing.T) {
	cases := []struct {
		name        string
		modelVision bool
		allowVision bool
		wantTool    bool
	}{
		{"both off (default)", false, false, false},
		{"session on, model off", false, true, false},
		{"model on, session off", true, false, false},
		{"both on", true, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			session := &db.ScanSession{AllowVision: tc.allowVision}
			tb := NewToolBuilder(nil, nil, 1, 1, NewLogger(nil, 1), config.AgentConfig{}, session, tc.modelVision)

			tools, err := tb.BuildTools()
			if err != nil {
				t.Fatalf("BuildTools: %v", err)
			}
			if got := hasTool(toolNames(t, tools), "view_image"); got != tc.wantTool {
				t.Fatalf("BuildTools view_image present=%v, want %v", got, tc.wantTool)
			}

			instructTools, err := tb.BuildInstructTools()
			if err != nil {
				t.Fatalf("BuildInstructTools: %v", err)
			}
			if got := hasTool(toolNames(t, instructTools), "view_image"); got != tc.wantTool {
				t.Fatalf("BuildInstructTools view_image present=%v, want %v", got, tc.wantTool)
			}
		})
	}
}

// With vision off (the default), the autotag tool set must be exactly what it
// was before the feature existed: 8 always-on tools plus the session-gated
// read_file / create_category.
func TestBuildToolsDefaultOffRegression(t *testing.T) {
	session := &db.ScanSession{AllowReadFile: true, AllowAutoCategory: true}
	tb := NewToolBuilder(nil, nil, 1, 1, NewLogger(nil, 1), config.AgentConfig{}, session, false)
	tools, err := tb.BuildTools()
	if err != nil {
		t.Fatalf("BuildTools: %v", err)
	}
	names := toolNames(t, tools)
	want := []string{"list_files", "get_file_info", "update_description", "mark_tagged",
		"list_categories", "set_target", "update_category", "delete_category",
		"read_file", "create_category"}
	if len(names) != len(want) {
		t.Fatalf("tool count: got %d %v, want %d %v", len(names), names, len(want), want)
	}
	for _, w := range want {
		if !hasTool(names, w) {
			t.Fatalf("missing tool %q in %v", w, names)
		}
	}
	if hasTool(names, "view_image") {
		t.Fatalf("view_image must not be registered by default: %v", names)
	}
}

// imageParts extracts the image parts of a ToolResult.
func imageParts(res *schema.ToolResult) []schema.ToolOutputPart {
	var out []schema.ToolOutputPart
	for _, p := range res.Parts {
		if p.Type == schema.ToolPartTypeImage {
			out = append(out, p)
		}
	}
	return out
}

func textParts(res *schema.ToolResult) string {
	var sb strings.Builder
	for _, p := range res.Parts {
		if p.Type == schema.ToolPartTypeText {
			sb.WriteString(p.Text)
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

func newVisionToolBuilder(t *testing.T, dir string, cfg config.AgentConfig) *ToolBuilder {
	t.Helper()
	fs, err := remotefs.NewLocalFS(dir)
	if err != nil {
		t.Fatalf("NewLocalFS: %v", err)
	}
	return NewToolBuilder(nil, fs, 1, 1, NewLogger(nil, 1), cfg, &db.ScanSession{AllowVision: true}, true)
}

func TestViewImageThumbnails(t *testing.T) {
	dir := t.TempDir()
	writeGradientJPEG(t, dir, "photo.jpg", 1200, 800)
	writeSolidPNG(t, dir, "icon.png", 100, 50)

	tb := newVisionToolBuilder(t, dir, config.AgentConfig{VisionImageMaxPx: 256})
	res, err := tb.viewImage(context.Background(), &ViewImageInput{Paths: []string{"photo.jpg", "icon.png"}})
	if err != nil {
		t.Fatalf("viewImage: %v", err)
	}

	imgs := imageParts(res)
	if len(imgs) != 2 {
		t.Fatalf("image parts: got %d, want 2 (result: %s)", len(imgs), textParts(res))
	}

	// photo.jpg: 1200x800 -> long edge capped at 256, stays jpeg.
	if imgs[0].Image == nil || imgs[0].Image.Base64Data == nil {
		t.Fatalf("image part 0 missing base64: %+v", imgs[0])
	}
	if imgs[0].Image.MIMEType != "image/jpeg" {
		t.Fatalf("image part 0 mime: %q, want image/jpeg", imgs[0].Image.MIMEType)
	}
	raw, err := base64.StdEncoding.DecodeString(*imgs[0].Image.Base64Data)
	if err != nil {
		t.Fatalf("decode base64: %v", err)
	}
	decCfg, format, err := image.DecodeConfig(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("decode thumbnail: %v", err)
	}
	if format != "jpeg" {
		t.Fatalf("thumbnail format: %q, want jpeg", format)
	}
	if decCfg.Width != 256 || decCfg.Height != 171 {
		t.Fatalf("thumbnail dims: %dx%d, want 256x171", decCfg.Width, decCfg.Height)
	}

	// icon.png: already small -> unchanged dims, stays png.
	if imgs[1].Image.MIMEType != "image/png" {
		t.Fatalf("image part 1 mime: %q, want image/png", imgs[1].Image.MIMEType)
	}
	raw2, _ := base64.StdEncoding.DecodeString(*imgs[1].Image.Base64Data)
	decCfg2, _, err := image.DecodeConfig(strings.NewReader(string(raw2)))
	if err != nil {
		t.Fatalf("decode png thumbnail: %v", err)
	}
	if decCfg2.Width != 100 || decCfg2.Height != 50 {
		t.Fatalf("png dims: %dx%d, want 100x50 (must not upscale)", decCfg2.Width, decCfg2.Height)
	}

	// Text notes carry per-file dimensions, never base64.
	notes := textParts(res)
	if !strings.Contains(notes, "photo.jpg (1200x800 -> 256x171") {
		t.Fatalf("missing dimension note for photo.jpg: %q", notes)
	}
	if strings.Contains(notes, *imgs[0].Image.Base64Data) {
		t.Fatal("text notes must not contain base64 payloads")
	}
}

func TestViewImageSkipsUnsupportedAndMissing(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "phone.heic"), []byte("fake heic"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	tb := newVisionToolBuilder(t, dir, config.AgentConfig{})
	res, err := tb.viewImage(context.Background(), &ViewImageInput{
		Paths: []string{"phone.heic", "notes.txt", "gone.jpg"},
	})
	if err != nil {
		t.Fatalf("viewImage: %v", err)
	}
	if len(imageParts(res)) != 0 {
		t.Fatalf("expected no image parts, got %d", len(imageParts(res)))
	}
	notes := textParts(res)
	for _, want := range []string{"phone.heic", "notes.txt", "gone.jpg", "skipped"} {
		if !strings.Contains(notes, want) {
			t.Fatalf("notes missing %q: %q", want, notes)
		}
	}
}

func TestViewImageGuardrails(t *testing.T) {
	dir := t.TempDir()
	writeGradientJPEG(t, dir, "a.jpg", 64, 64)
	writeGradientJPEG(t, dir, "b.jpg", 64, 64)
	writeGradientJPEG(t, dir, "c.jpg", 64, 64)

	// Per-call image cap.
	tb := newVisionToolBuilder(t, dir, config.AgentConfig{VisionMaxImagesPerBatch: 2})
	_, err := tb.viewImage(context.Background(), &ViewImageInput{Paths: []string{"a.jpg", "b.jpg", "c.jpg"}})
	if err == nil || !strings.Contains(err.Error(), "too many") {
		t.Fatalf("batch cap: got err=%v, want 'too many' error", err)
	}
	if _, err := tb.viewImage(context.Background(), &ViewImageInput{Paths: nil}); err == nil {
		t.Fatal("empty paths: expected error")
	}

	// Byte cap: reject source files larger than the limit.
	bigInfo, err := os.Stat(filepath.Join(dir, "a.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	tb = newVisionToolBuilder(t, dir, config.AgentConfig{VisionMaxImageBytes: int(bigInfo.Size()) - 1})
	res, err := tb.viewImage(context.Background(), &ViewImageInput{Paths: []string{"a.jpg"}})
	if err != nil {
		t.Fatalf("viewImage: %v", err)
	}
	if len(imageParts(res)) != 0 {
		t.Fatalf("oversized file must be skipped, got %d image parts", len(imageParts(res)))
	}
	if notes := textParts(res); !strings.Contains(notes, "too large") {
		t.Fatalf("expected 'too large' note, got %q", notes)
	}
}

// countTokens must account for multimodal tool results: image parts get a flat
// per-image estimate (dimension-based billing), text parts are estimated as text.
func TestCountTokensMultimodalParts(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString([]byte("fake-image-bytes"))
	withParts := []adk.Message{
		{Role: schema.Tool, UserInputMultiContent: []schema.MessageInputPart{
			{Type: schema.ChatMessagePartTypeText, Text: "notes about the file"},
			{Type: schema.ChatMessagePartTypeImageURL, Image: &schema.MessageInputImage{
				MessagePartCommon: schema.MessagePartCommon{Base64Data: &b64, MIMEType: "image/jpeg"},
			}},
		}},
	}
	got := countTokens(withParts, nil)
	if got < int64(estimatedTokensPerImagePart) {
		t.Fatalf("countTokens with image part: got %d, want >= %d", got, estimatedTokensPerImagePart)
	}

	// Sanity: a plain text message of the same text scores lower than one
	// carrying an additional image part.
	plain := []adk.Message{{Role: schema.Tool, Content: "notes about the file"}}
	if countTokens(plain, nil) >= got {
		t.Fatalf("image part not reflected in estimate: plain=%d parts=%d", countTokens(plain, nil), got)
	}
}

// Enhanced tool results (multimodal) must surface in the M9 event stream as a
// text stand-in — never as raw base64.
func TestRunADKOnceEnhancedToolResultPlaceholder(t *testing.T) {
	ctx := context.Background()

	echoTool, err := utils.InferEnhancedTool("echo", "echo back text plus an image", func(_ context.Context, in *echoInput) (*schema.ToolResult, error) {
		b64 := base64.StdEncoding.EncodeToString([]byte("img"))
		return &schema.ToolResult{Parts: []schema.ToolOutputPart{
			{Type: schema.ToolPartTypeText, Text: "echo: " + in.Text},
			{Type: schema.ToolPartTypeImage, Image: &schema.ToolOutputImage{
				MessagePartCommon: schema.MessagePartCommon{Base64Data: &b64, MIMEType: "image/png"},
			}},
		}}, nil
	})
	if err != nil {
		t.Fatalf("build enhanced echo tool: %v", err)
	}

	adkAgent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:  "test-agent",
		Model: &scriptedModel{},
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{Tools: []tool.BaseTool{echoTool}},
		},
		MaxIterations: 3,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}

	logger := NewLogger(nil, 201)
	logger.SetBatch(7)

	ch := db.SubscribeEvents(201)
	defer db.UnsubscribeEvents(201, ch)

	if _, err := runADKOnce(ctx, adkAgent, []*schema.Message{{Role: schema.User, Content: "go"}}, logger); err != nil {
		t.Fatalf("runADKOnce: %v", err)
	}

	// 1st event: tool_call; 2nd: tool_result with the placeholder text.
	recvBusEvent(t, ch)
	ev := recvBusEvent(t, ch)
	p, ok := ev.Payload.(db.ToolResultPayload)
	if ev.Type != db.EventToolResult || !ok {
		t.Fatalf("event: type=%q payload=%+v", ev.Type, ev.Payload)
	}
	if !strings.Contains(p.Output, "echo: hi") || !strings.Contains(p.Output, "[1 image(s) attached]") {
		t.Fatalf("placeholder output: %q", p.Output)
	}
	if strings.Contains(p.Output, "aW1n") {
		t.Fatalf("base64 payload leaked into event stream: %q", p.Output)
	}
}
