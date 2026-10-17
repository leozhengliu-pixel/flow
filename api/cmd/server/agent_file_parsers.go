package main

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"
)

// Text extraction for the read_file chat tool. PDFs go through github.com/ledongthuc/pdf (BSD-3-Clause, a maintained
// fork of rsc/pdf: pure Go, no cgo, no external processes). Office files are zip+XML and use the standard library
// with limits on the archive, on each decompressed entry and on the XML read.

const (
	// agentReadFileMaxBytes is the largest file read_file opens (the composer's upload limit).
	agentReadFileMaxBytes = agentUploadMaxBytes
	// agentReadTextMax caps the text one read_file call returns.
	agentReadTextMax = 100 << 10
	agentReadMaxRows = 3000
	agentReadMaxCols = 50
	agentReadMaxCell = 500
	agentReadMaxPDFs = 300
	// Zip guards: entries per archive, one parsed entry, and all entries read for one file.
	zipMaxEntries    = 5000
	zipMaxEntryBytes = 64 << 20
	zipMaxTotalBytes = 128 << 20
	zipMaxDeclared   = 1 << 30
)

const agentReadTruncated = "\n[truncated]"

var errAgentFileTooLarge = errors.New("file is too large to read")

// cappedText collects output up to agentReadTextMax bytes.
type cappedText struct {
	builder strings.Builder
	full    bool
}

func (c *cappedText) Write(text string) bool {
	if c.full {
		return false
	}
	if room := agentReadTextMax - c.builder.Len(); len(text) > room {
		cut := room
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		c.builder.WriteString(text[:cut])
		c.full = true
		return false
	}
	c.builder.WriteString(text)
	return true
}

func (c *cappedText) String() string {
	if c.full {
		return c.builder.String() + agentReadTruncated
	}
	return c.builder.String()
}

// parseAgentFile turns file bytes into text for the model. Types it cannot read return an explanation, not an error.
func parseAgentFile(name, contentType string, data []byte) (string, error) {
	switch kind := agentFileKind(name, contentType, data); kind {
	case "pdf":
		return parsePDFText(data)
	case "xlsx", "docx", "pptx":
		archive, err := openAgentZip(data)
		if err != nil {
			return "", err
		}
		switch kind {
		case "xlsx":
			return parseXLSXText(archive)
		case "docx":
			return parseDOCXText(archive)
		}
		return parsePPTXText(archive)
	case "csv":
		return parseDelimitedText(data, ',')
	case "tsv":
		return parseDelimitedText(data, '\t')
	case "text":
		return limitAgentText(strings.ToValidUTF8(strings.TrimPrefix(string(data), "\ufeff"), "")), nil
	case "image":
		return "This is an image. It is shown to you directly in the conversation, so there is nothing to read.", nil
	case "legacy":
		return fmt.Sprintf("%s is an old binary Office format (.doc/.xls/.ppt) that cannot be read. Ask the user to save it as .docx, .xlsx or .pptx, or paste the content.", name), nil
	case "zip":
		return fmt.Sprintf("%s is a zip archive; its contents cannot be read.", name), nil
	}
	return fmt.Sprintf("%s (%s) is a binary file that cannot be read as text.", name, firstNonEmpty(contentType, "unknown type")), nil
}

func limitAgentText(text string) string {
	out := cappedText{}
	out.Write(text)
	return out.String()
}

var agentTextExtensions = map[string]bool{
	".md": true, ".markdown": true, ".mdx": true, ".txt": true, ".text": true, ".log": true, ".json": true, ".jsonl": true, ".ndjson": true,
	".xml": true, ".yaml": true, ".yml": true, ".toml": true, ".ini": true, ".cfg": true, ".conf": true, ".env": true, ".properties": true,
	".html": true, ".htm": true, ".css": true, ".scss": true, ".js": true, ".mjs": true, ".cjs": true, ".jsx": true, ".ts": true, ".tsx": true,
	".go": true, ".py": true, ".rb": true, ".php": true, ".java": true, ".kt": true, ".swift": true, ".c": true, ".h": true, ".cc": true, ".cpp": true,
	".hpp": true, ".cs": true, ".rs": true, ".sh": true, ".bash": true, ".zsh": true, ".sql": true, ".graphql": true, ".proto": true, ".diff": true,
	".patch": true, ".tex": true, ".rst": true, ".svg": true, ".vue": true, ".svelte": true, ".lua": true, ".r": true, ".scala": true, ".dart": true,
}

