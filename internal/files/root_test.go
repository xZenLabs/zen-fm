package files

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func testRoot(t *testing.T, opts Options) (*Root, string) {
	t.Helper()
	dir := t.TempDir()
	r, err := Open(dir, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r, dir
}

func TestNormalizeRejectsTraversalAndAmbiguousPaths(t *testing.T) {
	for _, value := range []string{"../secret", "a/../secret", "a//b", "a/./b", "C:\\secret", "a\x00b"} {
		if _, err := Normalize(value); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("Normalize(%q) = %v", value, err)
		}
	}
	for input, want := range map[string]string{"": ".", "/": ".", "/books/a.epub": "books/a.epub"} {
		if got, err := Normalize(input); err != nil || got != want {
			t.Errorf("Normalize(%q) = %q, %v", input, got, err)
		}
	}
}

func TestOpenCanonicalizesConfiguredRootSymlink(t *testing.T) {
	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "root-link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	r, err := Open(link, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	if r.Name() != resolved || r.Advanced() {
		t.Fatalf("canonical root = %q, advanced=%v; want %q", r.Name(), r.Advanced(), resolved)
	}
}

func TestRootSymlinkToLiteralRootEnablesAdvancedMode(t *testing.T) {
	link := filepath.Join(t.TempDir(), "literal-root")
	if err := os.Symlink(string(os.PathSeparator), link); err != nil {
		t.Fatal(err)
	}
	r, err := Open(link, Options{})
	if err != nil {
		t.Skipf("literal root unavailable: %v", err)
	}
	defer r.Close()
	if r.Name() != string(os.PathSeparator) || !r.Advanced() {
		t.Fatalf("root symlink bypassed advanced classification: %q %v", r.Name(), r.Advanced())
	}
}

func TestCRUDSearchCopyAndChecksum(t *testing.T) {
	r, _ := testRoot(t, Options{})
	if _, err := r.Mkdir("books"); err != nil {
		t.Fatal(err)
	}
	entry, err := r.Write("books/Hello.txt", strings.NewReader("zen"), false)
	if err != nil || entry.Size != 3 {
		t.Fatalf("write: %+v %v", entry, err)
	}
	if _, err := r.Write("books/Hello.txt", strings.NewReader("again"), false); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}
	data, err := r.ReadContent("books/Hello.txt")
	if err != nil || string(data) != "zen" {
		t.Fatalf("read: %q %v", data, err)
	}
	result, err := r.Search(context.Background(), "/", "hello", true, 10)
	if err != nil || len(result.Entries) != 1 {
		t.Fatalf("search: %+v %v", result, err)
	}
	if _, err := r.Copy(context.Background(), "books", "backup", false); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Move("backup/Hello.txt", "backup/Renamed.txt", false); err != nil {
		t.Fatal(err)
	}
	digest, err := r.Checksum(context.Background(), "backup/Renamed.txt")
	if err != nil || digest != "92e4accd3d41d63fe7698c06c990e5a78916d6dfe3f6d498e46d9db1128c2ac9" {
		t.Fatalf("checksum: %q %v", digest, err)
	}
	if err := r.Delete("backup", true); err != nil {
		t.Fatal(err)
	}
}

