// Command testdup reports duplicated Go test bodies and tests that only
// re-exercise a shared helper through a one-line forwarding wrapper whose own
// package already covers the wrapped function.
//
// The -strict flag gates on exact duplicate bodies (excluding the per-package
// TestMain boilerplate); the wrapper and -sim sections are advisory.
//
// Usage: go run ./tools/testdup [-strict] [-sim 0.9] [-root .]
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type testFunc struct {
	file      string
	pkgDir    string
	name      string
	hash      string
	body      string
	lines     int
	buildTag  bool
	bareCalls map[string]bool
}

type forwarder struct {
	pkgDir string
	name   string
	target string // import path of the forwarded package
	callee string // forwarded function name
}

func main() {
	strict := flag.Bool("strict", false, "exit 1 when exact duplicate test bodies exist")
	sim := flag.Float64("sim", 0, "report same-package near-duplicate pairs at this Jaccard similarity (0 disables)")
	root := flag.String("root", ".", "repository root to scan")
	flag.Parse()

	files := collect(*root)
	fset := token.NewFileSet()
	var tests []*testFunc
	var forwarders []*forwarder
	for _, file := range files {
		fileTests, fileForwarders := scanFile(file, fset)
		tests = append(tests, fileTests...)
		forwarders = append(forwarders, fileForwarders...)
	}

	fmt.Printf("scanned %d files, %d test functions\n\n", len(files), len(tests))

	exactCount := reportExactDuplicates(tests)
	reportWrapperTests(tests, forwarders)
	if *sim > 0 {
		reportNearDuplicates(tests, *sim)
	}
	if *strict && exactCount > 0 {
		os.Exit(1)
	}
}

// scanFile extracts test functions and forwarding wrappers from one file.
func scanFile(file string, fset *token.FileSet) ([]*testFunc, []*forwarder) {
	src, err := os.ReadFile(file)
	if err != nil {
		return nil, nil
	}
	parsed, err := parser.ParseFile(fset, file, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, nil
	}
	dir := filepath.Dir(file)
	imports := importAliases(parsed)
	isTest := strings.HasSuffix(file, "_test.go")
	var tests []*testFunc
	var forwarders []*forwarder
	for _, decl := range parsed.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil || fn.Recv != nil {
			continue
		}
		if isTest {
			if tf := newTestFunc(file, dir, fn, fset, src); tf != nil {
				tests = append(tests, tf)
			}
			continue
		}
		if fw := newForwarder(dir, fn, imports); fw != nil {
			forwarders = append(forwarders, fw)
		}
	}
	return tests, forwarders
}

func importAliases(parsed *ast.File) map[string]string {
	imports := map[string]string{}
	for _, imp := range parsed.Imports {
		p := strings.Trim(imp.Path.Value, `"`)
		name := filepath.Base(p)
		if imp.Name != nil {
			name = imp.Name.Name
		}
		imports[name] = p
	}
	return imports
}

func newTestFunc(file, dir string, fn *ast.FuncDecl, fset *token.FileSet, src []byte) *testFunc {
	if !isTestName(fn.Name.Name) {
		return nil
	}
	start := fset.Position(fn.Body.Lbrace).Offset
	end := fset.Position(fn.Body.Rbrace).Offset + 1
	if start < 0 || end > len(src) || start >= end {
		return nil
	}
	body := normalize(src[start:end])
	sum := sha256.Sum256([]byte(body))
	tf := &testFunc{
		file:      file,
		pkgDir:    dir,
		name:      fn.Name.Name,
		hash:      hex.EncodeToString(sum[:8]),
		body:      body,
		lines:     strings.Count(string(src[start:end]), "\n") + 1,
		buildTag:  hasBuildTag(src),
		bareCalls: map[string]bool{},
	}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if id, ok := bareCallName(n); ok {
			tf.bareCalls[id] = true
		}
		return true
	})
	return tf
}