// agentFileKind decides how a file is read: by its bytes first (a label can lie), then by type and extension.
func agentFileKind(name, contentType string, data []byte) string {
	mediaType, _, _ := mime.ParseMediaType(contentType)
	mediaType = strings.ToLower(mediaType)
	ext := strings.ToLower(filepath.Ext(name))
	switch {
	case bytes.HasPrefix(data, []byte("%PDF-")):
		return "pdf"
	case bytes.HasPrefix(data, []byte("PK\x03\x04")):
		return zipFileKind(data)
	case bytes.HasPrefix(data, []byte("\xd0\xcf\x11\xe0\xa1\xb1\x1a\xe1")):
		return "legacy"
	}
	if loopAttachmentImageType(http.DetectContentType(data)) != "" {
		return "image"
	}
	if bytes.IndexByte(data[:min(len(data), 8192)], 0) >= 0 {
		return ""
	}
	switch {
	case ext == ".csv" || mediaType == "text/csv":
		return "csv"
	case ext == ".tsv" || mediaType == "text/tab-separated-values":
		return "tsv"
	case strings.HasPrefix(mediaType, "text/"), strings.HasSuffix(mediaType, "+json"), strings.HasSuffix(mediaType, "+xml"), agentTextExtensions[ext]:
		return "text"
	}
	switch mediaType {
	case "application/json", "application/xml", "application/x-yaml", "application/yaml", "application/javascript", "application/x-sh", "application/sql", "application/toml":
		return "text"
	case "", "application/octet-stream":
		if looksLikeText(data) {
			return "text"
		}
	}
	return ""
}

// looksLikeText reports whether the start of data is UTF-8 (a rune cut at the end of the sample is fine).
func looksLikeText(data []byte) bool {
	sample := data[:min(len(data), 8192)]
	for trim := 0; trim < 4 && trim <= len(sample); trim++ {
		if utf8.Valid(sample[:len(sample)-trim]) {
			return true
		}
		if len(data) == len(sample) {
			break
		}
	}
	return false
}

func zipFileKind(data []byte) string {
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil || len(archive.File) > zipMaxEntries {
		return "zip"
	}
	for _, file := range archive.File {
		switch file.Name {
		case "xl/workbook.xml":
			return "xlsx"
		case "word/document.xml":
			return "docx"
		case "ppt/presentation.xml":
			return "pptx"
		}
	}
	return "zip"
}

// --- PDF ---

func parsePDFText(data []byte) (text string, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			text, err = "", fmt.Errorf("could not parse the PDF")
		}
	}()
	reader, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "password") || strings.Contains(strings.ToLower(err.Error()), "encrypt") {
			return "This PDF is password-protected and cannot be read.", nil
		}
		return "", fmt.Errorf("could not parse the PDF")
	}
	out := cappedText{}
	pages := reader.NumPage()
	found := false
	fonts := map[string]*pdf.Font{}
	for number := 1; number <= min(pages, agentReadMaxPDFs); number++ {
		page := reader.Page(number)
		pageText, pageErr := page.GetPlainText(fonts)
		pageText = strings.TrimSpace(pageText)
		switch {
		case pageErr != nil && pageText == "":
			pageText = "(this page could not be read)"
		case pageText == "":
			pageText = "(no extractable text on this page)"
		default:
			found = true
		}
		if !out.Write(fmt.Sprintf("Page %d:\n%s\n\n", number, pageText)) {
			break
		}
	}
	if pages > agentReadMaxPDFs && !out.full {
		out.Write(fmt.Sprintf("[%d more pages not shown]\n", pages-agentReadMaxPDFs))
	}
	if !found {
		return "This PDF has no extractable text (it may be scanned images).", nil
	}
	return strings.TrimSpace(out.String()), nil
}

