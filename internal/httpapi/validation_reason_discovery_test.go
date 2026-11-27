package httpapi

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// emittedReason is a stable API reason string plus the HTTP/error code category
// under which the client looks up errors.<code>.<reason> in en.json.
type emittedReason struct {
	Reason string
	Code   string // VALIDATION_ERROR | CONFLICT | PAYLOAD_TOO_LARGE | INTERNAL
}

const (
	codeValidation   = "VALIDATION_ERROR"
	codeConflict     = "CONFLICT"
	codePayloadLarge = "PAYLOAD_TOO_LARGE"
	codeInternal     = "INTERNAL"
)

// knownConflictReasons covers store Reason* values that are conflict-shaped even
// when AST usage scanning cannot see a base error (e.g. only defined). Prefer
// usage-based classification when available.
var knownConflictReasons = map[string]bool{
	"already_member":                true,
	"cannot_demote_last_maintainer": true,
	"cannot_demote_self":            true,
	"custom_field_key_exists":       true,
	"email_in_use":                  true,
	"owns_projects":                 true,
	"priority_tier_in_use":          true,
	"sole_maintainer":               true,
	"todo_archived":                 true,
	"wall_drawing_version_conflict": true,
}

// knownInternalReasons covers store Reason* values surfaced as INTERNAL with a
// redacted client message (no SQL/paths). Prefer usage-based classification when
// available.
var knownInternalReasons = map[string]bool{
	"import_internal_redacted": true,
}

func discoverEmittedReasons(t testing.TB) []emittedReason {
	t.Helper()
	byKey := map[string]emittedReason{} // reason\x00code → entry
	add := func(reason, code string) {
		if reason == "" || !isSnakeReason(reason) {
			return
		}
		key := reason + "\x00" + code
		byKey[key] = emittedReason{Reason: reason, Code: code}
	}

	for _, r := range validationReasonByMessage {
		add(r, codeValidation)
	}
	for _, p := range validationReasonPrefixes {
		add(p.reason, codeValidation)
	}

	httpDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for _, reason := range discoverWriteValidationErrorReasons(t, httpDir) {
		add(reason, codeValidation)
	}
	// Scan writeError(..., "PAYLOAD_TOO_LARGE", ..., validationDetails("reason")) calls.
	for _, reason := range discoverWriteErrorReasonsWithCode(t, httpDir, codePayloadLarge) {
		add(reason, codePayloadLarge)
	}

	storeDir := filepath.Join(httpDir, "..", "store")
	for _, er := range discoverStoreReasonConstants(t, storeDir) {
		add(er.Reason, er.Code)
	}

	out := make([]emittedReason, 0, len(byKey))
	for _, er := range byKey {
		out = append(out, er)
	}
	return out
}

func isSnakeReason(s string) bool {
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' {
			return false
		}
	}
	return true
}

// discoverWriteErrorReasonsWithCode walks non-test *.go files in dir and
// collects the first argument of validationDetails(...) when it appears as the
// 5th argument of a writeError(..., code, ...) call whose 3rd argument matches
// codeFilter. Covers PAYLOAD_TOO_LARGE and similar patterns.
//
// Limitation: only direct validationDetails("literal", ...) calls are resolved;
// indirect patterns are not discovered here.
func discoverWriteErrorReasonsWithCode(t testing.TB, dir string, codeFilter string) []string {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(info os.FileInfo) bool {
		name := info.Name()
		return strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse httpapi for writeError: %v", err)
	}
	var reasons []string
	for _, pkg := range pkgs {
		for _, r := range discoverWriteErrorReasonsWithCodeFromPackage(pkg, codeFilter) {
			reasons = append(reasons, r)
		}
	}
	return reasons
}

// discoverWriteErrorReasonsWithCodeFromPackage scans writeError calls in pkg
// whose 3rd argument (HTTP code) matches codeFilter and whose 5th argument is
// validationDetails(reason, ...) — extracts the reason string.
func discoverWriteErrorReasonsWithCodeFromPackage(pkg *ast.Package, codeFilter string) []string {
	consts := packageStringConsts(pkg)
	var reasons []string
	for _, file := range pkg.Files {
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if !isIdentName(call.Fun, "writeError") {
				return true
			}
			// writeError(w, status, code, message, details)
			if len(call.Args) < 5 {
				return true
			}
			// 3rd arg (index 2) is the error code string.
			code, ok := resolveStringArg(call.Args[2], consts)
			if !ok || code != codeFilter {
				return true
			}
			// 5th arg (index 4) is the details — look for validationDetails(reason, ...).
			detailsCall, ok := call.Args[4].(*ast.CallExpr)
			if !ok {
				return true
			}
			if !isIdentName(detailsCall.Fun, "validationDetails") {
				return true
			}
			if len(detailsCall.Args) < 1 {
				return true
			}
			if reason, ok := resolveStringArg(detailsCall.Args[0], consts); ok {
				reasons = append(reasons, reason)
			}
			return true
		})
	}
	return reasons
}

