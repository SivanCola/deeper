// Package builtin provides Deepzora's compile-time built-in tools. Each tool
// self-registers via init(); main blank-imports this package to wire them in.
package builtin

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/text/transform"

	fileenc "deepzora/internal/fileutil/encoding"
	"deepzora/internal/tool"
)

const (
	readFileBinaryPeek        = 8 * 1024   // bytes scanned for NUL before reading further
	readFileDetectSample      = 256 * 1024 // bytes sampled for encoding detection before streaming
	readFileMaxLineBytes      = 1024 * 1024
	readFileMaxFormattedBytes = 8 << 20
)

func init() { tool.RegisterBuiltin(readFile{}) }

// readFile reads a text file. workDir, when non-empty, is the directory a
// relative path is resolved against (see resolveIn). paths maps session-scoped
// external read aliases to local roots without changing the model-visible tool
// schema. forbidRoots lists directories the tool may not read from (resolved,
// absolute paths).
type readFile struct {
	workDir     string
	paths       *PathResolver
	forbidRoots []string
	// overlay, when non-nil, serves content from the host transport (unsaved
	// editor buffers) before falling back to disk. Consulted only after path
	// resolution and read confinement, and never for external alias paths.
	overlay  FileOverlay
	captured *tool.ReadResultSource
}

const (
	readFileDefaultLimit = 2000 // lines returned when limit is unset
)

// readFileParams is one validated read_file call with defaults applied.
type readFileParams struct {
	Path        string
	WindowGiven bool
	Offset      int
	Limit       int
}

const (
	readFileEmptyOutput = "(empty file)"
	readFilePastEOFTail = " is past EOF — file has "
)

// readWindowGiven reports whether the call named an explicit line window.
func readWindowGiven(args json.RawMessage) bool {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(args, &fields); err != nil {
		return false
	}
	_, offset := fields["offset"]
	_, limit := fields["limit"]
	return offset || limit
}