// --- Zip and XML helpers ---

// agentZip is an opened archive with a shared budget for everything read from it.
type agentZip struct {
	files     map[string]*zip.File
	remaining int64
}

func openAgentZip(data []byte) (*agentZip, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("could not open the file as an Office document")
	}
	if len(reader.File) > zipMaxEntries {
		return nil, fmt.Errorf("the archive has too many entries")
	}
	archive := &agentZip{files: make(map[string]*zip.File, len(reader.File)), remaining: zipMaxTotalBytes}
	var declared uint64
	for _, file := range reader.File {
		declared += file.UncompressedSize64
		if declared > zipMaxDeclared {
			return nil, fmt.Errorf("the archive expands to too much data")
		}
		archive.files[file.Name] = file
	}
	return archive, nil
}

// open returns an entry's stream, limited by its own bound and the archive's remaining budget (headers can lie, so
// the real decompressed bytes are what count). The error of an overrun surfaces from Read as errAgentFileTooLarge.
func (a *agentZip) open(name string, limit int64) (io.Reader, io.Closer, error) {
	file := a.files[name]
	if file == nil {
		return nil, nil, fmt.Errorf("%s is missing", name)
	}
	if file.UncompressedSize64 > uint64(limit) {
		return nil, nil, errAgentFileTooLarge
	}
	rc, err := file.Open()
	if err != nil {
		return nil, nil, err
	}
	return &budgetReader{reader: rc, archive: a, left: limit}, rc, nil
}

type budgetReader struct {
	reader  io.Reader
	archive *agentZip
	left    int64
}

func (b *budgetReader) Read(p []byte) (int, error) {
	if b.left <= 0 || b.archive.remaining <= 0 {
		// Probe one byte: a clean end is fine, more data is an overrun.
		var probe [1]byte
		if n, _ := b.reader.Read(probe[:]); n > 0 {
			return 0, errAgentFileTooLarge
		}
		return 0, io.EOF
	}
	if int64(len(p)) > b.left {
		p = p[:b.left]
	}
	n, err := b.reader.Read(p)
	b.left -= int64(n)
	b.archive.remaining -= int64(n)
	return n, err
}

func (a *agentZip) has(name string) bool { return a.files[name] != nil }

func (a *agentZip) read(name string, limit int64) ([]byte, error) {
	reader, closer, err := a.open(name, limit)
	if err != nil {
		return nil, err
	}
	defer closer.Close()
	return io.ReadAll(reader)
}

// xmlStream decodes an entry token by token; non-UTF-8 charsets and entity bombs are refused by the decoder.
func (a *agentZip) xmlStream(name string, limit int64) (*xml.Decoder, io.Closer, error) {
	reader, closer, err := a.open(name, limit)
	if err != nil {
		return nil, nil, err
	}
	decoder := xml.NewDecoder(reader)
	decoder.Strict = false
	decoder.Entity = nil
	return decoder, closer, nil
}

func attrValue(start xml.StartElement, local string) string {
	for _, attr := range start.Attr {
		if attr.Name.Local == local {
			return attr.Value
		}
	}
	return ""
}

// elementText reads the character data of the element just opened, to its end.
func elementText(decoder *xml.Decoder) (string, error) {
	var builder strings.Builder
	depth := 1
	for depth > 0 {
		token, err := decoder.Token()
		if err != nil {
			return builder.String(), err
		}
		switch item := token.(type) {
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
		case xml.CharData:
			if builder.Len() < 1<<20 {
				builder.Write(item)
			}
		}
	}
	return builder.String(), nil
}

// --- XLSX ---

const (
	xlsxMaxSheets  = 30
	xlsxMaxShared  = 2_000_000
	xlsxSharedSize = 16 << 20
	xlsxMaxColumn  = 16384
)

type xlsxSheet struct{ name, file string }