// discoverWriteValidationErrorReasons walks non-test *.go files in dir and
// collects the reason argument of writeValidationError calls when it is a
// string literal or a same-package named constant whose value is a string
// literal.
func discoverWriteValidationErrorReasons(t testing.TB, dir string) []string {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(info os.FileInfo) bool {
		name := info.Name()
		return strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse httpapi: %v", err)
	}
	var reasons []string
	for _, pkg := range pkgs {
		reasons = append(reasons, discoverWriteValidationErrorReasonsFromPackage(pkg)...)
	}
	return reasons
}

// discoverStoreReasonConstants finds const Reason* = "snake_case" in non-test
// store packages and classifies each as CONFLICT, VALIDATION_ERROR, or INTERNAL
// from reasoned/priorityError/reasonedWithDetails/importValidation/
// drawingConflict/drawingError/reasonedStoreError usage (plus known maps).
func discoverStoreReasonConstants(t testing.TB, dir string) []emittedReason {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(info os.FileInfo) bool {
		name := info.Name()
		return strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse store: %v", err)
	}
	var out []emittedReason
	for _, pkg := range pkgs {
		out = append(out, discoverStoreReasonsFromPackage(pkg)...)
	}
	return out
}

func classifyReasonCall(call *ast.CallExpr, mark func(name, code string)) {
	funName := ""
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		funName = fun.Name
	default:
		return
	}
	switch funName {
	case "reasoned", "priorityError", "reasonedWithDetails":
		if len(call.Args) < 2 {
			return
		}
		base := identName(call.Args[0])
		reasonName := identName(call.Args[1])
		if reasonName == "" {
			return
		}
		switch base {
		case "ErrConflict":
			mark(reasonName, codeConflict)
		case "ErrValidation":
			mark(reasonName, codeValidation)
		}
	case "importValidation":
		if len(call.Args) < 1 {
			return
		}
		if name := identName(call.Args[0]); name != "" {
			mark(name, codeValidation)
		}
	case "drawingConflict":
		if len(call.Args) < 1 {
			return
		}
		if name := identName(call.Args[0]); name != "" {
			mark(name, codeConflict)
		}
	case "drawingError":
		if len(call.Args) < 1 {
			return
		}
		if name := identName(call.Args[0]); name != "" {
			mark(name, codeValidation)
		}
	}
}

func classifyReasonedStoreErrorLit(lit *ast.CompositeLit, mark func(name, code string)) {
	typeName := ""
	switch t := lit.Type.(type) {
	case *ast.Ident:
		typeName = t.Name
	case *ast.SelectorExpr:
		typeName = t.Sel.Name
	}
	if typeName != "reasonedStoreError" {
		return
	}
	var base, reason string
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key := identName(kv.Key)
		switch key {
		case "base":
			base = identName(kv.Value)
		case "reason":
			reason = identName(kv.Value)
		}
	}
	if reason == "" {
		return
	}
	switch base {
	case "ErrConflict":
		mark(reason, codeConflict)
	case "ErrValidation":
		mark(reason, codeValidation)
	}
}

func packageStringConsts(pkg *ast.Package) map[string]string {
	out := map[string]string{}
	for _, file := range pkg.Files {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range vs.Names {
					var val ast.Expr
					if i < len(vs.Values) {
						val = vs.Values[i]
					} else if len(vs.Values) == 1 {
						val = vs.Values[0]
					}
					if lit, ok := stringLiteral(val); ok {
						out[name.Name] = lit
					}
				}
			}
		}
	}
	return out
}

func resolveStringArg(expr ast.Expr, consts map[string]string) (string, bool) {
	if lit, ok := stringLiteral(expr); ok {
		return lit, true
	}
	if name := identName(expr); name != "" {
		if v, ok := consts[name]; ok {
			return v, true
		}
	}
	return "", false
}