func TestCopyAndMoveCanExplicitlyReplaceDirectories(t *testing.T) {
	r, directory := testRoot(t, Options{})
	for _, name := range []string{"copy-source", "copy-target", "move-source", "move-target"} {
		if _, err := r.Mkdir(name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.Write("copy-source/new.txt", strings.NewReader("copied"), false); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Write("copy-target/old.txt", strings.NewReader("old"), false); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Copy(context.Background(), "copy-source", "copy-target", false); !errors.Is(err, ErrConflict) {
		t.Fatalf("non-overwrite copy returned %v", err)
	}
	if _, err := r.Copy(context.Background(), "copy-source", "copy-target", true); err != nil {
		t.Fatal(err)
	}
	if data, err := r.ReadContent("copy-target/new.txt"); err != nil || string(data) != "copied" {
		t.Fatalf("copied replacement = %q, %v", data, err)
	}
	if _, err := r.Entry("copy-target/old.txt"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("old copy target survived: %v", err)
	}
	if _, err := r.Write("move-source/new.txt", strings.NewReader("moved"), false); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Write("move-target/old.txt", strings.NewReader("old"), false); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Move("move-source", "move-target", true); err != nil {
		t.Fatal(err)
	}
	if data, err := r.ReadContent("move-target/new.txt"); err != nil || string(data) != "moved" {
		t.Fatalf("moved replacement = %q, %v", data, err)
	}
	if _, err := r.Entry("move-source"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("move source survived: %v", err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".zenfm-replaced-") {
			t.Fatalf("replacement backup leaked: %s", entry.Name())
		}
	}
}

func TestCopyRejectsSamePathAndDirectoryDescendant(t *testing.T) {
	r, _ := testRoot(t, Options{})
	if _, err := r.Mkdir("tree"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Write("tree/keep.txt", strings.NewReader("keep"), false); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Copy(context.Background(), "tree", "tree", true); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("same-path copy returned %v", err)
	}
	if data, err := r.ReadContent("tree/keep.txt"); err != nil || string(data) != "keep" {
		t.Fatalf("same-path copy damaged source: %q %v", data, err)
	}
	if _, err := r.Copy(context.Background(), "tree", "tree/descendant", false); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("descendant copy returned %v", err)
	}
}

func TestWriteNoReplaceIsAtomicBetweenConcurrentCreators(t *testing.T) {
	r, _ := testRoot(t, Options{})
	const writers = 32
	start := make(chan struct{})
	results := make(chan struct {
		value string
		err   error
	}, writers)
	for index := range writers {
		value := fmt.Sprintf("writer-%02d", index)
		go func() {
			<-start
			_, err := r.Write("winner.txt", strings.NewReader(value), false)
			results <- struct {
				value string
				err   error
			}{value: value, err: err}
		}()
	}
	close(start)
	winner := ""
	for range writers {
		result := <-results
		switch {
		case result.err == nil:
			if winner != "" {
				t.Fatalf("multiple no-replace writers succeeded: %q and %q", winner, result.value)
			}
			winner = result.value
		case errors.Is(result.err, ErrConflict):
		default:
			t.Fatalf("concurrent writer returned %v", result.err)
		}
	}
	if winner == "" {
		t.Fatal("no writer succeeded")
	}
	data, err := r.ReadContent("winner.txt")
	if err != nil || string(data) != winner {
		t.Fatalf("destination = %q, %v; winner = %q", data, err, winner)
	}
}

func TestCopyFailureCleansOnlyOwnedStage(t *testing.T) {
	r, dir := testRoot(t, Options{MaxWalkEntries: 2})
	if err := os.Mkdir(filepath.Join(dir, "source"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "source", "a-large"), make([]byte, 8<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "source", "b-limit"), []byte("limit"), 0o600); err != nil {
		t.Fatal(err)
	}
	creatorDone := make(chan error, 1)
	go func() {
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			entries, _ := os.ReadDir(dir)
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".zenfm-copy-") {
					if err := os.Mkdir(filepath.Join(dir, "destination"), 0o700); err != nil {
						creatorDone <- err
						return
					}
					creatorDone <- os.WriteFile(filepath.Join(dir, "destination", "concurrent.txt"), []byte("keep"), 0o600)
					return
				}
			}
			time.Sleep(time.Millisecond)
		}
		creatorDone <- errors.New("copy stage was not observed")
	}()
	_, copyErr := r.Copy(context.Background(), "source", "destination", false)
	if !errors.Is(copyErr, ErrWalkLimit) {
		t.Fatalf("copy returned %v", copyErr)
	}
	if err := <-creatorDone; err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "destination", "concurrent.txt"))
	if err != nil || string(data) != "keep" {
		t.Fatalf("copy cleanup deleted concurrent destination: %q %v", data, err)
	}
}

func TestMoveNoReplaceDoesNotOverwriteConcurrentCreator(t *testing.T) {
	r, dir := testRoot(t, Options{})
	other, err := Open(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Close() })
	for index := range 50 {
		source := fmt.Sprintf("source-%d", index)
		destination := fmt.Sprintf("destination-%d", index)
		if _, err := r.Write(source, strings.NewReader("source"), false); err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		results := make(chan error, 2)
		go func() {
			<-start
			_, err := r.Move(source, destination, false)
			results <- err
		}()
		go func() {
			<-start
			_, err := other.Write(destination, strings.NewReader("creator"), false)
			results <- err
		}()
		close(start)
		first, second := <-results, <-results
		successes := 0
		for _, err := range []error{first, second} {
			if err == nil {
				successes++
			} else if !errors.Is(err, ErrConflict) {
				t.Fatalf("iteration %d returned %v", index, err)
			}
		}
		if successes != 1 {
			t.Fatalf("iteration %d had %d successful publishers: %v / %v", index, successes, first, second)
		}
	}
}