func parseXLSXText(archive *agentZip) (string, error) {
	sheets, err := xlsxSheets(archive)
	if err != nil {
		return "", err
	}
	if len(sheets) == 0 {
		return "This workbook has no sheets.", nil
	}
	shared, err := xlsxSharedStrings(archive)
	if err != nil {
		return "", err
	}
	out := cappedText{}
	for index, sheet := range sheets {
		if index >= xlsxMaxSheets {
			out.Write(fmt.Sprintf("[%d more sheets not shown]\n", len(sheets)-xlsxMaxSheets))
			break
		}
		rows, more, err := xlsxRows(archive, sheet.file, shared)
		if err != nil {
			if errors.Is(err, errAgentFileTooLarge) {
				return "", err
			}
			if !out.Write(fmt.Sprintf("## %s\n(this sheet could not be read)\n\n", sheet.name)) {
				break
			}
			continue
		}
		if !out.Write("## "+sheet.name+"\n") || !writeMarkdownTable(&out, rows, more) {
			break
		}
		out.Write("\n")
	}
	return strings.TrimSpace(out.String()), nil
}

func xlsxSheets(archive *agentZip) ([]xlsxSheet, error) {
	decoder, closer, err := archive.xmlStream("xl/workbook.xml", 4<<20)
	if err != nil {
		return nil, err
	}
	defer closer.Close()
	type entry struct{ name, rid string }
	var entries []entry
	for {
		token, err := decoder.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			if errors.Is(err, errAgentFileTooLarge) {
				return nil, err
			}
			return nil, fmt.Errorf("could not read the workbook")
		}
		if start, ok := token.(xml.StartElement); ok && start.Name.Local == "sheet" {
			entries = append(entries, entry{name: attrValue(start, "name"), rid: attrValue(start, "id")})
		}
	}
	targets := map[string]string{}
	if relations, closer, err := archive.xmlStream("xl/_rels/workbook.xml.rels", 4<<20); err == nil {
		defer closer.Close()
		for {
			token, err := relations.Token()
			if err != nil {
				break
			}
			if start, ok := token.(xml.StartElement); ok && start.Name.Local == "Relationship" {
				targets[attrValue(start, "Id")] = attrValue(start, "Target")
			}
		}
	}
	var sheets []xlsxSheet
	for index, item := range entries {
		target := targets[item.rid]
		file := ""
		switch {
		case strings.HasPrefix(target, "/"):
			file = strings.TrimPrefix(path.Clean(target), "/")
		case target != "":
			file = path.Join("xl", target)
		default:
			file = fmt.Sprintf("xl/worksheets/sheet%d.xml", index+1)
		}
		if archive.has(file) {
			sheets = append(sheets, xlsxSheet{name: firstNonEmpty(item.name, fmt.Sprintf("Sheet%d", index+1)), file: file})
		}
	}
	return sheets, nil
}

func xlsxSharedStrings(archive *agentZip) ([]string, error) {
	if !archive.has("xl/sharedStrings.xml") {
		return nil, nil
	}
	decoder, closer, err := archive.xmlStream("xl/sharedStrings.xml", zipMaxEntryBytes)
	if err != nil {
		return nil, err
	}
	defer closer.Close()
	var shared []string
	total := 0
	for {
		token, err := decoder.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return shared, nil
			}
			if errors.Is(err, errAgentFileTooLarge) {
				return nil, err
			}
			return shared, nil
		}
		if start, ok := token.(xml.StartElement); ok && start.Name.Local == "si" {
			text, err := xlsxRichText(decoder)
			if err != nil && !errors.Is(err, io.EOF) {
				return nil, err
			}
			total += len(text)
			if len(shared) >= xlsxMaxShared || total > xlsxSharedSize {
				return nil, errAgentFileTooLarge
			}
			shared = append(shared, text)
		}
	}
}