func stringLiteral(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return s, true
}

func identName(expr ast.Expr) string {
	id, ok := expr.(*ast.Ident)
	if !ok {
		return ""
	}
	return id.Name
}

func isIdentName(expr ast.Expr, name string) bool {
	return identName(expr) == name
}

// parseFixturePackage parses all .go files under dir as a single package named
// "fixture" (files may declare package fixture). Used by discovery unit tests.
func parseFixtureFiles(t *testing.T, dir string) *ast.Package {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(info os.FileInfo) bool {
		return strings.HasSuffix(info.Name(), ".go")
	}, 0)
	if err != nil {
		t.Fatalf("parse fixture %s: %v", dir, err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("fixture %s: want 1 package, got %d", dir, len(pkgs))
	}
	for _, pkg := range pkgs {
		return pkg
	}
	return nil
}

func discoverWriteValidationErrorReasonsFromPackage(pkg *ast.Package) []string {
	consts := packageStringConsts(pkg)
	var reasons []string
	for _, file := range pkg.Files {
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if !isIdentName(call.Fun, "writeValidationError") {
				return true
			}
			if len(call.Args) < 3 {
				return true
			}
			if reason, ok := resolveStringArg(call.Args[2], consts); ok {
				reasons = append(reasons, reason)
			}
			return true
		})
	}
	return reasons
}

func discoverStoreReasonsFromPackage(pkg *ast.Package) []emittedReason {
	// Shared by production store scans and fixture unit tests.
	reasonConsts := map[string]string{}
	for _, file := range pkg.Files {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range vs.Names {
					if !strings.HasPrefix(name.Name, "Reason") {
						continue
					}
					var val ast.Expr
					if i < len(vs.Values) {
						val = vs.Values[i]
					} else if len(vs.Values) == 1 {
						val = vs.Values[0]
					}
					lit, ok := stringLiteral(val)
					if !ok || !isSnakeReason(lit) {
						continue
					}
					reasonConsts[name.Name] = lit
				}
			}
		}
	}
	conflictUse := map[string]bool{}
	validationUse := map[string]bool{}
	mark := func(name, code string) {
		if _, ok := reasonConsts[name]; !ok {
			return
		}
		switch code {
		case codeConflict:
			conflictUse[name] = true
		case codeValidation:
			validationUse[name] = true
		}
	}
	for _, file := range pkg.Files {
		ast.Inspect(file, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.CallExpr:
				classifyReasonCall(node, mark)
			case *ast.CompositeLit:
				classifyReasonedStoreErrorLit(node, mark)
			}
			return true
		})
	}
	out := make([]emittedReason, 0, len(reasonConsts))
	for name, value := range reasonConsts {
		for _, code := range classifyStoreReasonCodes(name, value, conflictUse, validationUse) {
			out = append(out, emittedReason{Reason: value, Code: code})
		}
	}
	return out
}

func classifyStoreReasonCode(name, value string, conflictUse, validationUse map[string]bool) string {
	switch {
	case conflictUse[name] && !validationUse[name]:
		return codeConflict
	case validationUse[name] && !conflictUse[name]:
		return codeValidation
	case conflictUse[name] && validationUse[name]:
		// Caller should emit both categories via classifyStoreReasonCodes.
		return codeConflict
	case knownInternalReasons[value]:
		return codeInternal
	case knownConflictReasons[value]:
		return codeConflict
	default:
		return codeValidation
	}
}

// classifyStoreReasonCodes returns every category a Reason* constant is observed under.
func classifyStoreReasonCodes(name, value string, conflictUse, validationUse map[string]bool) []string {
	if conflictUse[name] && validationUse[name] {
		return []string{codeConflict, codeValidation}
	}
	return []string{classifyStoreReasonCode(name, value, conflictUse, validationUse)}
}

func TestValidationReasonDiscovery_WriteValidationErrorPatterns(t *testing.T) {
	dir := filepath.Join("testdata", "validation_reason_discovery", "write_validation_error")
	pkg := parseFixtureFiles(t, dir)
	got := map[string]bool{}
	for _, r := range discoverWriteValidationErrorReasonsFromPackage(pkg) {
		got[r] = true
	}
	for _, want := range []string{
		"single_line_reason",
		"multiline_reason",
		"named_const_reason",
	} {
		if !got[want] {
			t.Errorf("missing discovered reason %q from writeValidationError fixtures; got %v", want, keys(got))
		}
	}
	if got["skipped_dynamic_reason"] {
		t.Errorf("must not treat non-literal/non-const reason args as discovered")
	}
}

