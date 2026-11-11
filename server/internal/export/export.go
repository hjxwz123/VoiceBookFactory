// Package export writes deliverable side files: subtitles (SRT/LRC/JSON), Word scripts (DOCX),
// spreadsheets of lines (XLSX), ffmpeg chapter metadata and ZIP packages.
package export

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/xuri/excelize/v2"
)

type Cue struct {
	StartMs int64
	EndMs   int64
	Speaker string
	Text    string
}

func ts(ms int64, sep string) string {
	h := ms / 3600000
	m := ms % 3600000 / 60000
	s := ms % 60000 / 1000
	return fmt.Sprintf("%02d:%02d:%02d%s%03d", h, m, s, sep, ms%1000)
}

func SRT(cues []Cue, withSpeaker bool) string {
	var sb strings.Builder
	for i, c := range cues {
		txt := c.Text
		if withSpeaker && c.Speaker != "" {
			txt = c.Speaker + "：" + txt
		}
		fmt.Fprintf(&sb, "%d\n%s --> %s\n%s\n\n", i+1, ts(c.StartMs, ","), ts(c.EndMs, ","), txt)
	}
	return sb.String()
}

func LRC(title, artist string, cues []Cue) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "[ti:%s]\n[ar:%s]\n", title, artist)
	for _, c := range cues {
		fmt.Fprintf(&sb, "[%02d:%02d.%02d]%s\n", c.StartMs/60000, c.StartMs%60000/1000, c.StartMs%1000/10, c.Text)
	}
	return sb.String()
}

// FFMetadata writes an ffmetadata file with chapter markers (used for M4B).
func FFMetadata(title, artist string, chapters []Cue) string {
	esc := func(s string) string {
		r := strings.NewReplacer("=", "\\=", ";", "\\;", "#", "\\#", "\\", "\\\\", "\n", " ")
		return r.Replace(s)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, ";FFMETADATA1\ntitle=%s\nartist=%s\nalbum=%s\ngenre=Audiobook\n", esc(title), esc(artist), esc(title))
	for _, c := range chapters {
		fmt.Fprintf(&sb, "\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=%d\nEND=%d\ntitle=%s\n", c.StartMs, c.EndMs, esc(c.Text))
	}
	return sb.String()
}

// ---------- DOCX ----------

type DocLine struct {
	Speaker string
	Color   string // hex without '#'
	Text    string
	Note    string
	Heading bool
}

func xmlEsc(s string) string {
	var sb strings.Builder
	xml.EscapeText(&sb, []byte(s))
	return sb.String()
}

func run(text, color string, bold bool, size int) string {
	props := ""
	if bold {
		props += "<w:b/>"
	}
	if color != "" {
		props += `<w:color w:val="` + strings.TrimPrefix(color, "#") + `"/>`
	}
	if size > 0 {
		props += fmt.Sprintf(`<w:sz w:val="%d"/><w:szCs w:val="%d"/>`, size, size)
	}
	return `<w:r><w:rPr><w:rFonts w:ascii="Microsoft YaHei" w:eastAsia="Microsoft YaHei" w:hAnsi="Microsoft YaHei"/>` + props + `</w:rPr><w:t xml:space="preserve">` + xmlEsc(text) + `</w:t></w:r>`
}

// WriteDocx writes a minimal, valid Word document.
func WriteDocx(path, title string, lines []DocLine) error {
	var body strings.Builder
	body.WriteString(`<w:p><w:pPr><w:spacing w:after="240"/></w:pPr>` + run(title, "111827", true, 36) + `</w:p>`)
	for _, l := range lines {
		if l.Heading {
			body.WriteString(`<w:p><w:pPr><w:spacing w:before="360" w:after="160"/></w:pPr>` + run(l.Text, "111827", true, 28) + `</w:p>`)
			continue
		}
		body.WriteString(`<w:p><w:pPr><w:spacing w:after="120"/></w:pPr>`)
		if l.Speaker != "" {
			body.WriteString(run("【"+l.Speaker+"】", l.Color, true, 21))
		}
		body.WriteString(run(l.Text, "", false, 21))
		if l.Note != "" {
			body.WriteString(run("  （"+l.Note+"）", "6B7280", false, 18))
		}
		body.WriteString(`</w:p>`)
	}
	doc := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` + body.String() +
		`<w:sectPr><w:pgSz w:w="11906" w:h="16838"/><w:pgMar w:top="1440" w:right="1440" w:bottom="1440" w:left="1440" w:header="851" w:footer="992" w:gutter="0"/></w:sectPr></w:body></w:document>`
	files := map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`,
		"_rels/.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`,
		"word/document.xml": doc,
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for _, name := range []string{"[Content_Types].xml", "_rels/.rels", "word/document.xml"} {
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		io.WriteString(w, files[name])
	}
	return zw.Close()
}

// ---------- XLSX ----------

type Sheet struct {
	Name   string
	Header []string
	Rows   [][]any
	Widths []float64
}