// xlsxRichText reads an <si> or <is> element's text runs, skipping phonetic hints.
func xlsxRichText(decoder *xml.Decoder) (string, error) {
	var builder strings.Builder
	depth := 1
	inText, skip := false, 0
	for depth > 0 {
		token, err := decoder.Token()
		if err != nil {
			return builder.String(), err
		}
		switch item := token.(type) {
		case xml.StartElement:
			depth++
			switch {
			case skip > 0 || item.Name.Local == "rPh":
				skip++
			case item.Name.Local == "t":
				inText = true
			}
		case xml.EndElement:
			depth--
			if skip > 0 {
				skip--
			}
			if item.Name.Local == "t" {
				inText = false
			}
		case xml.CharData:
			if inText && skip == 0 && builder.Len() < 1<<20 {
				builder.Write(item)
			}
		}
	}
	return builder.String(), nil
}

var xlsxCellRef = regexp.MustCompile(`^([A-Za-z]{1,3})(\d*)$`)

func xlsxColumn(ref string) int {
	match := xlsxCellRef.FindStringSubmatch(ref)
	if match == nil {
		return -1
	}
	column := 0
	for _, letter := range strings.ToUpper(match[1]) {
		column = column*26 + int(letter-'A') + 1
	}
	if column > xlsxMaxColumn {
		return -1
	}
	return column - 1
}

// xlsxRows reads a sheet's cell values as rows of text. It keeps up to agentReadMaxRows rows and agentReadMaxCols
// columns and reports whether rows were left out. Trailing empty rows and columns are dropped.
func xlsxRows(archive *agentZip, file string, shared []string) ([][]string, bool, error) {
	decoder, closer, err := archive.xmlStream(file, zipMaxEntryBytes)
	if err != nil {
		return nil, false, err
	}
	defer closer.Close()
	var rows [][]string
	var row []string
	more, inRow := false, false
	nextColumn := 0
	for {
		token, err := decoder.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			if errors.Is(err, errAgentFileTooLarge) {
				return nil, false, err
			}
			break
		}
		start, ok := token.(xml.StartElement)
		if ok {
			switch start.Name.Local {
			case "row":
				row, inRow, nextColumn = nil, true, 0
				if reference, err := strconv.Atoi(attrValue(start, "r")); err == nil && reference > 0 && reference <= 1_048_576 {
					// Rows the file skips are empty rows.
					for len(rows) < reference-1 && len(rows) < agentReadMaxRows {
						rows = append(rows, nil)
					}
				}
			case "c":
				if !inRow {
					continue
				}
				column := xlsxColumn(attrValue(start, "r"))
				if column < 0 {
					column = nextColumn
				}
				nextColumn = column + 1
				value, err := xlsxCellValue(decoder, start, shared)
				if err != nil && !errors.Is(err, io.EOF) {
					return nil, false, err
				}
				if column >= agentReadMaxCols || value == "" {
					continue
				}
				for len(row) <= column {
					row = append(row, "")
				}
				row[column] = value
			}
			continue
		}
		if end, ok := token.(xml.EndElement); ok && end.Name.Local == "row" && inRow {
			inRow = false
			if len(rows) >= agentReadMaxRows {
				more = true
				break
			}
			rows = append(rows, row)
		}
	}
	for len(rows) > 0 && rowEmpty(rows[len(rows)-1]) {
		rows = rows[:len(rows)-1]
	}
	return rows, more, nil
}

func rowEmpty(row []string) bool {
	for _, cell := range row {
		if cell != "" {
			return false
		}
	}
	return true
}

// xlsxCellValue reads one <c> element to its end and resolves its value by cell type.
func xlsxCellValue(decoder *xml.Decoder, cell xml.StartElement, shared []string) (string, error) {
	kind := attrValue(cell, "t")
	value, inline := "", ""
	depth := 1
	for depth > 0 {
		token, err := decoder.Token()
		if err != nil {
			return "", err
		}
		switch item := token.(type) {
		case xml.StartElement:
			switch item.Name.Local {
			case "v":
				text, err := elementText(decoder)
				if err != nil {
					return "", err
				}
				value = strings.TrimSpace(text)
			case "is":
				text, err := xlsxRichText(decoder)
				if err != nil {
					return "", err
				}
				inline = text
			default:
				depth++
			}
		case xml.EndElement:
			depth--
		}
	}
	switch kind {
	case "s":
		if index, err := strconv.Atoi(value); err == nil && index >= 0 && index < len(shared) {
			return shared[index], nil
		}
		return "", nil
	case "inlineStr":
		return inline, nil
	case "b":
		if value == "1" {
			return "TRUE", nil
		}
		if value == "0" {
			return "FALSE", nil
		}
	case "e":
		return value, nil
	}
	if value == "" {
		return inline, nil
	}
	return value, nil
}