// newForwarder reports a function whose whole body forwards to another package.
func newForwarder(dir string, fn *ast.FuncDecl, imports map[string]string) *forwarder {
	if len(fn.Body.List) != 1 {
		return nil
	}
	var call *ast.CallExpr
	switch st := fn.Body.List[0].(type) {
	case *ast.ReturnStmt:
		if len(st.Results) == 1 {
			call, _ = st.Results[0].(*ast.CallExpr)
		}
	case *ast.ExprStmt:
		call, _ = st.X.(*ast.CallExpr)
	}
	sel, ok := callFun(call)
	if !ok {
		return nil
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok {
		return nil
	}
	p, ok := imports[id.Name]
	if !ok || !strings.HasPrefix(p, "deepzora/") {
		return nil
	}
	return &forwarder{pkgDir: dir, name: fn.Name.Name, target: p, callee: sel.Sel.Name}
}

func bareCallName(n ast.Node) (string, bool) {
	ce, ok := n.(*ast.CallExpr)
	if !ok {
		return "", false
	}
	id, ok := ce.Fun.(*ast.Ident)
	if !ok {
		return "", false
	}
	return id.Name, true
}

func isTestName(name string) bool {
	if name == "TestMain" { // TestMain is per-package boilerplate, by design
		return false
	}
	return strings.HasPrefix(name, "Test") || strings.HasPrefix(name, "Benchmark") || strings.HasPrefix(name, "Fuzz")
}

// reportExactDuplicates prints body-identical tests and returns the count of
// redundant copies (all but the first in each group).
func reportExactDuplicates(tests []*testFunc) int {
	byHash := map[string][]*testFunc{}
	for _, t := range tests {
		byHash[t.hash] = append(byHash[t.hash], t)
	}
	var groups [][]*testFunc
	for _, g := range byHash {
		if len(g) > 1 {
			groups = append(groups, g)
		}
	}
	sort.Slice(groups, func(i, j int) bool { return len(groups[i]) > len(groups[j]) })
	fmt.Println("== exact duplicate test bodies (excluding TestMain) ==")
	if len(groups) == 0 {
		fmt.Println("none")
		fmt.Println()
		return 0
	}
	total := 0
	for _, g := range groups {
		sort.Slice(g, func(i, j int) bool { return g[i].file < g[j].file })
		platform := ""
		if isPlatformOnlyDuplicate(g) {
			platform = " [build-tagged duplicate of an untagged test]"
		}
		fmt.Printf("\n[%d copies, %d lines each]%s\n", len(g), g[0].lines, platform)
		for _, t := range g {
			fmt.Printf("  %s:%s\n", t.file, t.name)
		}
		total += len(g) - 1
	}
	fmt.Printf("\nredundant copies: %d\n\n", total)
	return total
}

func isPlatformOnlyDuplicate(g []*testFunc) bool {
	tagged, untagged := 0, 0
	for _, t := range g {
		if t.buildTag {
			tagged++
		} else {
			untagged++
		}
	}
	return tagged > 0 && untagged == 1
}

// reportWrapperTests lists tests that only exercise a one-line forwarder while
// the forwarded package tests the same function itself.
func reportWrapperTests(tests []*testFunc, forwarders []*forwarder) {
	ownTests := map[string][]string{} // pkgDir -> test names
	testsByDir := map[string][]*testFunc{}
	for _, t := range tests {
		ownTests[t.pkgDir] = append(ownTests[t.pkgDir], t.name)
		testsByDir[t.pkgDir] = append(testsByDir[t.pkgDir], t)
	}
	dirOfImport := func(imp string) string { return strings.TrimPrefix(imp, "deepzora/") }

	fmt.Println("== wrapper tests (advisory) ==")
	found := 0
	for _, fw := range forwarders {
		defDir := dirOfImport(fw.target)
		var wrapperTests []string
		for _, t := range testsByDir[fw.pkgDir] {
			if t.bareCalls[fw.name] {
				wrapperTests = append(wrapperTests, t.file+":"+t.name)
			}
		}
		if len(wrapperTests) == 0 {
			continue
		}
		covered := false
		for _, name := range ownTests[defDir] {
			if strings.Contains(name, fw.callee) {
				covered = true
				break
			}
		}
		if !covered {
			continue
		}
		found++
		fmt.Printf("\n%s:%s -> %s.%s (already covered in %s)\n", fw.pkgDir, fw.name, defDir, fw.callee, defDir)
		for _, w := range wrapperTests {
			fmt.Printf("  %s\n", w)
		}
	}
	if found == 0 {
		fmt.Println("none")
	}
	fmt.Printf("\nwrapper groups: %d (advisory; review before deleting)\n\n", found)
}

func reportNearDuplicates(tests []*testFunc, threshold float64) {
	fmt.Printf("== near-duplicate pairs within a package (>= %.2f, advisory) ==\n", threshold)
	type entry struct {
		t     *testFunc
		grams map[uint64]bool
	}
	byDir := map[string][]*entry{}
	for _, t := range tests {
		if len(strings.Fields(t.body)) < 30 {
			continue
		}
		byDir[t.pkgDir] = append(byDir[t.pkgDir], &entry{t: t, grams: shingles(t.body, 5)})
	}
	pairs := 0
	for _, entries := range byDir {
		for i := range entries {
			for j := i + 1; j < len(entries); j++ {
				a, b := entries[i], entries[j]
				if a.t.hash == b.t.hash {
					continue // exact duplicates are reported separately
				}
				inter := 0
				for h := range a.grams {
					if b.grams[h] {
						inter++
					}
				}
				union := len(a.grams) + len(b.grams) - inter
				if union == 0 {
					continue
				}
				if s := float64(inter) / float64(union); s >= threshold {
					fmt.Printf("  %.3f  %s:%s\n         %s:%s\n", s, a.t.file, a.t.name, b.t.file, b.t.name)
					pairs++
				}
			}
		}
	}
	fmt.Printf("near-duplicate pairs: %d\n", pairs)
}

func callFun(call *ast.CallExpr) (*ast.SelectorExpr, bool) {
	if call == nil {
		return nil, false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil, false
	}
	if _, ok := sel.X.(*ast.Ident); !ok {
		return nil, false
	}
	return sel, true
}

func collect(root string) []string {
	var files []string
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			switch filepath.Base(path) {
			case ".git", "node_modules", ".worktrees", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") {
			files = append(files, path)
		}
		return nil
	})
	return files
}

