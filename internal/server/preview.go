package server

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	htmlstd "html"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"

	zenfiles "github.com/xZenLabs/zen-fm/internal/files"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
	htmlnode "golang.org/x/net/html"
)

const (
	maxTextPreviewBytes  = int64(4 << 20)
	maxImageSourceBytes  = int64(32 << 20)
	maxDocumentBytes     = int64(32 << 20)
	maxMediaPreviewBytes = int64(256 << 20)
	maxPreviewPixels     = int64(4_000_000)
	maxDecodedPixels     = int64(16_000_000)
	maxEPUBEntries       = 2_000
	maxEPUBTextBytes     = int64(4 << 20)
	maxEPUBInflatedBytes = uint64(16 << 20)
)

func (s *Server) previewFile(w http.ResponseWriter, r *http.Request) {
	if !s.acquireHeavy(w, r) {
		return
	}
	defer s.releaseHeavy()
	name := r.URL.Query().Get("path")
	extension := strings.ToLower(path.Ext(name))
	contentType := strings.SplitN(mime.TypeByExtension(extension), ";", 2)[0]
	switch {
	case isRasterExtension(extension):
		s.previewImage(w, r, name)
	case extension == ".epub":
		s.previewEPUB(w, r, name)
	case extension == ".pdf":
		s.previewStream(w, r, name, "application/pdf", maxDocumentBytes, true)
	case strings.HasPrefix(contentType, "audio/"), strings.HasPrefix(contentType, "video/"):
		s.previewStream(w, r, name, contentType, maxMediaPreviewBytes, false)
	case isTextPreview(extension, contentType):
		s.previewText(w, r, name, extension, contentType)
	default:
		problem(w, r, http.StatusUnsupportedMediaType, "Unsupported File", "this file type has no bounded safe preview")
	}
}

