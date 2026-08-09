package server

import (
	"bufio"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Each Control row in docs/security-advisories.md names the regression that
// exercises it. Removed rows are inapplicable because ZenFM has no corresponding
// multi-user/JWT/proxy/hook/command surface.
func TestAdvisoryControlInventoryHasRegressionCoverage(t *testing.T) {
	regressions := map[string]string{
		"GHSA-m9f5-2232-frp6": "TestGHSA_m9f5ExpiredUploadCleanupIsRooted",
		"GHSA-ffv3-7h97-993q": "TestGHSA_ffv3DeclaredLengthOffsetAndAtomicTUSFlow",
		"GHSA-833g-cqhp-h72j": "TestPasswordProtectedShareAndRevocation",
		"GHSA-83xp-526h-j3ww": "TestGHSA_83xpArchiveNamesAndStreamingFormats",
		"GHSA-8wc8-hf36-mjh9": "TestSymlinkCannotEscapeRoot",
		"GHSA-pp88-jhwj-5qh5": "TestGHSA_pp88MoveRevokesDescendantShares",
		"GHSA-fmm7-x4gx-8jhr": "TestGHSA_m9f5ExpiredUploadCleanupIsRooted",
		"GHSA-gxjx-7m74-hcq8": "TestGHSA_gxjxArchiveNameValidationAndCancellation",
		"GHSA-3q2p-72cj-682c": "TestPasswordProtectedShareAndRevocation",
		"GHSA-239w-m3h6-ch8v": "TestOpenRegularResistsSymlinkSwap",
		"GHSA-v7vv-5wj2-gfcj": "TestPasswordReplacementRevokesCredentials",
		"GHSA-5vpr-4fgw-f69h": "TestGHSA_5vprHTMLAndEPUBActiveContentIsRemoved",
		"GHSA-9f3r-2vgw-m8xp": "TestFileWorkflowAndTraversalRegression",
		"GHSA-79pf-vx4x-7jmm": "TestGHSA_79pfUploadDeletionRequiresCSRFAndActiveLimit",
		"GHSA-68j5-4m99-w9w9": "TestPasswordProtectedShareAndRevocation",
		"GHSA-mr74-928f-rw69": "TestPasswordProtectedShareAndRevocation",
		"GHSA-hxw8-4h9j-hq2r": "TestCSRFOriginAndPersonalTokenScope",
		"GHSA-4mh3-h929-w968": "TestGHSA_4mh3RepeatedLeadingSlashRejected",
		"GHSA-43mm-m3h2-3prc": "TestGHSA_43mmUniformLoginFailureAndDistributedAccountLimit",
		"GHSA-6jqf-mv7m-3q7p": "ci:govulncheck",
		"GHSA-6cqf-cfhv-659g": "TestDeleteShareRevokesPublicSessions",
		"GHSA-w5fm-68j4-fpc4": "TestGHSA_43mmUniformLoginFailureAndDistributedAccountLimit",
		"GHSA-7xwp-2cpp-p8r7": "TestSessionIdleAndAbsoluteExpiry",
		"GHSA-7xqm-7738-642x": "TestGHSA_7xqmPreviewInputsAndDimensionsAreBounded",
		"GHSA-rmwh-g367-mj4x": "TestCSRFOriginAndPersonalTokenScope",
		"GHSA-3v48-283x-f2w4": "TestPasswordProtectedShareAndRevocation",
		"GHSA-cm2r-rg7r-p7gg": "TestPasswordRoundTrip",
		"GHSA-jj2r-455p-5gvf": "TestGHSA_jj2rCopiedEntriesAreOwnerOnlyWithPermissiveUmask",
		"GHSA-4wx8-5gm2-2j97": "frontend:renders Markdown while escaping raw HTML and unsafe links",
	}
	available := regressionIdentifiers(t)
	file, err := os.Open(filepath.Join("..", "..", "docs", "security-advisories.md"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	found := 0
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), "|")
		if len(fields) < 4 || strings.TrimSpace(fields[2]) != "Control" {
			continue
		}
		advisory := strings.TrimSpace(fields[1])
		regression := regressions[advisory]
		if regression == "" {
			t.Errorf("%s has no named regression", advisory)
		} else if !available[regression] {
			t.Errorf("%s names missing regression %q", advisory, regression)
		}
		found++
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if found != len(regressions) {
		t.Fatalf("control inventory has %d rows, regression map has %d", found, len(regressions))
	}
}

func regressionIdentifiers(t *testing.T) map[string]bool {
	t.Helper()
	root := filepath.Join("..", "..")
	identifiers := make(map[string]bool)
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", ".build", ".gradle", ".toolchains", "build", "dist", "node_modules", "test-results":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, "_test.go") {
			return nil
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			return err
		}
		for _, declaration := range parsed.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if ok && strings.HasPrefix(function.Name.Name, "Test") {
				identifiers[function.Name.Name] = true
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	markdownTest, err := os.ReadFile(filepath.Join(root, "frontend", "src", "components", "FileDialogs.test.tsx"))
	if err != nil {
		t.Fatal(err)
	}
	const markdownMarker = "renders Markdown while escaping raw HTML and unsafe links"
	identifiers["frontend:"+markdownMarker] = strings.Contains(string(markdownTest), markdownMarker)
	securityWorkflow, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "security.yml"))
	if err != nil {
		t.Fatal(err)
	}
	identifiers["ci:govulncheck"] = strings.Contains(string(securityWorkflow), "govulncheck")
	return identifiers
}