func TestSymlinkCannotEscapeRoot(t *testing.T) {
	r, dir := testRoot(t, Options{})
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ReadContent("escape"); err == nil {
		t.Fatal("symlink escaped root")
	}
	listing, err := r.List("/", true)
	if err != nil || len(listing.Entries) != 1 || !listing.Entries[0].Symlink {
		t.Fatalf("symlink was not safely listed: %+v %v", listing, err)
	}
}

func TestOpenRegularResistsSymlinkSwap(t *testing.T) {
	r, dir := testRoot(t, Options{})
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("outside-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("inside"), 0o600); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 500 {
			_ = os.Remove(target)
			_ = os.Symlink(outside, target)
			_ = os.Remove(target)
			_ = os.WriteFile(target, []byte("inside"), 0o600)
		}
	}()
	for range 500 {
		data, err := r.ReadContent("target")
		if err == nil && string(data) != "inside" && string(data) != "" {
			t.Fatalf("escaped through swap: %q", data)
		}
	}
	wg.Wait()
}

func TestSpecialFileIsListedButNotOpened(t *testing.T) {
	r, dir := testRoot(t, Options{})
	pipe := filepath.Join(dir, "pipe")
	if err := makeFIFO(pipe); err != nil {
		t.Skipf("FIFO unavailable: %v", err)
	}
	listing, err := r.List("/", true)
	if err != nil || len(listing.Entries) != 1 || listing.Entries[0].Type != "special" {
		t.Fatalf("listing: %+v %v", listing, err)
	}
	if _, err := r.ReadContent("pipe"); !errors.Is(err, ErrNotRegular) {
		t.Fatalf("special file read returned %v", err)
	}
}

func TestOpenRegularDoesNotBlockWhenFileIsSwappedForFIFO(t *testing.T) {
	r, dir := testRoot(t, Options{})
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("inside"), 0o600); err != nil {
		t.Fatal(err)
	}
	probe := filepath.Join(dir, "fifo-probe")
	if err := makeFIFO(probe); err != nil {
		t.Skipf("FIFO unavailable: %v", err)
	}
	if err := os.Remove(probe); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		for range 2_000 {
			data, err := r.ReadContent("target")
			if err == nil && string(data) != "inside" && len(data) != 0 {
				done <- errors.New("read unexpected data during FIFO swap")
				return
			}
		}
		done <- nil
	}()
	for range 2_000 {
		_ = os.Remove(target)
		_ = makeFIFO(target)
		_ = os.Remove(target)
		_ = os.WriteFile(target, []byte("inside"), 0o600)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("regular-file open blocked after a FIFO swap")
	}
}

func TestWriteLimitIsAtomic(t *testing.T) {
	r, dir := testRoot(t, Options{MaxWriteBytes: 3})
	if _, err := r.Write("file", strings.NewReader("toolarge"), false); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("expected limit, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "file")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial destination exists: %v", err)
	}
}