func TestValidationReasonDiscovery_WriteErrorPayloadTooLargePatterns(t *testing.T) {
	dir := filepath.Join("testdata", "validation_reason_discovery", "write_error_payload")
	pkg := parseFixtureFiles(t, dir)
	got := map[string]bool{}
	for _, r := range discoverWriteErrorReasonsWithCodeFromPackage(pkg, codePayloadLarge) {
		got[r] = true
	}
	for _, want := range []string{
		"literal_payload_reason",
		"named_payload_reason",
	} {
		if !got[want] {
			t.Errorf("missing discovered PAYLOAD_TOO_LARGE reason %q; got %v", want, keys(got))
		}
	}
	if got["skipped_dynamic_reason"] {
		t.Errorf("must not treat non-literal/non-const reason args as discovered for PAYLOAD_TOO_LARGE")
	}
}

func TestValidationReasonDiscovery_StoreReasonCategories(t *testing.T) {
	dir := filepath.Join("testdata", "validation_reason_discovery", "store_reasons")
	pkg := parseFixtureFiles(t, dir)
	byKey := map[string]bool{}
	for _, er := range discoverStoreReasonsFromPackage(pkg) {
		byKey[er.Reason+"@"+er.Code] = true
	}
	want := []emittedReason{
		{Reason: "email_in_use", Code: codeConflict},
		{Reason: "custom_field_key_exists", Code: codeConflict},
		{Reason: "invalid_due_at", Code: codeValidation},
		{Reason: "wall_drawing_version_conflict", Code: codeConflict},
		{Reason: "todo_archived", Code: codeConflict},
		{Reason: "only_defined_validation", Code: codeValidation},
		{Reason: "owns_projects", Code: codeConflict},            // unused const; known conflict value
		{Reason: "import_internal_redacted", Code: codeInternal}, // unused const; known INTERNAL value
		{Reason: "fixture_dual_category", Code: codeConflict},
		{Reason: "fixture_dual_category", Code: codeValidation},
	}
	for _, er := range want {
		if !byKey[er.Reason+"@"+er.Code] {
			t.Errorf("missing discovered %s.%s (map=%v)", er.Code, er.Reason, byKey)
		}
	}
}

func TestValidationReasonDiscovery_MissingAndWrongCategory(t *testing.T) {
	// Synthetic locale map: proves the shared localeCoverageHasKey helper
	// distinguishes missing keys and wrong categories (reason only under
	// CONFLICT when VALIDATION_ERROR is expected).
	en := map[string]string{
		"errors.VALIDATION_ERROR.ok_reason":        "ok",
		"errors.CONFLICT.conflict_only":            "conflict",
		"errors.PAYLOAD_TOO_LARGE.too_big":         "big",
		"errors.INTERNAL.import_internal_redacted": "redacted",
	}
	cases := []struct {
		name    string
		reason  string
		code    string
		wantHas bool
	}{
		{"present under expected category", "ok_reason", codeValidation, true},
		{"missing entirely", "never_emitted", codeValidation, false},
		{"wrong category (CONFLICT only, expect VALIDATION)", "conflict_only", codeValidation, false},
		{"correct CONFLICT category", "conflict_only", codeConflict, true},
		{"PAYLOAD category", "too_big", codePayloadLarge, true},
		{"PAYLOAD reason under VALIDATION fails", "too_big", codeValidation, false},
		{"INTERNAL category", "import_internal_redacted", codeInternal, true},
		{"INTERNAL reason under VALIDATION fails", "import_internal_redacted", codeValidation, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := localeCoverageHasKey(en, tc.reason, tc.code); got != tc.wantHas {
				t.Fatalf("localeCoverageHasKey(%q,%q)=%v want %v", tc.reason, tc.code, got, tc.wantHas)
			}
		})
	}
}

// localeCoverageHasKey reports whether catalog contains errors.<code>.<reason>.
// Shared by discovery unit tests and the English locale ratchet.
func localeCoverageHasKey(catalog map[string]string, reason, code string) bool {
	_, ok := catalog["errors."+code+"."+reason]
	return ok
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
