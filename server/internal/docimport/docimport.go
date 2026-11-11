// Package docimport extracts plain text (and structural chapter hints) from txt, md, docx, epub and pdf.
package docimport

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

type Section struct {
	Title string
	Text  string
}

type Document struct {
	Title    string
	Author   string
	Text     string    // full text
	Sections []Section // structural chapters when the format provides them (epub spine, docx headings)
	Cover    []byte    // epub cover image
	CoverExt string
}

func Parse(name string, data []byte) (*Document, error) {
	ext := strings.ToLower(path.Ext(name))
	switch ext {
	case ".txt", ".md", ".markdown", "":
		s, err := DecodeText(data)
		if err != nil {
			return nil, err
		}
		if ext == ".md" || ext == ".markdown" {
			s = stripMarkdown(s)
		}
		return &Document{Text: s}, nil
	case ".docx":
		return parseDocx(data)
	case ".epub":
		return parseEpub(data)
	case ".pdf":
		return parsePDF(data)
	}
	return nil, fmt.Errorf("不支持的文件格式 %s，请上传 txt、docx、epub、pdf 或 md", ext)
}

// DecodeText detects UTF-8 / UTF-16 / GB18030 / Big5 encodings.
func DecodeText(b []byte) (string, error) {
	switch {
	case bytes.HasPrefix(b, []byte{0xEF, 0xBB, 0xBF}):
		return string(b[3:]), nil
	case bytes.HasPrefix(b, []byte{0xFF, 0xFE}):
		out, _, err := transform.Bytes(unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewDecoder(), b)
		return string(out), err
	case bytes.HasPrefix(b, []byte{0xFE, 0xFF}):
		out, _, err := transform.Bytes(unicode.UTF16(unicode.BigEndian, unicode.UseBOM).NewDecoder(), b)
		return string(out), err
	}
	if utf8.Valid(b) {
		return string(b), nil
	}
	if out, _, err := transform.Bytes(simplifiedchinese.GB18030.NewDecoder(), b); err == nil && utf8.Valid(out) {
		return string(out), nil
	}
	if out, _, err := transform.Bytes(traditionalchinese.Big5.NewDecoder(), b); err == nil {
		return string(out), nil
	}
	return "", errors.New("无法识别文本编码，请另存为 UTF-8 后再上传")
}

var mdRe = []struct {
	re  *regexp.Regexp
	rep string
}{
	{regexp.MustCompile(`(?m)^#{1,6}\s*`), ""},
	{regexp.MustCompile(`\*\*(.+?)\*\*`), "$1"},
	{regexp.MustCompile(`__(.+?)__`), "$1"},
	{regexp.MustCompile(`(?m)^>\s?`), ""},
	{regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`), ""},
	{regexp.MustCompile(`\[([^\]]+)\]\([^)]*\)`), "$1"},
	{regexp.MustCompile("`([^`]+)`"), "$1"},
}

func stripMarkdown(s string) string {
	for _, r := range mdRe {
		s = r.re.ReplaceAllString(s, r.rep)
	}
	return s
}

func readZip(z *zip.Reader, name string) ([]byte, error) {
	for _, f := range z.File {
		if f.Name == name || strings.EqualFold(f.Name, name) {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(io.LimitReader(rc, 64<<20))
		}
	}
	return nil, fmt.Errorf("缺少 %s", name)
}

// ---------- docx ----------

func parseDocx(data []byte) (*Document, error) {
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, errors.New("docx 文件已损坏")
	}
	doc, err := readZip(z, "word/document.xml")
	if err != nil {
		return nil, err
	}
	dec := xml.NewDecoder(bytes.NewReader(doc))
	var (
		paras  []string
		heads  []bool
		cur    strings.Builder
		style  string
		inText bool
	)
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "p":
				cur.Reset()
				style = ""
			case "pStyle":
				for _, a := range t.Attr {
					if a.Name.Local == "val" {
						style = a.Value
					}
				}
			case "t":
				inText = true
			case "tab":
				cur.WriteString(" ")
			case "br":
				cur.WriteString("\n")
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "t":
				inText = false
			case "p":
				paras = append(paras, cur.String())
				s := strings.ToLower(style)
				heads = append(heads, strings.HasPrefix(s, "heading1") || strings.HasPrefix(s, "heading2") || s == "1" || s == "2" || strings.HasPrefix(s, "title") || strings.Contains(style, "标题"))
			}
		case xml.CharData:
			if inText {
				cur.Write(t)
			}
		}
	}
	d := &Document{Text: strings.Join(paras, "\n")}
	// structural sections from headings
	var secs []Section
	var buf []string
	title := ""
	hasHead := false
	for i, p := range paras {
		if heads[i] && strings.TrimSpace(p) != "" {
			hasHead = true
			if title != "" || len(buf) > 0 {
				secs = append(secs, Section{Title: title, Text: strings.TrimSpace(strings.Join(buf, "\n"))})
			}
			title = strings.TrimSpace(p)
			buf = nil
			continue
		}
		buf = append(buf, p)
	}
	if hasHead {
		secs = append(secs, Section{Title: title, Text: strings.TrimSpace(strings.Join(buf, "\n"))})
		d.Sections = secs
	}
	if core, err := readZip(z, "docProps/core.xml"); err == nil {
		var cp struct {
			Title   string `xml:"title"`
			Creator string `xml:"creator"`
		}
		if xml.Unmarshal(core, &cp) == nil {
			d.Title, d.Author = cp.Title, cp.Creator
		}
	}
	return d, nil
}