// --- Markdown tables ---

func markdownCell(cell string) string {
	cell = strings.Join(strings.Fields(strings.ReplaceAll(cell, "|", `\|`)), " ")
	if utf8.RuneCountInString(cell) > agentReadMaxCell {
		cell = string([]rune(cell)[:agentReadMaxCell]) + "…"
	}
	return cell
}

// writeMarkdownTable writes rows as a Markdown table whose header is the first row; false once the output is full.
func writeMarkdownTable(out *cappedText, rows [][]string, more bool) bool {
	columns := 0
	for _, row := range rows {
		columns = max(columns, len(row))
	}
	if len(rows) == 0 || columns == 0 {
		return out.Write("(empty)\n")
	}
	line := func(row []string) string {
		cells := make([]string, columns)
		for index := range cells {
			if index < len(row) {
				cells[index] = markdownCell(row[index])
			}
		}
		return "| " + strings.Join(cells, " | ") + " |\n"
	}
	if !out.Write(line(rows[0])) || !out.Write("|"+strings.Repeat(" --- |", columns)+"\n") {
		return false
	}
	for _, row := range rows[1:] {
		if !out.Write(line(row)) {
			return false
		}
	}
	if more {
		return out.Write(fmt.Sprintf("[truncated: only the first %d rows are shown]\n", agentReadMaxRows))
	}
	return true
}

// --- CSV and TSV ---

func parseDelimitedText(data []byte, delimiter rune) (string, error) {
	text := strings.ToValidUTF8(strings.TrimPrefix(string(data), "\ufeff"), "")
	reader := csv.NewReader(strings.NewReader(text))
	reader.Comma, reader.LazyQuotes, reader.FieldsPerRecord = delimiter, true, -1
	var rows [][]string
	more := false
	for {
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if len(rows) == 0 {
				return limitAgentText(text), nil
			}
			break
		}
		if len(rows) >= agentReadMaxRows {
			more = true
			break
		}
		rows = append(rows, record[:min(len(record), agentReadMaxCols)])
	}
	for len(rows) > 0 && rowEmpty(rows[len(rows)-1]) {
		rows = rows[:len(rows)-1]
	}
	out := cappedText{}
	writeMarkdownTable(&out, rows, more)
	return strings.TrimSpace(out.String()), nil
}

// --- DOCX ---

