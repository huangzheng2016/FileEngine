package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"path/filepath"
	"strings"

	"github.com/cloudwego/eino/schema"
)

const (
	defaultVisionMaxImagesPerBatch = 4
	defaultVisionImageMaxPx        = 512
	defaultVisionMaxImageBytes     = 20 * 1024 * 1024
)

// estimatedTokensPerImagePart is the flat token estimate for one image part in
// message history. Thumbnails are capped at VisionImageMaxPx long edge, so the
// real cost is bounded: Claude ≈ w*h/750 (512px square → ~350), OpenAI high
// detail ≈ 85 + 170*tiles (512px → ~255). 400 covers the worst case.
const estimatedTokensPerImagePart = 400

// maxImagePixels caps decoded dimensions. image.Decode allocates by header
// size, so a small compressed file can still be a decompression bomb
// (a 50K×50K solid PNG is KBs on disk but ~10GB decoded). 50MP ≈ 200MB RGBA
// worst case, transient.
const maxImagePixels = 50_000_000

type ViewImageInput struct {
	Paths []string `json:"paths" jsonschema_description:"Image file paths to view (original_path from list_files). Supported: jpg/jpeg/png/gif. HEIC/RAW are skipped."`
}

func (tb *ToolBuilder) visionLimits() (maxImages, maxPx, maxBytes int) {
	maxImages = tb.cfg.VisionMaxImagesPerBatch
	if maxImages <= 0 {
		maxImages = defaultVisionMaxImagesPerBatch
	}
	maxPx = tb.cfg.VisionImageMaxPx
	if maxPx <= 0 {
		maxPx = defaultVisionImageMaxPx
	}
	maxBytes = tb.cfg.VisionMaxImageBytes
	if maxBytes <= 0 {
		maxBytes = defaultVisionMaxImageBytes
	}
	return
}

// Per-file problems become text notes so one bad file never fails the whole
// call.
func (tb *ToolBuilder) viewImage(ctx context.Context, input *ViewImageInput) (*schema.ToolResult, error) {
	maxImages, maxPx, maxBytes := tb.visionLimits()
	if len(input.Paths) == 0 {
		return nil, fmt.Errorf("no paths provided")
	}
	if len(input.Paths) > maxImages {
		return nil, fmt.Errorf("too many images: %d requested, max %d per view_image call", len(input.Paths), maxImages)
	}

	result := &schema.ToolResult{}

	for _, p := range input.Paths {
		path := normalizePath(p)
		if tb.isSensitivePath(path) {
			result.Parts = append(result.Parts, schema.ToolOutputPart{Type: schema.ToolPartTypeText, Text: SanitizeUntrustedName(path) + ": " + sensitiveNotice})
			continue
		}
		format := imageFormatFor(path)
		if format == "" {
			note := fmt.Sprintf("%s: skipped (unsupported format; supported: jpg/jpeg/png/gif)", path)
			result.Parts = append(result.Parts, schema.ToolOutputPart{Type: schema.ToolPartTypeText, Text: note})
			continue
		}

		info, err := tb.fs.Stat(ctx, path)
		if err != nil {
			note := fmt.Sprintf("%s: skipped (stat failed: %v)", path, err)
			result.Parts = append(result.Parts, schema.ToolOutputPart{Type: schema.ToolPartTypeText, Text: note})
			continue
		}
		if info.IsDir {
			note := fmt.Sprintf("%s: skipped (is a directory)", path)
			result.Parts = append(result.Parts, schema.ToolOutputPart{Type: schema.ToolPartTypeText, Text: note})
			continue
		}
		if info.Size > int64(maxBytes) {
			note := fmt.Sprintf("%s: skipped (too large: %d bytes, limit %d)", path, info.Size, maxBytes)
			result.Parts = append(result.Parts, schema.ToolOutputPart{Type: schema.ToolPartTypeText, Text: note})
			continue
		}

		data, mime, origW, origH, thumbW, thumbH, err := tb.loadThumbnail(ctx, path, format, maxPx, maxBytes)
		if err != nil {
			note := fmt.Sprintf("%s: skipped (%v)", path, err)
			result.Parts = append(result.Parts, schema.ToolOutputPart{Type: schema.ToolPartTypeText, Text: note})
			continue
		}

		b64 := base64.StdEncoding.EncodeToString(data)
		result.Parts = append(result.Parts,
			schema.ToolOutputPart{Type: schema.ToolPartTypeText,
				Text: fmt.Sprintf("%s (%dx%d -> %dx%d, %s):", path, origW, origH, thumbW, thumbH, mime)},
			schema.ToolOutputPart{Type: schema.ToolPartTypeImage,
				Image: &schema.ToolOutputImage{MessagePartCommon: schema.MessagePartCommon{
					Base64Data: &b64,
					MIMEType:   mime,
				}}},
		)
	}

	return result, nil
}