// parseReadFileParams validates one read_file call and applies the documented
// defaults, so Execute and ReadEnvelope agree on what was requested.
func parseReadFileParams(args json.RawMessage) (readFileParams, error) {
	var p struct {
		Path   string `json:"path"`
		Offset int    `json:"offset,omitempty"`
		Limit  int    `json:"limit,omitempty"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return readFileParams{}, fmt.Errorf("invalid args: %w", err)
	}
	if p.Path == "" {
		return readFileParams{}, fmt.Errorf("path is required")
	}
	windowGiven := readWindowGiven(args)
	if p.Offset < 0 {
		p.Offset = 0
	}
	if p.Limit <= 0 {
		p.Limit = readFileDefaultLimit
	}
	return readFileParams{Path: p.Path, WindowGiven: windowGiven, Offset: p.Offset, Limit: p.Limit}, nil
}

func (readFile) Name() string { return "read_file" }

func (readFile) Description() string {
	return "Read one bounded text window with optional line offset/limit. Output prefixes each line with its 1-based number. Any successful window observes the current file version for later structured edits. Continue with the host-issued cursor or next-window offset only when more content is useful. A cursor is bound to the current read, file version and session; do not combine it with offset or limit."
}

func (readFile) Schema() json.RawMessage {
	return json.RawMessage(`{
"type":"object",
"properties":{
  "path":{"type":"string","description":"File path"},
	"cursor":{"type":"string","description":"Host-issued continuation cursor from a prior result in this session. Pass unchanged without offset or limit; if stale, request a fresh explicit window."},
  "offset":{"type":"integer","description":"0-based line offset to start reading from (default 0)","minimum":0},
  "limit":{"type":"integer","description":"Maximum lines to return (default 2000)","minimum":1}
},
"required":["path"],
"additionalProperties":false
}`)
}

func (readFile) ReadOnly() bool { return true }

// ReadEnvelope reports what one read_file call delivered. The source identity
// comes from the store that actually served the content, the snapshot stays
// constant across the pages of one logical read, and the window digest covers
// only this page's delivered lines. read_id, result_ref and workspace_id are
// host identity the agent fills in.
func (r readFile) ReadEnvelope(ctx context.Context, args json.RawMessage, output string) (tool.ReadResultEnvelope, bool) {
	p, err := parseReadFileParams(args)
	if err != nil {
		return tool.ReadResultEnvelope{}, false
	}
	rp := resolveReadablePath(r.workDir, p.Path, r.paths)
	env := tool.ReadResultEnvelope{
		ProtocolVersion: tool.ReadResultProtocolVersion,
		Source:          tool.ReadResultSource{CanonicalPath: rp.Path},
	}
	if p.WindowGiven {
		requested := tool.ReadRange{Start: p.Offset, End: p.Offset + p.Limit}
		env.RequestedRange = &requested
	}

	// The store that served the content owns the identity: an unsaved editor
	// buffer must never be proven by the disk file's identity.
	if r.captured != nil {
		env.Source = *r.captured
	}
	env.Source.Snapshot = tool.SourceSnapshot(env.Source.Kind, rp.Path, env.Source.Identity)

	window, hasWindow := tool.ParseReadWindow(output)
	if hasWindow {
		env.DeliveredRanges = []tool.ReadRange{window.Range()}
		env.WindowDigest = tool.WindowDigest(rp.Path, window)
	}
	trailer := tool.ParseReadTrailer(output)
	env.HasMore = trailer.HasMore
	env.EOF = !trailer.HasMore
	switch {
	case trailer.LocalSafety:
		env.SourceCut = tool.ReadCutSafetyPage
	case trailer.HasMore:
		env.SourceCut = tool.ReadCutPageLimit
	}
	if env.EOF {
		if end, ok := readSourceEnd(output, window, hasWindow); ok {
			env.SourceEnd = &end
		}
	}
	if trailer.HasMore {
		env.NextCursor = tool.EncodeReadCursor(tool.ReadCursor{
			Path:      rp.Path,
			Snapshot:  env.Source.Snapshot,
			NextStart: trailer.NextOffset,
		})
	}
	return env, true
}

// overlayText mirrors Execute's overlay routing so the envelope names the same
// store that produced the delivered bytes.
func (r readFile) overlayText(ctx context.Context, rp ResolvedPath) (string, bool) {
	if r.overlay == nil || rp.External || !filepath.IsAbs(rp.Path) {
		return "", false
	}
	return r.overlay.ReadTextFile(ctx, rp.Path)
}

func digestText(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// readSourceEnd recovers the source's zero-based end line index from the
// reader's own result text: a complete window ends at its last line, and the
// empty-file / past-EOF markers state the count directly.
func readSourceEnd(output string, window tool.ReadWindow, hasWindow bool) (int, bool) {
	if hasWindow {
		return window.Range().End, true
	}
	trimmed := strings.TrimSpace(output)
	if trimmed == readFileEmptyOutput {
		return 0, true
	}
	if rest, ok := strings.CutPrefix(trimmed, "(offset "); ok {
		if _, tail, found := strings.Cut(rest, readFilePastEOFTail); found {
			if n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(tail), " lines)")); err == nil && n >= 0 {
				return n, true
			}
		}
	}
	return 0, false
}

func (r readFile) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	output, _, err := r.ExecuteRead(ctx, args)
	return output, err
}

func (r readFile) scanEncoded(f io.Reader, offset, limit int) (string, error) {

	// Peek the first 8 KiB to reject binary files cheaply (a NUL byte) before
	// reading further — keeps a multi-GB archive from being slurped just to be
	// discarded.
	peek := make([]byte, readFileBinaryPeek)
	pn, perr := io.ReadFull(f, peek)
	peek = peek[:pn]
	peekEOF := perr != nil // whole file fit in the peek (EOF / ErrUnexpectedEOF)

	// BOM check first: UTF-16 files contain 0x00 for every ASCII character, so a
	// naive NUL check would misidentify them as binary.
	switch fileenc.DetectQuick(peek) {
	case fileenc.UTF16LE, fileenc.UTF16BE:
		enc := fileenc.DetectQuick(peek)
		return r.scan(transform.NewReader(io.MultiReader(bytes.NewReader(peek), f), fileenc.Decoder(enc)), offset, limit)
	case fileenc.UTF8BOM:
		// Strip the 3-byte BOM; the content is valid UTF-8 and streams directly.
		body := peek
		if len(body) >= 3 {
			body = body[3:]
		}
		return r.scan(io.MultiReader(bytes.NewReader(body), f), offset, limit)
	}

	// BOM-less UTF-16 (Windows source files) has a NUL for every ASCII char but
	// no BOM, so it reaches here; recognise it by its NUL pattern and decode it
	// rather than rejecting it as binary.
	if k, ok := fileenc.DetectUTF16NoBOM(peek); ok {
		return r.scan(transform.NewReader(io.MultiReader(bytes.NewReader(peek), f), fileenc.Decoder(k)), offset, limit)
	}

	if bytes.IndexByte(peek, 0) >= 0 {
		return "", fmt.Errorf("binary file (NUL byte detected); use a binary inspection tool")
	}

	// Read up to a bounded sample for encoding detection, then stream the rest —
	// so a large text file isn't slurped whole just to return a few lines.
	head := peek
	if !peekEOF {
		more := make([]byte, readFileDetectSample-len(peek))
		mn, merr := io.ReadFull(f, more)
		head = append(peek, more[:mn]...)
		peekEOF = merr != nil
	}

	// When more file follows, the sample can end inside a character.
	detect := fileenc.Detect
	if !peekEOF {
		detect = fileenc.DetectFragment
	}
	enc, _ := detect(head)

	src := io.MultiReader(bytes.NewReader(head), f)
	if dec := fileenc.Decoder(enc); dec != nil {
		return r.scan(transform.NewReader(src, dec), offset, limit)
	}
	return r.scan(src, offset, limit)
}

// scan reads lines from src and returns the formatted output with line numbers.
func (r readFile) scan(src io.Reader, offset, limit int) (string, error) {
	scanner := bufio.NewScanner(src)
	scanner.Buffer(make([]byte, 0, 64*1024), readFileMaxLineBytes)

	var collected []string
	textBytes := 0
	lineNo := 0
	hasMore := false
	safetyPaged := false
	requestedEnd := offset + limit
	for scanner.Scan() {
		lineNo++
		if lineNo <= offset {
			continue
		}
		if len(collected) < limit {
			line := scanner.Text()
			count := len(collected) + 1
			width := len(strconv.Itoa(offset + count))
			nextOffset := offset + count
			bodyBytes := textBytes + len(line) + count*(width+len("→")+1)
			trailer := readFileSafetyTrailer(nextOffset, requestedEnd)
			if bodyBytes+len(trailer) > readFileMaxFormattedBytes {
				hasMore = true
				safetyPaged = true
				break
			}
			collected = append(collected, line)
			textBytes += len(line)
			continue
		}
		// A line past the requested window exists — stop here rather than reading
		// the rest of the file just to count the remainder.
		hasMore = true
		break
	}
	if err := scanner.Err(); err != nil {
		if strings.Contains(err.Error(), "token too long") {
			return "", fmt.Errorf("scan: source line exceeds the 1 MiB local safety limit: %w", err)
		}
		return "", fmt.Errorf("scan: %w", err)
	}

	if lineNo == 0 {
		return readFileEmptyOutput, nil
	}
	if len(collected) == 0 {
		return fmt.Sprintf("(offset %d%s%d lines)", offset, readFilePastEOFTail, lineNo), nil
	}

	maxShown := offset + len(collected)
	w := len(fmt.Sprint(maxShown))

	var b strings.Builder
	for i, line := range collected {
		fmt.Fprintf(&b, "%*d→%s\n", w, offset+i+1, line)
	}
	if safetyPaged {
		b.WriteString(readFileSafetyTrailer(offset+len(collected), requestedEnd))
	} else if hasMore {
		fmt.Fprintf(&b, "\n[PARTIAL view: showing lines %d-%d of at least %d; pass offset=%d to continue. A partial window may be sufficient for local work.]\n", offset+1, maxShown, lineNo, maxShown)
	}
	return b.String(), nil
}

func readFileSafetyTrailer(nextOffset, requestedEnd int) string {
	return fmt.Sprintf("\n[read_file local safety page; next_offset=%d requested_end=%d]\n", nextOffset, requestedEnd)
}