func WriteXLSX(path string, sheets []Sheet) error {
	f := excelize.NewFile()
	defer f.Close()
	headerStyle, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true, Color: "FFFFFF"}, Fill: excelize.Fill{Type: "pattern", Color: []string{"4F46E5"}, Pattern: 1}, Alignment: &excelize.Alignment{Vertical: "center"}})
	wrap, _ := f.NewStyle(&excelize.Style{Alignment: &excelize.Alignment{WrapText: true, Vertical: "top"}})
	for i, sh := range sheets {
		name := sanitizeSheet(sh.Name, i)
		if i == 0 {
			f.SetSheetName("Sheet1", name)
		} else {
			f.NewSheet(name)
		}
		for c, h := range sh.Header {
			cell, _ := excelize.CoordinatesToCellName(c+1, 1)
			f.SetCellValue(name, cell, h)
		}
		if len(sh.Header) > 0 {
			end, _ := excelize.CoordinatesToCellName(len(sh.Header), 1)
			f.SetCellStyle(name, "A1", end, headerStyle)
			f.SetRowHeight(name, 1, 22)
		}
		for r, row := range sh.Rows {
			for c, v := range row {
				cell, _ := excelize.CoordinatesToCellName(c+1, r+2)
				f.SetCellValue(name, cell, v)
			}
		}
		for c, w := range sh.Widths {
			col, _ := excelize.ColumnNumberToName(c + 1)
			f.SetColWidth(name, col, col, w)
			if w >= 30 && len(sh.Rows) > 0 {
				f.SetCellStyle(name, col+"2", fmt.Sprintf("%s%d", col, len(sh.Rows)+1), wrap)
			}
		}
		f.SetPanes(name, &excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"})
	}
	return f.SaveAs(path)
}

func sanitizeSheet(name string, i int) string {
	r := strings.NewReplacer(":", " ", "\\", " ", "/", " ", "?", " ", "*", " ", "[", "(", "]", ")")
	name = strings.TrimSpace(r.Replace(name))
	rs := []rune(name)
	if len(rs) > 28 {
		name = string(rs[:28])
	}
	if name == "" {
		name = fmt.Sprintf("Sheet%d", i+1)
	}
	return name
}

// ---------- ZIP ----------

type ZipEntry struct {
	Name string
	Path string // file on disk
	Data []byte // or inline content
}

func WriteZip(path string, entries []ZipEntry) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	seen := map[string]int{}
	for _, e := range entries {
		name := filepath.ToSlash(e.Name)
		if n := seen[name]; n > 0 {
			ext := filepath.Ext(name)
			name = fmt.Sprintf("%s (%d)%s", strings.TrimSuffix(name, ext), n+1, ext)
		}
		seen[filepath.ToSlash(e.Name)]++
		hdr := &zip.FileHeader{Name: name, Method: zip.Deflate}
		hdr.Flags |= 0x800 // UTF-8 names
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		if e.Path != "" {
			src, err := os.Open(e.Path)
			if err != nil {
				return err
			}
			_, err = io.Copy(w, src)
			src.Close()
			if err != nil {
				return err
			}
		} else {
			w.Write(e.Data)
		}
	}
	return zw.Close()
}

// SafeName removes characters not allowed in file names.
func SafeName(s string) string {
	r := strings.NewReplacer("/", "_", "\\", "_", ":", "：", "*", "_", "?", "？", "\"", "'", "<", "《", ">", "》", "|", "_", "\n", " ")
	s = strings.TrimSpace(r.Replace(s))
	if s == "" {
		return "untitled"
	}
	rs := []rune(s)
	if len(rs) > 80 {
		s = string(rs[:80])
	}
	return s
}

// ---------- OpenTimelineIO ----------

type OTIOClip struct {
	Name     string
	StartMs  int64 // position on the timeline
	OffsetMs int64 // trim into the media
	DurMs    int64
	Media    string // relative path inside the package
	MediaMs  int64  // full media length
}

type OTIOTrack struct {
	Name  string
	Clips []OTIOClip
}

func rt(ms int64) map[string]any {
	return map[string]any{"OTIO_SCHEMA": "RationalTime.1", "rate": 1000.0, "value": float64(ms)}
}

func tr(start, dur int64) map[string]any {
	return map[string]any{"OTIO_SCHEMA": "TimeRange.1", "start_time": rt(start), "duration": rt(dur)}
}

// OTIO builds an OpenTimelineIO (.otio) document importable by DaVinci Resolve, Premiere (via plugins) and others.
func OTIO(name string, tracks []OTIOTrack) map[string]any {
	var children []any
	for _, t := range tracks {
		var items []any
		cursor := int64(0)
		for _, c := range t.Clips {
			if c.StartMs > cursor {
				items = append(items, map[string]any{"OTIO_SCHEMA": "Gap.1", "name": "", "source_range": tr(0, c.StartMs-cursor), "effects": []any{}, "markers": []any{}, "metadata": map[string]any{}})
			} else if c.StartMs < cursor {
				// overlapping clips are moved after the previous one inside a single track
				c.StartMs = cursor
			}
			items = append(items, map[string]any{
				"OTIO_SCHEMA": "Clip.2", "name": c.Name, "source_range": tr(c.OffsetMs, c.DurMs), "effects": []any{}, "markers": []any{}, "metadata": map[string]any{},
				"active_media_reference_key": "DEFAULT_MEDIA",
				"media_references":           map[string]any{"DEFAULT_MEDIA": map[string]any{"OTIO_SCHEMA": "ExternalReference.1", "name": c.Media, "target_url": c.Media, "available_range": tr(0, max(c.MediaMs, c.OffsetMs+c.DurMs)), "metadata": map[string]any{}}},
			})
			cursor = c.StartMs + c.DurMs
		}
		children = append(children, map[string]any{"OTIO_SCHEMA": "Track.1", "name": t.Name, "kind": "Audio", "children": items, "effects": []any{}, "markers": []any{}, "metadata": map[string]any{}, "source_range": nil})
	}
	return map[string]any{
		"OTIO_SCHEMA": "Timeline.1", "name": name, "global_start_time": rt(0), "metadata": map[string]any{"generator": "voicebook"},
		"tracks": map[string]any{"OTIO_SCHEMA": "Stack.1", "name": "tracks", "children": children, "effects": []any{}, "markers": []any{}, "metadata": map[string]any{}, "source_range": nil},
	}
}