func parseDOCXText(archive *agentZip) (string, error) {
	decoder, closer, err := archive.xmlStream("word/document.xml", zipMaxEntryBytes)
	if err != nil {
		return "", err
	}
	defer closer.Close()
	out := cappedText{}
	var paragraph strings.Builder
	var cell *strings.Builder
	var cells []string
	var rows [][]string
	heading, listItem := "", false
	inText := false
	flushParagraph := func() bool {
		text := strings.TrimRight(paragraph.String(), " \t\n")
		paragraph.Reset()
		if cell != nil {
			if text != "" {
				if cell.Len() > 0 {
					cell.WriteString(" ")
				}
				cell.WriteString(text)
			}
			heading, listItem = "", false
			return true
		}
		prefix := heading
		if prefix == "" && listItem {
			prefix = "- "
		}
		heading, listItem = "", false
		if text == "" {
			return true
		}
		return out.Write(prefix + text + "\n\n")
	}
	tableDepth := 0
	for {
		token, err := decoder.Token()
		if err != nil {
			if errors.Is(err, errAgentFileTooLarge) {
				return "", err
			}
			break
		}
		switch item := token.(type) {
		case xml.StartElement:
			switch item.Name.Local {
			case "tbl":
				tableDepth++
				if tableDepth == 1 {
					rows = nil
				}
			case "tr":
				if tableDepth == 1 {
					cells = nil
				}
			case "tc":
				if tableDepth == 1 {
					cell = &strings.Builder{}
				}
			case "p":
				paragraph.Reset()
			case "pStyle":
				style := strings.ToLower(attrValue(item, "val"))
				switch {
				case style == "title":
					heading = "# "
				case strings.HasPrefix(style, "heading"):
					level, _ := strconv.Atoi(strings.TrimPrefix(style, "heading"))
					heading = strings.Repeat("#", min(max(level, 1), 6)) + " "
				}
			case "numPr":
				listItem = true
			case "t":
				inText = true
			case "tab":
				paragraph.WriteString("\t")
			case "br", "cr":
				paragraph.WriteString("\n")
			}
		case xml.EndElement:
			switch item.Name.Local {
			case "t":
				inText = false
			case "p":
				if !flushParagraph() {
					return out.String(), nil
				}
			case "tc":
				if tableDepth == 1 && cell != nil {
					cells = append(cells, cell.String())
					cell = nil
				}
			case "tr":
				if tableDepth == 1 {
					rows = append(rows, cells)
				}
			case "tbl":
				if tableDepth == 1 {
					rows = rows[:min(len(rows), agentReadMaxRows)]
					for index := range rows {
						rows[index] = rows[index][:min(len(rows[index]), agentReadMaxCols)]
					}
					if !writeMarkdownTable(&out, rows, false) || !out.Write("\n") {
						return out.String(), nil
					}
				}
				tableDepth = max(tableDepth-1, 0)
			}
		case xml.CharData:
			if inText && paragraph.Len() < 1<<20 {
				paragraph.Write(item)
			}
		}
	}
	text := strings.TrimSpace(out.String())
	if text == "" {
		return "This document has no text.", nil
	}
	return text, nil
}

// --- PPTX ---

var pptxSlideName = regexp.MustCompile(`^ppt/slides/slide(\d+)\.xml$`)

func parsePPTXText(archive *agentZip) (string, error) {
	type slide struct {
		number int
		name   string
	}
	var slides []slide
	for name := range archive.files {
		if match := pptxSlideName.FindStringSubmatch(name); match != nil {
			number, _ := strconv.Atoi(match[1])
			slides = append(slides, slide{number, name})
		}
	}
	sort.Slice(slides, func(i, j int) bool { return slides[i].number < slides[j].number })
	out := cappedText{}
	for _, item := range slides {
		decoder, closer, err := archive.xmlStream(item.name, 8<<20)
		if err != nil {
			if errors.Is(err, errAgentFileTooLarge) {
				return "", err
			}
			continue
		}
		var lines []string
		var line strings.Builder
		inText := false
	tokens:
		for {
			token, err := decoder.Token()
			if err != nil {
				break
			}
			switch part := token.(type) {
			case xml.StartElement:
				switch part.Name.Local {
				case "t":
					inText = true
				case "br":
					line.WriteString("\n")
				}
			case xml.EndElement:
				switch part.Name.Local {
				case "t":
					inText = false
				case "p":
					if text := strings.TrimSpace(line.String()); text != "" {
						lines = append(lines, text)
					}
					line.Reset()
					if len(lines) > 2000 {
						break tokens
					}
				}
			case xml.CharData:
				if inText && line.Len() < 1<<16 {
					line.Write(part)
				}
			}
		}
		closer.Close()
		body := "(no text)"
		if len(lines) > 0 {
			body = strings.Join(lines, "\n")
		}
		if !out.Write(fmt.Sprintf("Slide %d:\n%s\n\n", item.number, body)) {
			break
		}
	}
	if out.builder.Len() == 0 {
		return "This presentation has no slides with text.", nil
	}
	return strings.TrimSpace(out.String()), nil
}