func hasBuildTag(src []byte) bool {
	head := string(src)
	if i := strings.Index(head, "package "); i > 0 {
		head = head[:i]
	}
	return strings.Contains(head, "//go:build")
}

// normalize strips comments and collapses whitespace so logically identical
// bodies compare equal regardless of formatting.
func normalize(src []byte) string {
	var s scanner.Scanner
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(src))
	s.Init(file, src, nil, 0)
	var out strings.Builder
	for {
		_, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		if tok == token.COMMENT {
			continue
		}
		if lit != "" {
			out.WriteString(lit)
		} else {
			out.WriteString(tok.String())
		}
		out.WriteString(" ")
	}
	return strings.Join(strings.Fields(out.String()), " ")
}

func shingles(body string, n int) map[uint64]bool {
	toks := strings.Fields(body)
	set := map[uint64]bool{}
	if len(toks) < n {
		if len(toks) > 0 {
			set[hashTokens(toks)] = true
		}
		return set
	}
	for i := 0; i+n <= len(toks); i++ {
		set[hashTokens(toks[i:i+n])] = true
	}
	return set
}

func hashTokens(toks []string) uint64 {
	var h uint64 = 14695981039346656037
	for _, tok := range toks {
		for i := range len(tok) {
			h ^= uint64(tok[i])
			h *= 1099511628211
		}
		h ^= 0x20
		h *= 1099511628211
	}
	return h
}