func TestListAndSearchBoundDirectoryEntries(t *testing.T) {
	r, dir := testRoot(t, Options{MaxWalkEntries: 2})
	for _, name := range []string{"one", "two", "three"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.List("/", true); !errors.Is(err, ErrWalkLimit) {
		t.Fatalf("unbounded listing returned %v", err)
	}
	if _, err := r.Search(context.Background(), "/", "missing", true, 10); !errors.Is(err, ErrWalkLimit) {
		t.Fatalf("unbounded search returned %v", err)
	}
}

func TestRootCanBeListedRepeatedly(t *testing.T) {
	r, _ := testRoot(t, Options{})
	if _, err := r.Mkdir("books"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Write("books/file", strings.NewReader("data"), false); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := r.validateComponents(".", false, false); err != nil {
			t.Fatal(err)
		}
		if _, err := r.List("/", true); err != nil {
			t.Fatalf("repeated root listing failed: %v", err)
		}
		if _, err := r.List("/books", true); err != nil {
			t.Fatalf("repeated child listing failed: %v", err)
		}
	}
	if result, err := r.Search(context.Background(), "/", "file", true, 10); err != nil || len(result.Entries) != 1 {
		t.Fatalf("search after repeated listing failed: %+v %v", result, err)
	}
}

func TestSearchSkipsUnreadableChildDirectory(t *testing.T) {
	r, dir := testRoot(t, Options{})
	locked := filepath.Join(dir, "a-locked")
	if err := os.Mkdir(locked, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	if err := os.WriteFile(filepath.Join(dir, "z-target.txt"), []byte("target"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := r.Search(context.Background(), "/", "target", true, 10)
	if err != nil || len(result.Entries) != 1 || result.Entries[0].Name != "z-target.txt" {
		t.Fatalf("search stopped at unreadable child: %+v %v", result, err)
	}
}

func TestUsage(t *testing.T) {
	r, _ := testRoot(t, Options{})
	usage, err := r.Usage()
	if err != nil {
		t.Fatal(err)
	}
	if usage.Total == 0 || usage.Used > usage.Total {
		t.Fatalf("invalid usage: %+v", usage)
	}
}

func TestAdvancedRootListsPseudoFilesystemsWithoutOpeningTheirContent(t *testing.T) {
	r, err := Open("/", Options{MaxWalkEntries: 100})
	if err != nil {
		t.Skipf("literal root unavailable: %v", err)
	}
	defer r.Close()
	listing, err := r.List("/", true)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, entry := range listing.Entries {
		found[entry.Name] = true
	}
	for _, name := range []string{"proc", "sys", "dev"} {
		if !found[name] {
			t.Skipf("host root has no /%s", name)
		}
	}
	if _, err := r.ReadContent("proc/cpuinfo"); !errors.Is(err, ErrPseudoFile) {
		t.Fatalf("pseudo-file content was not blocked: %v", err)
	}
}

func TestAdvancedModeAllowsRegularStateAndCertificateFiles(t *testing.T) {
	r, _ := testRoot(t, Options{})
	r.advanced = true // Safe stand-in for literal / without modifying the host root.
	for name, content := range map[string]string{"zenfm/state.db": "database", "zenfm/cert.pem": "certificate"} {
		if err := r.root.MkdirAll(path.Dir(name), 0o700); err != nil {
			t.Fatal(err)
		}
		if _, err := r.Write(name, strings.NewReader(content), false); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		got, err := r.ReadContent(name)
		if err != nil || string(got) != content {
			t.Fatalf("read %s: %q %v", name, got, err)
		}
	}
}

func TestNormalModeHidesPrivateStateSubtree(t *testing.T) {
	r, dir := testRoot(t, Options{})
	stateDir := filepath.Join(dir, ".koreader", "settings", "zenfm")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "zenfm.db"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.ExcludeAbsolute(stateDir); err != nil {
		t.Fatal(err)
	}
	listing, err := r.List("/.koreader/settings", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(listing.Entries) != 0 {
		t.Fatalf("private state was listed: %+v", listing.Entries)
	}
	if _, err := r.ReadContent("/.koreader/settings/zenfm/zenfm.db"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("private state was readable: %v", err)
	}
	if err := os.Symlink(filepath.Join(".koreader", "settings", "zenfm"), filepath.Join(dir, "state-alias")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ReadContent("/state-alias/zenfm.db"); err == nil {
		t.Fatal("private state was readable through an intermediate symlink")
	}
	r.advanced = true
	data, err := r.ReadContent("/.koreader/settings/zenfm/zenfm.db")
	if err != nil || string(data) != "secret" {
		t.Fatalf("advanced state access failed: %q %v", data, err)
	}
}

func TestExclusionCanonicalizesSymlinkedAndMissingTargets(t *testing.T) {
	r, dir := testRoot(t, Options{})
	stateDir := filepath.Join(dir, "state")
	if err := os.Mkdir(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "state-alias")
	if err := os.Symlink(stateDir, alias); err != nil {
		t.Fatal(err)
	}
	if err := r.ExcludeAbsolute(alias); err != nil {
		t.Fatal(err)
	}
	missingKey := filepath.Join(dir, "custom", "private.key")
	if err := r.ExcludeAbsolute(missingKey); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(missingKey), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(missingKey, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	listing, err := r.List("/", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range listing.Entries {
		if entry.Name == "state" {
			t.Fatal("symlinked state directory was not canonically excluded")
		}
	}
	if _, err := r.ReadContent("custom/private.key"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing-at-registration private key became readable: %v", err)
	}
}

func FuzzNormalize(f *testing.F) {
	for _, seed := range []string{"/", "books/file", "../etc/passwd", "a//b", "a%2fb"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		clean, err := Normalize(value)
		if err == nil && (clean == ".." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/")) {
			t.Fatalf("unsafe result %q for %q", clean, value)
		}
	})
}