// HEIC/RAW are unsupported: no pure-Go decoder.
func imageFormatFor(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg":
		return "jpeg"
	case ".png":
		return "png"
	case ".gif":
		return "gif"
	}
	return ""
}

func (tb *ToolBuilder) loadThumbnail(ctx context.Context, path, format string, maxPx, maxBytes int) (data []byte, mime string, origW, origH, thumbW, thumbH int, err error) {
	reader, err := tb.fs.ReadFile(ctx, path)
	if err != nil {
		return nil, "", 0, 0, 0, 0, fmt.Errorf("read failed: %w", err)
	}
	defer reader.Close()

	// Second size defense in case Stat under-reported.
	raw, err := io.ReadAll(io.LimitReader(reader, int64(maxBytes)+1))
	if err != nil {
		return nil, "", 0, 0, 0, 0, fmt.Errorf("read failed: %w", err)
	}
	if len(raw) > maxBytes {
		return nil, "", 0, 0, 0, 0, fmt.Errorf("too large: over %d bytes", maxBytes)
	}

	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return nil, "", 0, 0, 0, 0, fmt.Errorf("decode failed: %w", err)
	}
	// Decompression-bomb guard: check header dimensions before Decode
	// allocates by them (also keeps the area-average accumulator safe).
	if pixels := int64(cfg.Width) * int64(cfg.Height); cfg.Width <= 0 || cfg.Height <= 0 || pixels > maxImagePixels {
		return nil, "", 0, 0, 0, 0, fmt.Errorf("dimensions too large: %dx%d (limit %d pixels)", cfg.Width, cfg.Height, maxImagePixels)
	}

	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, "", 0, 0, 0, 0, fmt.Errorf("decode failed: %w", err)
	}
	origW, origH = cfg.Width, cfg.Height

	thumb := downscaleToFit(img, maxPx)
	tbnd := thumb.Bounds()
	thumbW, thumbH = tbnd.Dx(), tbnd.Dy()

	var buf bytes.Buffer
	switch format {
	case "jpeg":
		mime = "image/jpeg"
		err = jpeg.Encode(&buf, thumb, &jpeg.Options{Quality: 85})
	default: // png, gif (first frame) -> png
		mime = "image/png"
		err = png.Encode(&buf, thumb)
	}
	if err != nil {
		return nil, "", 0, 0, 0, 0, fmt.Errorf("encode failed: %w", err)
	}
	return buf.Bytes(), mime, origW, origH, thumbW, thumbH, nil
}

// Area averaging keeps text screenshots legible compared to nearest-neighbor
// and needs only stdlib.
func downscaleToFit(img image.Image, maxPx int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= maxPx && h <= maxPx {
		return img
	}
	scale := float64(maxPx) / float64(w)
	if h > w {
		scale = float64(maxPx) / float64(h)
	}
	nw := int(float64(w)*scale + 0.5)
	nh := int(float64(h)*scale + 0.5)
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}

	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < nh; y++ {
		y0 := int(float64(y) / scale)
		y1 := int(float64(y+1) / scale)
		if y1 <= y0 {
			y1 = y0 + 1
		}
		if y1 > h {
			y1 = h
		}
		for x := 0; x < nw; x++ {
			x0 := int(float64(x) / scale)
			x1 := int(float64(x+1) / scale)
			if x1 <= x0 {
				x1 = x0 + 1
			}
			if x1 > w {
				x1 = w
			}
			var r, g, bl, a uint32
			var n uint32
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					cr, cg, cb, ca := img.At(b.Min.X+sx, b.Min.Y+sy).RGBA()
					r += cr
					g += cg
					bl += cb
					a += ca
					n++
				}
			}
			i := dst.PixOffset(x, y)
			dst.Pix[i+0] = uint8((r / n) >> 8)
			dst.Pix[i+1] = uint8((g / n) >> 8)
			dst.Pix[i+2] = uint8((bl / n) >> 8)
			dst.Pix[i+3] = uint8((a / n) >> 8)
		}
	}
	return dst
}

// Raw base64 payloads must never reach tool-result events or logs.
func describeMessageParts(parts []schema.MessageInputPart) string {
	var sb strings.Builder
	images := 0
	for _, p := range parts {
		switch p.Type {
		case schema.ChatMessagePartTypeText:
			sb.WriteString(p.Text)
			sb.WriteString("\n")
		case schema.ChatMessagePartTypeImageURL:
			images++
		}
	}
	if images > 0 {
		fmt.Fprintf(&sb, "[%d image(s) attached]", images)
	}
	return strings.TrimSpace(sb.String())
}