func (s *Server) previewImage(w http.ResponseWriter, r *http.Request, name string) {
	data, err := s.readPreviewFile(name, maxImageSourceBytes)
	if err != nil {
		mapError(w, r, err)
		return
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 || config.Width > 50_000 || config.Height > 50_000 || int64(config.Width) > maxDecodedPixels/int64(config.Height) {
		problem(w, r, http.StatusUnsupportedMediaType, "Unsupported Image", "image dimensions or encoding are unsafe")
		return
	}
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		problem(w, r, http.StatusUnsupportedMediaType, "Unsupported Image", "image could not be decoded safely")
		return
	}
	width, height := fitDimensions(config.Width, config.Height, boundedDimension(r.URL.Query().Get("width"), 1600), boundedDimension(r.URL.Query().Get("height"), 1200))
	thumbnail := nearestNeighbor(decoded, width, height)
	var output bytes.Buffer
	if err := png.Encode(&output, thumbnail); err != nil {
		internalError(w, r, err)
		return
	}
	if output.Len() > int(maxImageSourceBytes) {
		problem(w, r, http.StatusRequestEntityTooLarge, "Too Large", "rendered preview exceeds its output limit")
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Content-Disposition", "inline")
	_, _ = w.Write(output.Bytes())
}

func (s *Server) previewText(w http.ResponseWriter, r *http.Request, name, extension, contentType string) {
	data, err := s.readPreviewFile(name, maxTextPreviewBytes)
	if err != nil {
		mapError(w, r, err)
		return
	}
	if !utf8.Valid(data) {
		problem(w, r, http.StatusUnsupportedMediaType, "Unsupported Text", "text preview is not valid UTF-8")
		return
	}
	if extension == ".html" || extension == ".htm" || extension == ".xhtml" {
		data, err = sanitizeHTMLContext(r.Context(), data)
		if err != nil {
			problem(w, r, http.StatusUnsupportedMediaType, "Unsupported HTML", "HTML preview could not be sanitized")
			return
		}
		contentType = "text/html"
	} else if extension == ".csv" {
		contentType = "text/csv"
	} else if extension == ".md" || extension == ".markdown" {
		contentType = "text/markdown"
	} else if !strings.HasPrefix(contentType, "text/") {
		contentType = "text/plain"
	}
	w.Header().Set("Content-Type", contentType+"; charset=utf-8")
	w.Header().Set("Content-Disposition", "inline")
	_, _ = w.Write(data)
}

func (s *Server) previewStream(w http.ResponseWriter, r *http.Request, name, contentType string, maximum int64, sandbox bool) {
	file, info, err := s.cfg.Files.OpenRegular(name)
	if err != nil {
		mapError(w, r, err)
		return
	}
	defer file.Close()
	if info.Size() > maximum {
		problem(w, r, http.StatusRequestEntityTooLarge, "Too Large", "preview source exceeds its size limit")
		return
	}
	if contentType == "application/pdf" {
		prefix := make([]byte, 5)
		if _, err := io.ReadFull(file, prefix); err != nil || string(prefix) != "%PDF-" {
			problem(w, r, http.StatusUnsupportedMediaType, "Unsupported PDF", "file does not contain a PDF signature")
			return
		}
		_, _ = file.Seek(0, io.SeekStart)
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", "inline")
	if sandbox {
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; frame-ancestors 'self'")
	}
	http.ServeContent(&progressResponseWriter{ResponseWriter: w, timeout: progressTimeout, touch: s.touch}, r, info.Name(), info.ModTime(), file)
}

func (s *Server) previewEPUB(w http.ResponseWriter, r *http.Request, name string) {
	file, info, err := s.cfg.Files.OpenRegular(name)
	if err != nil {
		mapError(w, r, err)
		return
	}
	defer file.Close()
	if info.Size() > maxDocumentBytes {
		problem(w, r, http.StatusRequestEntityTooLarge, "Too Large", "EPUB exceeds its source limit")
		return
	}
	archive, err := zip.NewReader(&activityReaderAt{reader: file, touch: s.touch}, info.Size())
	if err != nil || len(archive.File) > maxEPUBEntries {
		problem(w, r, http.StatusUnsupportedMediaType, "Unsupported EPUB", "EPUB container is invalid or too complex")
		return
	}
	var output strings.Builder
	var inflated uint64
	for _, member := range archive.File {
		if output.Len() >= int(maxEPUBTextBytes) {
			break
		}
		if validateArchiveName(member.Name) != nil || member.FileInfo().IsDir() {
			continue
		}
		extension := strings.ToLower(path.Ext(member.Name))
		if extension != ".xhtml" && extension != ".html" && extension != ".htm" || member.UncompressedSize64 > uint64(maxEPUBTextBytes) {
			continue
		}
		if member.UncompressedSize64 > maxEPUBInflatedBytes-inflated {
			break
		}
		inflated += member.UncompressedSize64
		reader, err := member.Open()
		if err != nil {
			continue
		}
		text, extractErr := extractHTMLText(io.LimitReader(reader, maxEPUBTextBytes+1), int(maxEPUBTextBytes)-output.Len())
		_ = reader.Close()
		if extractErr == nil && text != "" {
			if output.Len() > 0 {
				output.WriteString("\n\n")
			}
			output.WriteString(text)
		}
	}
	if output.Len() == 0 {
		problem(w, r, http.StatusUnsupportedMediaType, "Unsupported EPUB", "EPUB contains no bounded readable text")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", "inline")
	_, _ = io.WriteString(w, output.String())
}

func (s *Server) readPreviewFile(name string, maximum int64) ([]byte, error) {
	file, info, err := s.cfg.Files.OpenRegular(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if info.Size() > maximum {
		return nil, zenfiles.ErrTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(&activityReader{reader: file, touch: s.touch}, maximum+1))
	if int64(len(data)) > maximum {
		return nil, zenfiles.ErrTooLarge
	}
	return data, err
}

func isRasterExtension(extension string) bool {
	switch extension {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".tif", ".tiff":
		return true
	}
	return false
}

func isTextPreview(extension, contentType string) bool {
	if strings.HasPrefix(contentType, "text/") || contentType == "application/json" || contentType == "application/xml" {
		return true
	}
	switch extension {
	case ".txt", ".md", ".markdown", ".csv", ".json", ".xml", ".yaml", ".yml", ".toml", ".ini", ".log", ".html", ".htm", ".xhtml", ".css", ".js", ".ts", ".tsx", ".jsx", ".go", ".lua", ".sh":
		return true
	}
	return false
}

func boundedDimension(value string, fallback int) int {
	dimension, err := strconv.Atoi(value)
	if err != nil || dimension < 16 || dimension > 4096 {
		return fallback
	}
	return dimension
}

func fitDimensions(sourceWidth, sourceHeight, maximumWidth, maximumHeight int) (int, int) {
	width, height := sourceWidth, sourceHeight
	if width > maximumWidth {
		height, width = max(1, height*maximumWidth/width), maximumWidth
	}
	if height > maximumHeight {
		width, height = max(1, width*maximumHeight/height), maximumHeight
	}
	for int64(width)*int64(height) > maxPreviewPixels {
		width, height = max(1, width*9/10), max(1, height*9/10)
	}
	return width, height
}

func nearestNeighbor(source image.Image, width, height int) *image.RGBA {
	bounds := source.Bounds()
	destination := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			destination.Set(x, y, color.RGBAModel.Convert(source.At(bounds.Min.X+x*bounds.Dx()/width, bounds.Min.Y+y*bounds.Dy()/height)))
		}
	}
	return destination
}

func sanitizeHTML(data []byte) ([]byte, error) {
	return sanitizeHTMLContext(context.Background(), data)
}

func sanitizeHTMLContext(ctx context.Context, data []byte) ([]byte, error) {
	const (
		maxNodes = 100_000
		maxDepth = 256
	)
	tokenizer := htmlnode.NewTokenizer(bytes.NewReader(data))
	var output strings.Builder
	type frame struct {
		tag     string
		allowed bool
	}
	stack := make([]frame, 0, 32)
	dropDepth, nodes := 0, 0
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		tokenType := tokenizer.Next()
		if tokenType == htmlnode.ErrorToken {
			if !errors.Is(tokenizer.Err(), io.EOF) {
				return nil, tokenizer.Err()
			}
			break
		}
		nodes++
		if nodes > maxNodes {
			return nil, zenfiles.ErrTooLarge
		}
		switch tokenType {
		case htmlnode.TextToken:
			if dropDepth == 0 {
				output.WriteString(htmlstd.EscapeString(string(tokenizer.Text())))
			}
		case htmlnode.StartTagToken, htmlnode.SelfClosingTagToken:
			token := tokenizer.Token()
			tag := strings.ToLower(token.Data)
			selfClosing := tokenType == htmlnode.SelfClosingTagToken || tag == "br" || tag == "hr"
			if !selfClosing && len(stack)+dropDepth >= maxDepth {
				return nil, zenfiles.ErrTooLarge
			}
			if dropDepth > 0 {
				if !selfClosing {
					dropDepth++
				}
				break
			}
			if dropHTMLSubtree[tag] {
				if !selfClosing {
					dropDepth = 1
				}
				break
			}
			allowed := allowedHTMLTags[tag]
			if allowed {
				output.WriteByte('<')
				output.WriteString(tag)
				if tag == "a" {
					for _, attribute := range token.Attr {
						if strings.EqualFold(attribute.Key, "href") && safeHref(attribute.Val) {
							output.WriteString(` href="` + htmlstd.EscapeString(attribute.Val) + `" rel="noreferrer noopener"`)
							break
						}
					}
				}
				output.WriteByte('>')
				if selfClosing && tag != "br" && tag != "hr" {
					output.WriteString("</" + tag + ">")
				}
			}
			if !selfClosing {
				stack = append(stack, frame{tag: tag, allowed: allowed})
			}
		case htmlnode.EndTagToken:
			if dropDepth > 0 {
				dropDepth--
				break
			}
			if len(stack) > 0 {
				last := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				if last.allowed {
					output.WriteString("</" + last.tag + ">")
				}
			}
		}
		if output.Len() > int(maxTextPreviewBytes) {
			return nil, zenfiles.ErrTooLarge
		}
	}
	for index := len(stack) - 1; index >= 0; index-- {
		if stack[index].allowed {
			output.WriteString("</" + stack[index].tag + ">")
		}
	}
	if output.Len() > int(maxTextPreviewBytes) {
		return nil, zenfiles.ErrTooLarge
	}
	return []byte(output.String()), nil
}

var allowedHTMLTags = map[string]bool{"p": true, "div": true, "span": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true, "ul": true, "ol": true, "li": true, "table": true, "thead": true, "tbody": true, "tr": true, "th": true, "td": true, "pre": true, "code": true, "blockquote": true, "em": true, "strong": true, "b": true, "i": true, "u": true, "br": true, "hr": true, "a": true}
var dropHTMLSubtree = map[string]bool{"script": true, "style": true, "iframe": true, "object": true, "embed": true, "form": true, "svg": true, "math": true, "img": true, "link": true, "meta": true, "base": true, "template": true}

func safeHref(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(lower, "#") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "mailto:")
}

func extractHTMLText(reader io.Reader, limit int) (string, error) {
	if limit <= 0 {
		return "", zenfiles.ErrTooLarge
	}
	tokenizer := htmlnode.NewTokenizer(reader)
	var output strings.Builder
	dropDepth := 0
	for {
		switch tokenizer.Next() {
		case htmlnode.ErrorToken:
			if errors.Is(tokenizer.Err(), io.EOF) {
				return strings.Join(strings.Fields(output.String()), " "), nil
			}
			return "", tokenizer.Err()
		case htmlnode.StartTagToken:
			name, _ := tokenizer.TagName()
			if dropHTMLSubtree[strings.ToLower(string(name))] {
				dropDepth++
			}
		case htmlnode.EndTagToken:
			name, _ := tokenizer.TagName()
			if dropHTMLSubtree[strings.ToLower(string(name))] && dropDepth > 0 {
				dropDepth--
			}
		case htmlnode.TextToken:
			if dropDepth == 0 {
				text := string(tokenizer.Text())
				if output.Len()+len(text)+1 > limit {
					return output.String(), nil
				}
				output.WriteByte(' ')
				output.WriteString(text)
			}
		}
	}
}