// ---------- epub ----------

var (
	tagRe      = regexp.MustCompile(`(?s)<[^>]+>`)
	blockRe    = regexp.MustCompile(`(?i)</(p|div|h[1-6]|li|br|tr|section|blockquote)\s*>|<br\s*/?>`)
	headRe     = regexp.MustCompile(`(?is)<h[1-3][^>]*>(.*?)</h[1-3]>`)
	bodyRe     = regexp.MustCompile(`(?is)<body[^>]*>(.*)</body>`)
	titleTagRe = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	scriptRe   = regexp.MustCompile(`(?is)<(script|style)[^>]*>.*?</(script|style)>`)
)

func htmlToText(s string) string {
	if m := bodyRe.FindStringSubmatch(s); m != nil {
		s = m[1]
	}
	s = scriptRe.ReplaceAllString(s, "")
	s = blockRe.ReplaceAllString(s, "\n")
	s = tagRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	lines := strings.Split(s, "\n")
	var out []string
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l != "" {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

func parseEpub(data []byte) (*Document, error) {
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, errors.New("epub 文件已损坏")
	}
	container, err := readZip(z, "META-INF/container.xml")
	if err != nil {
		return nil, err
	}
	var c struct {
		Rootfiles []struct {
			Path string `xml:"full-path,attr"`
		} `xml:"rootfiles>rootfile"`
	}
	if err := xml.Unmarshal(container, &c); err != nil || len(c.Rootfiles) == 0 {
		return nil, errors.New("epub 缺少 OPF 描述")
	}
	opfPath := c.Rootfiles[0].Path
	opfData, err := readZip(z, opfPath)
	if err != nil {
		return nil, err
	}
	var opf struct {
		Metadata struct {
			Title   []string `xml:"title"`
			Creator []string `xml:"creator"`
			Meta    []struct {
				Name    string `xml:"name,attr"`
				Content string `xml:"content,attr"`
			} `xml:"meta"`
		} `xml:"metadata"`
		Manifest []struct {
			ID         string `xml:"id,attr"`
			Href       string `xml:"href,attr"`
			MediaType  string `xml:"media-type,attr"`
			Properties string `xml:"properties,attr"`
		} `xml:"manifest>item"`
		Spine []struct {
			IDRef string `xml:"idref,attr"`
		} `xml:"spine>itemref"`
	}
	if err := xml.Unmarshal(opfData, &opf); err != nil {
		return nil, fmt.Errorf("epub OPF 解析失败：%v", err)
	}
	base := path.Dir(opfPath)
	href := map[string]string{}
	media := map[string]string{}
	coverID := ""
	for _, m := range opf.Metadata.Meta {
		if m.Name == "cover" {
			coverID = m.Content
		}
	}
	for _, it := range opf.Manifest {
		href[it.ID] = path.Join(base, it.Href)
		media[it.ID] = it.MediaType
		if strings.Contains(it.Properties, "cover-image") {
			coverID = it.ID
		}
	}
	d := &Document{}
	if len(opf.Metadata.Title) > 0 {
		d.Title = strings.TrimSpace(opf.Metadata.Title[0])
	}
	if len(opf.Metadata.Creator) > 0 {
		d.Author = strings.TrimSpace(opf.Metadata.Creator[0])
	}
	if coverID != "" {
		if b, err := readZip(z, href[coverID]); err == nil {
			d.Cover = b
			d.CoverExt = strings.TrimPrefix(path.Ext(href[coverID]), ".")
		}
	}
	var all []string
	for _, s := range opf.Spine {
		p, ok := href[s.IDRef]
		if !ok || !strings.Contains(media[s.IDRef], "html") {
			continue
		}
		raw, err := readZip(z, p)
		if err != nil {
			continue
		}
		h := string(raw)
		title := ""
		if m := headRe.FindStringSubmatch(h); m != nil {
			title = strings.TrimSpace(html.UnescapeString(tagRe.ReplaceAllString(m[1], "")))
		} else if m := titleTagRe.FindStringSubmatch(h); m != nil {
			title = strings.TrimSpace(html.UnescapeString(m[1]))
		}
		body := htmlToText(h)
		if strings.TrimSpace(body) == "" {
			continue
		}
		// drop a duplicated heading line at the top of the body
		if title != "" {
			lines := strings.SplitN(body, "\n", 2)
			if strings.TrimSpace(lines[0]) == title && len(lines) == 2 {
				body = lines[1]
			}
		}
		d.Sections = append(d.Sections, Section{Title: title, Text: body})
		all = append(all, title, body)
	}
	d.Text = strings.Join(all, "\n")
	if len(d.Sections) == 0 {
		return nil, errors.New("epub 中没有可读取的正文")
	}
	return d, nil
}

// ---------- pdf ----------

func parsePDF(data []byte) (doc *Document, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errors.New("PDF 解析失败，建议转换为 txt 或 docx 后上传")
		}
	}()
	r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, errors.New("PDF 文件无法读取（可能已加密）")
	}
	var pages []string
	for i := 1; i <= r.NumPage(); i++ {
		p := r.Page(i)
		if p.V.IsNull() {
			continue
		}
		rows, err := p.GetTextByRow()
		if err != nil {
			continue
		}
		sort.SliceStable(rows, func(a, b int) bool { return rows[a].Position > rows[b].Position })
		var lines []string
		for _, row := range rows {
			var sb strings.Builder
			for _, w := range row.Content {
				sb.WriteString(w.S)
			}
			if t := strings.TrimSpace(sb.String()); t != "" {
				lines = append(lines, t)
			}
		}
		pages = append(pages, strings.Join(lines, "\n"))
	}
	text := strings.Join(pages, "\n")
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("PDF 中没有可提取的文字（可能是扫描件），请先进行 OCR")
	}
	return &Document{Text: joinWrappedLines(text)}, nil
}

// joinWrappedLines merges hard-wrapped PDF lines back into paragraphs.
func joinWrappedLines(s string) string {
	lines := strings.Split(s, "\n")
	var out []string
	var cur strings.Builder
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		cur.WriteString(l)
		last, _ := utf8.DecodeLastRuneInString(l)
		if strings.ContainsRune("。！？”」』…!?.", last) || utf8.RuneCountInString(l) < 18 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return strings.Join(out, "\n")
}
