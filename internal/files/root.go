// Package files implements all filesystem access through Go's traversal-safe
// os.Root API. It intentionally has no shell or command execution features.
package files

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/xZenLabs/zen-fm/internal/auth"
)

const (
	DefaultMaxWriteBytes   = int64(2 << 30)
	DefaultMaxContentBytes = int64(4 << 20)
	DefaultMaxWalkEntries  = 100_000
	internalOwnerFile      = ".zenfm-owner"
	internalOwnerContents  = "zenfm-internal-directory-v1\n"
)

var (
	ErrInvalidPath                = errors.New("invalid path")
	ErrNotRegular                 = errors.New("path is not a regular file")
	ErrPseudoFile                 = errors.New("content access to pseudo-filesystems is disabled")
	ErrTooLarge                   = errors.New("content is too large")
	ErrConflict                   = errors.New("destination already exists")
	ErrWalkLimit                  = errors.New("operation entry limit exceeded")
	errRenameNoReplaceUnsupported = errors.New("atomic no-replace rename is unsupported")
	chmodFile                     = func(file *os.File, mode os.FileMode) error { return file.Chmod(mode) }
)

type Root struct {
	root           *os.Root
	name           string
	advanced       bool
	maxWriteBytes  int64
	maxReadBytes   int64
	maxWalkEntries int
	excluded       []string
	excludedObject map[objectID]struct{}
	internal       []string
	internalObject map[objectID]struct{}
	pseudoDevices  map[uint64]struct{}
	publishMu      *sync.Mutex
	renameForMove  func(*os.Root, string, *os.Root, string, bool) error
	linkForMove    func(*os.Root, string, *os.Root, string) error
}

type objectID struct {
	device uint64
	inode  uint64
}

type Options struct {
	MaxWriteBytes   int64
	MaxContentBytes int64
	MaxWalkEntries  int
}

type Entry struct {
	Name       string    `json:"name"`
	Path       string    `json:"path"`
	Size       int64     `json:"size"`
	ModifiedAt time.Time `json:"modifiedAt"`
	Type       string    `json:"type"`
	MIMEType   string    `json:"mimeType,omitempty"`
	Hidden     bool      `json:"hidden"`
	Writable   bool      `json:"writable"`
	Mode       string    `json:"-"`
	Directory  bool      `json:"-"`
	Regular    bool      `json:"-"`
	Symlink    bool      `json:"-"`
}

type Listing struct {
	Path         string  `json:"path"`
	AdvancedMode bool    `json:"advancedMode"`
	Entries      []Entry `json:"entries"`
}

type SearchResult struct {
	Entries   []Entry `json:"entries"`
	Truncated bool    `json:"truncated"`
}

func Open(name string, opts Options) (*Root, error) {
	if name == "" {
		return nil, errors.New("root is empty")
	}
	abs, err := pathAbs(name)
	if err != nil {
		return nil, err
	}
	r, err := os.OpenRoot(abs)
	if err != nil {
		return nil, fmt.Errorf("open root: %w", err)
	}
	if opts.MaxWriteBytes <= 0 {
		opts.MaxWriteBytes = DefaultMaxWriteBytes
	}
	if opts.MaxContentBytes <= 0 {
		opts.MaxContentBytes = DefaultMaxContentBytes
	}
	if opts.MaxWalkEntries <= 0 {
		opts.MaxWalkEntries = DefaultMaxWalkEntries
	}
	root := &Root{root: r, name: abs, advanced: abs == string(os.PathSeparator), maxWriteBytes: opts.MaxWriteBytes, maxReadBytes: opts.MaxContentBytes, maxWalkEntries: opts.MaxWalkEntries, publishMu: &sync.Mutex{}, renameForMove: defaultMoveRename, linkForMove: linkNoReplace}
	root.loadPseudoDevices()
	return root, nil
}

func (r *Root) Close() error         { return r.root.Close() }
func (r *Root) Name() string         { return r.name }
func (r *Root) Advanced() bool       { return r.advanced }
func (r *Root) MaxWriteBytes() int64 { return r.maxWriteBytes }

func (r *Root) Pseudo(name string) bool {
	clean, err := r.clean(name)
	return err == nil && r.pseudoTarget(clean)
}

// Restricted returns a separately owned view of the same root with the named
// subtrees excluded, including when the root is the advanced literal / view.
func (r *Root) Restricted(names ...string) (*Root, error) {
	restricted, err := Open(r.name, Options{
		MaxWriteBytes: r.maxWriteBytes, MaxContentBytes: r.maxReadBytes,
		MaxWalkEntries: r.maxWalkEntries,
	})
	if err != nil {
		return nil, err
	}
	for _, name := range names {
		abs, err := canonicalAbsolute(name)
		if err != nil {
			_ = restricted.Close()
			return nil, err
		}
		if abs == restricted.name {
			restricted.excluded = append(restricted.excluded, "")
			continue
		}
		if err := restricted.excludeAbsolute(name); err != nil {
			_ = restricted.Close()
			return nil, err
		}
	}
	return restricted, nil
}

// ExcludeAbsolute hides a private subtree when it lies below a normal served
// root. Literal / advanced mode intentionally ignores exclusions.
func (r *Root) ExcludeAbsolute(name string) error {
	if r.advanced {
		return nil
	}
	return r.excludeAbsolute(name)
}

func (r *Root) excludeAbsolute(name string) error {
	abs, err := canonicalAbsolute(name)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(r.name, abs)
	if err != nil {
		return err
	}
	if relative == "." {
		return errors.New("private state directory cannot be the served root")
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return nil
	}
	clean := filepath.ToSlash(relative)
	if _, err := Normalize(clean); err != nil {
		return err
	}
	r.excluded = append(r.excluded, clean)
	if info, statErr := os.Stat(abs); statErr == nil {
		if id, ok := fileObjectID(info); ok {
			if r.excludedObject == nil {
				r.excludedObject = make(map[objectID]struct{})
			}
			r.excludedObject[id] = struct{}{}
		}
	}
	return nil
}

func (r *Root) clean(name string) (string, error) {
	clean, err := Normalize(name)
	if err != nil {
		return "", err
	}
	if r.isExcluded(clean) {
		return "", fs.ErrNotExist
	}
	if r.isInternal(clean) {
		return "", fs.ErrNotExist
	}
	return clean, nil
}

// validateComponents prevents an intermediate symlink from changing the
// security meaning of a lexical path. Symlinks remain visible as directory
// entries and may be removed or renamed as final components, but ZenFM never
// follows them for content or recursive operations.
func (r *Root) validateComponents(clean string, allowFinalSymlink, allowMissingFinal bool) error {
	parent, base, err := r.openParent(clean)
	if err != nil {
		return err
	}
	defer parent.Close()
	if base == "." {
		return nil
	}
	info, err := parent.Lstat(base)
	if err != nil {
		if allowMissingFinal && errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	if info.Mode()&fs.ModeSymlink != 0 && !allowFinalSymlink {
		return ErrInvalidPath
	}
	return nil
}

// openParent walks one directory descriptor at a time. Each opened directory
// must still be the same inode that was inspected immediately before opening,
// closing intermediate-symlink races and detecting bind aliases of excluded
// private state directories.
func (r *Root) openParent(clean string) (*os.Root, string, error) {
	current, err := r.root.OpenRoot(".")
	if err != nil {
		return nil, "", err
	}
	if clean == "." {
		return current, ".", nil
	}
	components := strings.Split(clean, "/")
	for _, component := range components[:len(components)-1] {
		before, err := current.Lstat(component)
		if err != nil {
			current.Close()
			return nil, "", err
		}
		if before.Mode()&fs.ModeSymlink != 0 || !before.IsDir() {
			current.Close()
			return nil, "", ErrInvalidPath
		}
		next, err := current.OpenRoot(component)
		if err != nil {
			current.Close()
			return nil, "", err
		}
		after, err := next.Stat(".")
		if err != nil || !os.SameFile(before, after) {
			next.Close()
			current.Close()
			if err != nil {
				return nil, "", err
			}
			return nil, "", ErrInvalidPath
		}
		if r.isExcludedObject(after) {
			next.Close()
			current.Close()
			return nil, "", fs.ErrNotExist
		}
		current.Close()
		current = next
	}
	return current, components[len(components)-1], nil
}

func (r *Root) isExcludedObject(info os.FileInfo) bool {
	id, ok := fileObjectID(info)
	if !ok {
		return false
	}
	if _, internal := r.internalObject[id]; internal {
		return true
	}
	if r.advanced || len(r.excludedObject) == 0 {
		return false
	}
	_, excluded := r.excludedObject[id]
	return excluded
}

func (r *Root) isExcluded(clean string) bool {
	if r.advanced {
		return false
	}
	for _, excluded := range r.excluded {
		if excluded == "" || clean == excluded || strings.HasPrefix(clean, excluded+"/") {
			return true
		}
	}
	return false
}

func (r *Root) isInternal(clean string) bool {
	for _, internal := range r.internal {
		if clean == internal || strings.HasPrefix(clean, internal+"/") {
			return true
		}
	}
	return false
}

func Normalize(name string) (string, error) {
	if name == "" || name == "/" {
		return ".", nil
	}
	if !utf8.ValidString(name) || strings.ContainsRune(name, 0) || strings.Contains(name, "\\") {
		return "", ErrInvalidPath
	}
	name = strings.TrimPrefix(name, "/")
	for _, component := range strings.Split(name, "/") {
		if component == "" || component == "." || component == ".." {
			return "", ErrInvalidPath
		}
	}
	clean := path.Clean(name)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || path.IsAbs(clean) {
		return "", ErrInvalidPath
	}
	return clean, nil
}

func PublicPath(name string) string {
	if name == "." || name == "" {
		return "/"
	}
	return "/" + name
}

func (r *Root) List(name string, includeHidden bool) (Listing, error) {
	return r.list(name, includeHidden, true)
}

func (r *Root) list(name string, includeHidden, skipUnreadable bool) (Listing, error) {
	clean, err := r.clean(name)
	if err != nil {
		return Listing{}, err
	}
	parent, base, err := r.openParent(clean)
	if err != nil {
		return Listing{}, err
	}
	defer parent.Close()
	before, err := parent.Lstat(base)
	if err != nil || before.Mode()&fs.ModeSymlink != 0 {
		if err != nil {
			return Listing{}, err
		}
		return Listing{}, fmt.Errorf("listed directory is a symlink: %w", ErrInvalidPath)
	}
	var f *os.File
	if base == "." {
		f, err = parent.Open(".")
	} else {
		f, err = parent.OpenFile(base, os.O_RDONLY|regularOpenFlags(), 0)
	}
	if err != nil {
		return Listing{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return Listing{}, err
	}
	if !info.IsDir() {
		return Listing{}, fmt.Errorf("%w: %s has mode %s", ErrInvalidPath, name, info.Mode())
	}
	if !os.SameFile(before, info) {
		return Listing{}, fmt.Errorf("listed directory changed during open: %w", ErrInvalidPath)
	}
	if r.isExcludedObject(info) {
		return Listing{}, fs.ErrNotExist
	}
	items, err := readDirBounded(f, r.maxWalkEntries)
	if err != nil {
		return Listing{}, err
	}
	out := Listing{Path: PublicPath(clean), AdvancedMode: r.advanced, Entries: make([]Entry, 0, len(items))}
	for _, item := range items {
		if !includeHidden && strings.HasPrefix(item.Name(), ".") {
			continue
		}
		entryPath := item.Name()
		if clean != "." {
			entryPath = path.Join(clean, item.Name())
		}
		if r.isInternal(entryPath) {
			continue
		}
		if r.isExcluded(entryPath) {
			continue
		}
		info, err := item.Info()
		if err != nil {
			if skipUnreadable {
				continue
			}
			return Listing{}, err
		}
		if r.isExcludedObject(info) {
			continue
		}
		entry := entryFromInfo(entryPath, info)
		entry.Symlink = item.Type()&fs.ModeSymlink != 0
		if entry.Symlink {
			entry.Type = "symlink"
			entry.Regular = false
			entry.Directory = false
		}
		out.Entries = append(out.Entries, entry)
	}
	sort.Slice(out.Entries, func(i, j int) bool {
		if out.Entries[i].Directory != out.Entries[j].Directory {
			return out.Entries[i].Directory
		}
		return strings.ToLower(out.Entries[i].Name) < strings.ToLower(out.Entries[j].Name)
	})
	return out, nil
}

// OpenInternalDirectory creates a private, descriptor-rooted directory inside
// the served filesystem. Its reserved name and inode are hidden from every
// public Root operation, including advanced mode and bind-mount aliases.
func (r *Root) OpenInternalDirectory(name string) (*os.Root, string, error) {
	if !strings.HasPrefix(name, ".zenfm-internal-") || strings.Contains(name, "/") || strings.Contains(name, "\\") {
		return nil, "", ErrInvalidPath
	}
	r.publishMu.Lock()
	defer r.publishMu.Unlock()
	created := false
	before, err := r.root.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		if err := r.root.Mkdir(name, 0o700); err != nil {
			return nil, "", err
		}
		created = true
		before, err = r.root.Lstat(name)
	}
	if err != nil {
		if created {
			_ = r.root.Remove(name)
		}
		return nil, "", err
	}
	if before.Mode()&fs.ModeSymlink != 0 || !before.IsDir() {
		return nil, "", ErrInvalidPath
	}
	sub, err := r.root.OpenRoot(name)
	if err != nil {
		if created {
			_ = r.root.Remove(name)
		}
		return nil, "", err
	}
	after, err := sub.Stat(".")
	if err != nil || !os.SameFile(before, after) || r.pseudoInfo(after) {
		sub.Close()
		if created {
			_ = r.root.Remove(name)
		}
		if err != nil {
			return nil, "", err
		}
		return nil, "", ErrInvalidPath
	}
	if err := claimInternalDirectory(sub, created); err != nil {
		if created {
			_ = sub.Remove(internalOwnerFile)
		}
		sub.Close()
		if created {
			_ = r.root.Remove(name)
		}
		return nil, "", err
	}
	directory, err := sub.Open(".")
	if err != nil {
		if created {
			_ = sub.Remove(internalOwnerFile)
		}
		sub.Close()
		if created {
			_ = r.root.Remove(name)
		}
		return nil, "", err
	}
	// FAT and Android emulated storage can be writable while rejecting chmod.
	// The directory is still hidden from ZenFM and holds data destined for the
	// same user-visible filesystem, so permissions are tightened best-effort.
	if err := chmodFile(directory, 0o700); err != nil && !errors.Is(err, fs.ErrPermission) {
		directory.Close()
		if created {
			_ = sub.Remove(internalOwnerFile)
		} else {
			r.registerInternal(name, after)
		}
		sub.Close()
		if created {
			_ = r.root.Remove(name)
		}
		return nil, "", err
	}
	closeErr := directory.Close()
	if closeErr != nil {
		if created {
			_ = sub.Remove(internalOwnerFile)
		} else {
			r.registerInternal(name, after)
		}
		sub.Close()
		if created {
			_ = r.root.Remove(name)
		}
		return nil, "", closeErr
	}
	r.registerInternal(name, after)
	return sub, filepath.Join(r.name, filepath.FromSlash(name)), nil
}

func (r *Root) registerInternal(name string, info os.FileInfo) {
	if id, ok := fileObjectID(info); ok {
		if r.internalObject == nil {
			r.internalObject = make(map[objectID]struct{})
		}
		r.internalObject[id] = struct{}{}
	}
	found := false
	for _, existing := range r.internal {
		found = found || existing == name
	}
	if !found {
		r.internal = append(r.internal, name)
	}
}

func claimInternalDirectory(root *os.Root, created bool) error {
	if created {
		marker, err := root.OpenFile(internalOwnerFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		_, writeErr := io.WriteString(marker, internalOwnerContents)
		syncErr := marker.Sync()
		closeErr := marker.Close()
		return errors.Join(writeErr, syncErr, closeErr)
	}
	marker, err := root.OpenFile(internalOwnerFile, os.O_RDONLY|regularOpenFlags(), 0)
	if err != nil {
		return ErrInvalidPath
	}
	contents, readErr := io.ReadAll(io.LimitReader(marker, int64(len(internalOwnerContents)+1)))
	closeErr := marker.Close()
	if readErr != nil || closeErr != nil {
		return errors.Join(readErr, closeErr)
	}
	if string(contents) != internalOwnerContents {
		return ErrInvalidPath
	}
	return nil
}

func readDirBounded(directory *os.File, limit int) ([]os.DirEntry, error) {
	const batchSize = 256
	items := make([]os.DirEntry, 0, min(limit, batchSize))
	for {
		batch, err := directory.ReadDir(batchSize)
		if len(batch) > limit-len(items) {
			return nil, ErrWalkLimit
		}
		items = append(items, batch...)
		switch {
		case errors.Is(err, io.EOF):
			return items, nil
		case err != nil:
			return nil, err
		}
	}
}

func (r *Root) Entry(name string) (Entry, error) {
	clean, err := r.clean(name)
	if err != nil {
		return Entry{}, err
	}
	parent, base, err := r.openParent(clean)
	if err != nil {
		return Entry{}, err
	}
	defer parent.Close()
	info, err := parent.Lstat(base)
	if err != nil {
		return Entry{}, err
	}
	if r.isExcludedObject(info) {
		return Entry{}, fs.ErrNotExist
	}
	return entryFromInfo(clean, info), nil
}

// OpenScope creates a descriptor-rooted capability for an existing directory.
// Relative operations on the returned root cannot follow a symlink outside the
// scoped directory, which is used to enforce public-share boundaries.
func (r *Root) OpenScope(name string) (*Root, error) {
	clean, err := r.clean(name)
	if err != nil {
		return nil, err
	}
	parent, base, err := r.openParent(clean)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	before, err := parent.Lstat(base)
	if err != nil {
		return nil, err
	}
	if before.Mode()&fs.ModeSymlink != 0 || !before.IsDir() {
		return nil, ErrInvalidPath
	}
	sub, err := parent.OpenRoot(base)
	if err != nil {
		return nil, err
	}
	after, err := sub.Stat(".")
	if err != nil || !os.SameFile(before, after) {
		sub.Close()
		if err != nil {
			return nil, err
		}
		return nil, ErrInvalidPath
	}
	if r.isExcludedObject(after) {
		sub.Close()
		return nil, fs.ErrNotExist
	}
	return &Root{
		root: sub, name: filepath.Join(r.name, filepath.FromSlash(clean)), advanced: r.advanced,
		maxWriteBytes: r.maxWriteBytes, maxReadBytes: r.maxReadBytes, maxWalkEntries: r.maxWalkEntries,
		excluded: r.rebasedExclusions(clean), excludedObject: r.excludedObject,
		internal: r.rebasedInternals(clean), internalObject: r.internalObject,
		pseudoDevices: r.pseudoDevices, publishMu: r.publishMu, renameForMove: r.renameForMove, linkForMove: r.linkForMove,
	}, nil
}

func (r *Root) rebasedInternals(base string) []string {
	values := make([]string, 0, len(r.internal))
	for _, internal := range r.internal {
		switch {
		case base == ".":
			values = append(values, internal)
		case strings.HasPrefix(internal, base+"/"):
			values = append(values, strings.TrimPrefix(internal, base+"/"))
		}
	}
	return values
}

func (r *Root) rebasedExclusions(base string) []string {
	values := make([]string, 0, len(r.excluded))
	for _, excluded := range r.excluded {
		switch {
		case base == ".":
			values = append(values, excluded)
		case strings.HasPrefix(excluded, base+"/"):
			values = append(values, strings.TrimPrefix(excluded, base+"/"))
		}
	}
	return values
}

func (r *Root) OpenRegular(name string) (*os.File, os.FileInfo, error) {
	clean, err := r.clean(name)
	if err != nil {
		return nil, nil, err
	}
	parent, base, err := r.openParent(clean)
	if err != nil {
		return nil, nil, err
	}
	defer parent.Close()
	if r.pseudoTarget(clean) {
		return nil, nil, ErrPseudoFile
	}
	// Lstat cheaply rejects special entries. The actual open also uses
	// O_NOFOLLOW|O_NONBLOCK on KOReader platforms, closing the swap race and
	// preventing a raced FIFO/device from blocking or causing side effects.
	before, err := parent.Lstat(base)
	if err != nil {
		return nil, nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, nil, ErrNotRegular
	}
	f, err := parent.OpenFile(base, os.O_RDONLY|regularOpenFlags(), 0)
	if err != nil {
		return nil, nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	if r.pseudoInfo(info) || pseudoFilesystemFile(f) {
		f.Close()
		return nil, nil, ErrPseudoFile
	}
	if !info.Mode().IsRegular() || !os.SameFile(before, info) {
		f.Close()
		return nil, nil, ErrNotRegular
	}
	return f, info, nil
}

func (r *Root) ReadContent(name string) ([]byte, error) {
	f, info, err := r.OpenRegular(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if info.Size() > r.maxReadBytes {
		return nil, ErrTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(f, r.maxReadBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > r.maxReadBytes {
		return nil, ErrTooLarge
	}
	return data, nil
}

func (r *Root) Write(name string, src io.Reader, overwrite bool) (Entry, error) {
	return r.WriteContext(context.Background(), name, src, overwrite)
}

func (r *Root) WriteContext(ctx context.Context, name string, src io.Reader, overwrite bool) (Entry, error) {
	if err := ctx.Err(); err != nil {
		return Entry{}, err
	}
	clean, err := r.clean(name)
	if err != nil {
		return Entry{}, err
	}
	if clean == "." {
		return Entry{}, ErrPseudoFile
	}
	if r.pseudoTarget(clean) {
		return Entry{}, ErrPseudoFile
	}
	parent, base, err := r.openParent(clean)
	if err != nil {
		return Entry{}, err
	}
	defer parent.Close()
	parentInfo, err := parent.Stat(".")
	if err != nil {
		return Entry{}, err
	}
	if r.pseudoInfo(parentInfo) {
		return Entry{}, ErrPseudoFile
	}
	if info, statErr := parent.Lstat(base); statErr == nil {
		if !overwrite {
			return Entry{}, ErrConflict
		}
		if info.IsDir() || info.Mode()&fs.ModeType != 0 && info.Mode()&fs.ModeSymlink == 0 {
			return Entry{}, ErrNotRegular
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return Entry{}, statErr
	}
	token, err := auth.RandomToken(".zenfm-upload-", 128)
	if err != nil {
		return Entry{}, err
	}
	tmp := token
	f, err := parent.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return Entry{}, err
	}
	ok := false
	defer func() {
		_ = f.Close()
		if !ok {
			_ = parent.Remove(tmp)
		}
	}()
	n, err := copyStreamWithProgress(ctx, f, io.LimitReader(src, r.maxWriteBytes+1), nil)
	if err != nil {
		return Entry{}, err
	}
	if n > r.maxWriteBytes {
		return Entry{}, ErrTooLarge
	}
	if err := f.Sync(); err != nil {
		return Entry{}, err
	}
	if err := f.Close(); err != nil {
		return Entry{}, err
	}
	if err := ctx.Err(); err != nil {
		return Entry{}, err
	}
	if err := r.commitTemp(parent, tmp, base, overwrite); err != nil {
		return Entry{}, err
	}
	ok = true
	return r.Entry(clean)
}

// commitTemp publishes a complete temporary file without a check-then-rename
// race. Hard-link publication is atomic on filesystems that support it. The
// portable fallback serializes ZenFM publishers, rechecks absence, and renames
// the complete file without ever exposing an empty destination.
func (r *Root) commitTemp(parent *os.Root, temporary, destination string, overwrite bool) error {
	r.publishMu.Lock()
	defer r.publishMu.Unlock()
	return r.commitTempLocked(parent, temporary, destination, overwrite)
}

func (r *Root) commitTempLocked(parent *os.Root, temporary, destination string, overwrite bool) error {
	if overwrite {
		return parent.Rename(temporary, destination)
	}
	if err := parent.Link(temporary, destination); err == nil {
		return parent.Remove(temporary)
	} else if errors.Is(err, fs.ErrExist) {
		return ErrConflict
	}
	if _, err := parent.Lstat(destination); err == nil {
		return ErrConflict
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return parent.Rename(temporary, destination)
}

// PublishTemporary atomically moves a regular file from a private Root into
// the served tree when both roots are on the same filesystem. A false moved
// result tells the caller to use a streaming cross-filesystem fallback; the
// source remains untouched in that case.
func (r *Root) PublishTemporary(sourceRoot *os.Root, source, destination string, overwrite bool) (bool, error) {
	if sourceRoot == nil || source == "" || source == "." || strings.Contains(source, "/") || strings.Contains(source, "\\") {
		return false, ErrInvalidPath
	}
	clean, err := r.clean(destination)
	if err != nil || clean == "." {
		return false, ErrInvalidPath
	}
	if r.pseudoTarget(clean) {
		return false, ErrPseudoFile
	}
	sourceInfo, err := sourceRoot.Lstat(source)
	if err != nil {
		return false, err
	}
	if !sourceInfo.Mode().IsRegular() {
		return false, ErrNotRegular
	}
	parent, base, err := r.openParent(clean)
	if err != nil {
		return false, err
	}
	defer parent.Close()
	parentInfo, err := parent.Stat(".")
	if err != nil {
		return false, err
	}
	if r.pseudoInfo(parentInfo) {
		return false, ErrPseudoFile
	}
	r.publishMu.Lock()
	defer r.publishMu.Unlock()
	if existing, statErr := parent.Lstat(base); statErr == nil {
		if !overwrite {
			return false, ErrConflict
		}
		if existing.IsDir() || existing.Mode()&fs.ModeType != 0 && existing.Mode()&fs.ModeSymlink == 0 {
			return false, ErrNotRegular
		}
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return false, statErr
	}
	err = r.renameForMove(sourceRoot, source, parent, base, overwrite)
	if errors.Is(err, syscall.EXDEV) {
		return false, nil
	}
	if !overwrite && errors.Is(err, errRenameNoReplaceUnsupported) {
		err = r.linkForMove(sourceRoot, source, parent, base)
		if err == nil {
			_ = sourceRoot.Remove(source)
			return true, nil
		}
		if errors.Is(err, fs.ErrExist) {
			return false, ErrConflict
		}
		// Some writable filesystems, notably FAT and Android emulated storage,
		// support neither no-replace rename nor hard links. We still hold the
		// publisher lock and checked that the destination is absent, so use the
		// same portable rename fallback as WriteContext.
		if _, statErr := parent.Lstat(base); statErr == nil {
			return false, ErrConflict
		} else if !errors.Is(statErr, fs.ErrNotExist) {
			return false, statErr
		}
		err = renameReplace(sourceRoot, source, parent, base)
	}
	if errors.Is(err, errRenameNoReplaceUnsupported) || errors.Is(err, syscall.EXDEV) {
		return false, nil
	}
	if errors.Is(err, fs.ErrExist) {
		return false, ErrConflict
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (r *Root) Mkdir(name string) (Entry, error) {
	clean, err := r.clean(name)
	if err != nil || clean == "." {
		return Entry{}, ErrInvalidPath
	}
	if r.pseudoTarget(clean) {
		return Entry{}, ErrPseudoFile
	}
	parent, base, err := r.openParent(clean)
	if err != nil {
		return Entry{}, err
	}
	defer parent.Close()
	r.publishMu.Lock()
	defer r.publishMu.Unlock()
	if err := parent.Mkdir(base, 0o750); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return Entry{}, ErrConflict
		}
		return Entry{}, err
	}
	return r.Entry(clean)
}

func (r *Root) Delete(name string, recursive bool) error {
	clean, err := r.clean(name)
	if err != nil || clean == "." {
		return ErrInvalidPath
	}
	if r.pseudoTarget(clean) {
		return ErrPseudoFile
	}
	parent, base, err := r.openParent(clean)
	if err != nil {
		return err
	}
	defer parent.Close()
	info, err := parent.Lstat(base)
	if err != nil {
		return err
	}
	if r.isExcludedObject(info) {
		return fs.ErrNotExist
	}
	r.publishMu.Lock()
	defer r.publishMu.Unlock()
	if info.IsDir() && recursive {
		return parent.RemoveAll(base)
	}
	return parent.Remove(base)
}

func (r *Root) Move(source, destination string, overwrite bool) (Entry, error) {
	return r.MoveWithProgress(context.Background(), source, destination, overwrite, nil)
}

func (r *Root) MoveWithProgress(ctx context.Context, source, destination string, overwrite bool, progress func()) (Entry, error) {
	if err := ctx.Err(); err != nil {
		return Entry{}, err
	}
	src, err := r.clean(source)
	if err != nil || src == "." {
		return Entry{}, ErrInvalidPath
	}
	dst, err := r.clean(destination)
	if err != nil || dst == "." {
		return Entry{}, ErrInvalidPath
	}
	if src == dst {
		return Entry{}, ErrInvalidPath
	}
	r.publishMu.Lock()
	defer r.publishMu.Unlock()
	if r.pseudoTarget(src) || r.pseudoTarget(dst) {
		return Entry{}, ErrPseudoFile
	}
	sourceParent, sourceBase, err := r.openParent(src)
	if err != nil {
		return Entry{}, err
	}
	defer sourceParent.Close()
	sourceInfo, err := sourceParent.Lstat(sourceBase)
	if err != nil {
		return Entry{}, err
	}
	if r.isExcludedObject(sourceInfo) || r.pseudoInfo(sourceInfo) {
		return Entry{}, ErrPseudoFile
	}
	if sourceInfo.IsDir() && strings.HasPrefix(dst, src+"/") {
		return Entry{}, ErrInvalidPath
	}
	destinationParent, destinationBase, err := r.openParent(dst)
	if err != nil {
		return Entry{}, err
	}
	defer destinationParent.Close()
	var destinationInfo os.FileInfo
	destinationExists := false
	if existing, statErr := destinationParent.Lstat(destinationBase); statErr == nil {
		destinationInfo = existing
		destinationExists = true
		if r.isExcludedObject(destinationInfo) || r.pseudoInfo(destinationInfo) {
			return Entry{}, ErrPseudoFile
		}
		if !overwrite {
			return Entry{}, ErrConflict
		}
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return Entry{}, statErr
	}
	if overwrite && destinationExists && (sourceInfo.IsDir() || destinationInfo.IsDir()) {
		err = r.replaceByRenameLocked(sourceParent, sourceBase, destinationParent, destinationBase)
	} else {
		err = r.renameForMove(sourceParent, sourceBase, destinationParent, destinationBase, overwrite)
	}
	if !overwrite {
		if errors.Is(err, errRenameNoReplaceUnsupported) && !sourceInfo.IsDir() {
			err = linkNoReplace(sourceParent, sourceBase, destinationParent, destinationBase)
			if err == nil {
				err = sourceParent.Remove(sourceBase)
			}
		}
	}
	if errors.Is(err, syscall.EXDEV) {
		return r.moveAcrossDevicesLocked(ctx, src, dst, sourceParent, sourceBase, sourceInfo, destinationParent, destinationBase, overwrite, progress)
	}
	if errors.Is(err, fs.ErrExist) {
		return Entry{}, ErrConflict
	}
	if err != nil {
		return Entry{}, err
	}
	return r.Entry(dst)
}

func defaultMoveRename(sourceParent *os.Root, source string, destinationParent *os.Root, destination string, overwrite bool) error {
	if overwrite {
		return renameReplace(sourceParent, source, destinationParent, destination)
	}
	return renameNoReplace(sourceParent, source, destinationParent, destination)
}

// replaceByRenameLocked moves an existing destination aside before publishing
// a directory replacement. Plain rename cannot replace a non-empty directory.
// The caller holds publishMu, and a failed publish restores the old target.
func (r *Root) replaceByRenameLocked(sourceParent *os.Root, source string, destinationParent *os.Root, destination string) error {
	backup, err := auth.RandomToken(".zenfm-replaced-", 128)
	if err != nil {
		return err
	}
	if err := destinationParent.Rename(destination, backup); err != nil {
		return err
	}
	if err := r.renameForMove(sourceParent, source, destinationParent, destination, true); err != nil {
		return errors.Join(err, destinationParent.Rename(backup, destination))
	}
	// Publication has succeeded. A cleanup failure must not report the
	// replacement itself as failed or put the old tree back into service.
	_ = destinationParent.RemoveAll(backup)
	return nil
}

// moveAcrossDevicesLocked stages a bounded private copy on the destination
// filesystem, publishes it, and only then removes the unchanged source. The
// caller holds publishMu throughout so another ZenFM mutation cannot be lost.
func (r *Root) moveAcrossDevicesLocked(ctx context.Context, src, dst string, sourceParent *os.Root, sourceBase string, sourceInfo os.FileInfo, destinationParent *os.Root, destinationBase string, overwrite bool, progress func()) (Entry, error) {
	if sourceInfo.Mode()&fs.ModeSymlink != 0 || sourceInfo.Mode()&fs.ModeType != 0 && !sourceInfo.IsDir() {
		return Entry{}, ErrNotRegular
	}
	stage, err := auth.RandomToken(".zenfm-move-", 128)
	if err != nil {
		return Entry{}, err
	}
	destinationScope := &Root{
		root: destinationParent, name: r.name, advanced: r.advanced,
		maxWriteBytes: r.maxWriteBytes, maxReadBytes: r.maxReadBytes, maxWalkEntries: r.maxWalkEntries,
		pseudoDevices: r.pseudoDevices, publishMu: r.publishMu, renameForMove: r.renameForMove, linkForMove: r.linkForMove,
	}
	count := 0
	var copiedBytes int64
	var byteProgress func(int64)
	if progress != nil {
		byteProgress = func(int64) { progress() }
	}
	if err := copyOneBetween(ctx, r, src, destinationScope, stage, &count, &copiedBytes, byteProgress, true); err != nil {
		_ = destinationParent.RemoveAll(stage)
		return Entry{}, err
	}
	if err := ctx.Err(); err != nil {
		_ = destinationParent.RemoveAll(stage)
		return Entry{}, err
	}
	if sourceInfo.IsDir() {
		err = r.commitDirectoryLocked(destinationParent, stage, destinationBase, overwrite)
	} else {
		err = r.commitTempLocked(destinationParent, stage, destinationBase, overwrite)
	}
	if err != nil {
		_ = destinationParent.RemoveAll(stage)
		return Entry{}, err
	}
	current, err := sourceParent.Lstat(sourceBase)
	if err != nil || !os.SameFile(sourceInfo, current) {
		if err != nil {
			return Entry{}, err
		}
		return Entry{}, ErrConflict
	}
	if sourceInfo.IsDir() {
		err = sourceParent.RemoveAll(sourceBase)
	} else {
		err = sourceParent.Remove(sourceBase)
	}
	if err != nil {
		return Entry{}, err
	}
	return r.Entry(dst)
}

func openDirectoryPair(sourceParent, destinationParent *os.Root) (*os.File, *os.File, error) {
	sourceDirectory, err := sourceParent.Open(".")
	if err != nil {
		return nil, nil, err
	}
	destinationDirectory, err := destinationParent.Open(".")
	if err != nil {
		sourceDirectory.Close()
		return nil, nil, err
	}
	return sourceDirectory, destinationDirectory, nil
}

func (r *Root) Copy(ctx context.Context, source, destination string, overwrite bool) (Entry, error) {
	return r.copy(ctx, source, destination, overwrite, nil)
}

func (r *Root) CopyWithProgress(ctx context.Context, source, destination string, overwrite bool, progress func()) (Entry, error) {
	if progress == nil {
		return r.copy(ctx, source, destination, overwrite, nil)
	}
	return r.copy(ctx, source, destination, overwrite, func(int64) { progress() })
}

func (r *Root) CopyWithByteProgress(ctx context.Context, source, destination string, overwrite bool, progress func(int64)) (Entry, error) {
	return r.copy(ctx, source, destination, overwrite, progress)
}

func (r *Root) CopySize(ctx context.Context, source string) (int64, error) {
	clean, err := r.clean(source)
	if err != nil || clean == "." {
		return 0, ErrInvalidPath
	}
	count := 0
	var total int64
	if err := r.copySize(ctx, clean, &count, &total); err != nil {
		return 0, err
	}
	return total, nil
}

func (r *Root) copySize(ctx context.Context, source string, count *int, total *int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	*count++
	if *count > r.maxWalkEntries {
		return ErrWalkLimit
	}
	if r.pseudoTarget(source) {
		return ErrPseudoFile
	}
	info, err := r.lstat(source, false, false)
	if err != nil {
		return err
	}
	if info.Mode()&fs.ModeSymlink != 0 || info.Mode()&fs.ModeType != 0 && !info.IsDir() {
		return ErrNotRegular
	}
	if !info.IsDir() {
		if info.Size() > r.maxWriteBytes-*total {
			return ErrTooLarge
		}
		*total += info.Size()
		return nil
	}
	listing, err := r.list(source, true, true)
	if err != nil {
		return err
	}
	for _, item := range listing.Entries {
		child := path.Join(source, item.Name)
		if item.Symlink || !item.Regular && !item.Directory || r.pseudoTarget(child) {
			continue
		}
		if err := r.copySize(ctx, child, count, total); err != nil {
			return err
		}
	}
	return nil
}

func (r *Root) copy(ctx context.Context, source, destination string, overwrite bool, progress func(int64)) (Entry, error) {
	src, err := r.clean(source)
	if err != nil || src == "." {
		return Entry{}, ErrInvalidPath
	}
	dst, err := r.clean(destination)
	if err != nil || dst == "." {
		return Entry{}, ErrInvalidPath
	}
	if src == dst {
		return Entry{}, ErrInvalidPath
	}
	sourceInfo, err := r.lstat(src, false, false)
	if err != nil {
		return Entry{}, err
	}
	if sourceInfo.IsDir() && strings.HasPrefix(dst, src+"/") {
		return Entry{}, ErrInvalidPath
	}
	if r.pseudoTarget(src) || r.pseudoTarget(dst) {
		return Entry{}, ErrPseudoFile
	}
	destinationParent, destinationBase, err := r.openParent(dst)
	if err != nil {
		return Entry{}, err
	}
	defer destinationParent.Close()
	parentInfo, err := destinationParent.Stat(".")
	if err != nil || r.pseudoInfo(parentInfo) {
		if err != nil {
			return Entry{}, err
		}
		return Entry{}, ErrPseudoFile
	}
	if destinationInfo, statErr := destinationParent.Lstat(destinationBase); statErr == nil {
		if r.isExcludedObject(destinationInfo) {
			return Entry{}, fs.ErrNotExist
		}
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return Entry{}, statErr
	}
	stage, err := auth.RandomToken(".zenfm-copy-", 128)
	if err != nil {
		return Entry{}, err
	}
	destinationScope := &Root{
		root: destinationParent, name: r.name, advanced: r.advanced,
		maxWriteBytes: r.maxWriteBytes, maxReadBytes: r.maxReadBytes, maxWalkEntries: r.maxWalkEntries,
		pseudoDevices: r.pseudoDevices, publishMu: r.publishMu, renameForMove: r.renameForMove, linkForMove: r.linkForMove,
	}
	count := 0
	var copiedBytes int64
	if err := copyOneBetween(ctx, r, src, destinationScope, stage, &count, &copiedBytes, progress, false); err != nil {
		_ = destinationParent.RemoveAll(stage)
		return Entry{}, err
	}
	if sourceInfo.IsDir() {
		err = r.commitDirectory(destinationParent, stage, destinationBase, overwrite)
	} else {
		err = r.commitTemp(destinationParent, stage, destinationBase, overwrite)
	}
	if err != nil {
		_ = destinationParent.RemoveAll(stage)
		return Entry{}, err
	}
	return r.Entry(dst)
}

func (r *Root) commitDirectory(parent *os.Root, temporary, destination string, overwrite bool) error {
	r.publishMu.Lock()
	defer r.publishMu.Unlock()
	return r.commitDirectoryLocked(parent, temporary, destination, overwrite)
}

func (r *Root) commitDirectoryLocked(parent *os.Root, temporary, destination string, overwrite bool) error {
	if _, err := parent.Lstat(destination); err == nil {
		if !overwrite {
			return ErrConflict
		}
		backup, err := auth.RandomToken(".zenfm-replaced-", 128)
		if err != nil {
			return err
		}
		if err := parent.Rename(destination, backup); err != nil {
			return err
		}
		if err := parent.Rename(temporary, destination); err != nil {
			return errors.Join(err, parent.Rename(backup, destination))
		}
		// The complete staged tree is now live. Do not turn successful
		// publication into a client-visible failure if old-tree cleanup fails.
		_ = parent.RemoveAll(backup)
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return parent.Rename(temporary, destination)
}

func copyOneBetween(ctx context.Context, source *Root, src string, destination *Root, dst string, count *int, copiedBytes *int64, progress func(int64), strict bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	*count++
	if progress != nil {
		progress(0)
	}
	if *count > source.maxWalkEntries {
		return ErrWalkLimit
	}
	if source.pseudoTarget(src) {
		return ErrPseudoFile
	}
	info, err := source.lstat(src, false, false)
	if err != nil {
		return err
	}
	if info.Mode()&fs.ModeSymlink != 0 || info.Mode()&fs.ModeType != 0 && !info.IsDir() {
		return ErrNotRegular
	}
	if info.IsDir() {
		destinationParent, destinationBase, err := destination.openParent(dst)
		if err != nil {
			return err
		}
		mode := copiedMode(info)
		if err := destinationParent.Mkdir(destinationBase, mode); err != nil {
			destinationParent.Close()
			return err
		}
		if err := chmodCreatedDirectory(destinationParent, destinationBase, mode); err != nil {
			destinationParent.Close()
			return err
		}
		destinationParent.Close()
		listing, err := source.list(src, true, !strict)
		if err != nil {
			return err
		}
		for _, item := range listing.Entries {
			childSrc, childDst := path.Join(src, item.Name), path.Join(dst, item.Name)
			if item.Symlink || !item.Regular && !item.Directory || source.pseudoTarget(childSrc) {
				if strict {
					return ErrNotRegular
				}
				continue
			}
			if err := copyOneBetween(ctx, source, childSrc, destination, childDst, count, copiedBytes, progress, strict); err != nil {
				return err
			}
		}
		return nil
	}
	in, actualInfo, err := source.OpenRegular(src)
	if err != nil {
		return err
	}
	defer in.Close()
	remaining := source.maxWriteBytes - *copiedBytes
	if remaining < 0 || actualInfo.Size() > remaining {
		return ErrTooLarge
	}
	parent, base, err := destination.openParent(dst)
	if err != nil {
		return err
	}
	defer parent.Close()
	mode := copiedMode(actualInfo)
	out, err := parent.OpenFile(base, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if err := chmodFile(out, mode); err != nil && !errors.Is(err, fs.ErrPermission) {
		out.Close()
		return err
	}
	written, copyErr := copyStreamWithProgress(ctx, out, io.LimitReader(in, remaining+1), progress)
	*copiedBytes += written
	if written > remaining {
		copyErr = errors.Join(copyErr, ErrTooLarge)
	}
	syncErr := out.Sync()
	closeErr := out.Close()
	return errors.Join(copyErr, syncErr, closeErr)
}

func copiedMode(info os.FileInfo) os.FileMode {
	mode := os.FileMode(0o600)
	if info.IsDir() || info.Mode().Perm()&0o100 != 0 {
		mode |= 0o100
	}
	return mode
}

func chmodCreatedDirectory(parent *os.Root, name string, mode os.FileMode) error {
	before, err := parent.Lstat(name)
	if err != nil {
		return err
	}
	directory, err := parent.OpenFile(name, os.O_RDONLY|regularOpenFlags(), 0)
	if err != nil {
		return err
	}
	defer directory.Close()
	after, err := directory.Stat()
	if err != nil {
		return err
	}
	if !after.IsDir() || !os.SameFile(before, after) {
		return ErrInvalidPath
	}
	err = chmodFile(directory, mode)
	if errors.Is(err, fs.ErrPermission) {
		return nil
	}
	return err
}

func copyStreamWithProgress(ctx context.Context, destination io.Writer, source io.Reader, progress func(int64)) (int64, error) {
	buffer := make([]byte, 128*1024)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		read, readErr := source.Read(buffer)
		if read > 0 {
			written, writeErr := destination.Write(buffer[:read])
			total += int64(written)
			if written > 0 && progress != nil {
				progress(int64(written))
			}
			if writeErr != nil {
				return total, writeErr
			}
			if written != read {
				return total, io.ErrShortWrite
			}
		}
		if errors.Is(readErr, io.EOF) {
			return total, nil
		}
		if readErr != nil {
			return total, readErr
		}
	}
}

func (r *Root) lstat(clean string, allowFinalSymlink, allowMissingFinal bool) (os.FileInfo, error) {
	parent, base, err := r.openParent(clean)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	info, err := parent.Lstat(base)
	if err != nil {
		if allowMissingFinal && errors.Is(err, fs.ErrNotExist) {
			return nil, fs.ErrNotExist
		}
		return nil, err
	}
	if info.Mode()&fs.ModeSymlink != 0 && !allowFinalSymlink {
		return nil, ErrInvalidPath
	}
	return info, nil
}

func (r *Root) Search(ctx context.Context, base, query string, includeHidden bool, limit int) (SearchResult, error) {
	clean, err := r.clean(base)
	if err != nil {
		return SearchResult{}, err
	}
	if err := r.validateComponents(clean, false, false); err != nil {
		return SearchResult{}, err
	}
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return SearchResult{}, errors.New("search query is empty")
	}
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	result := SearchResult{Entries: []Entry{}}
	seen := 0
	var walk func(string, bool) error
	walk = func(dir string, top bool) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if r.pseudoTarget(dir) {
			return nil
		}
		listing, err := r.List(PublicPath(dir), includeHidden)
		if err != nil {
			if !top && skippableChildError(err) {
				return nil
			}
			return fmt.Errorf("list %s: %w", PublicPath(dir), err)
		}
		for _, entry := range listing.Entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			seen++
			if seen > r.maxWalkEntries {
				return ErrWalkLimit
			}
			if strings.Contains(strings.ToLower(entry.Name), query) {
				if len(result.Entries) == limit {
					result.Truncated = true
					return nil
				}
				result.Entries = append(result.Entries, entry)
			}
			if entry.Directory && !entry.Symlink && !r.pseudoTarget(strings.TrimPrefix(entry.Path, "/")) {
				if err := walk(strings.TrimPrefix(entry.Path, "/"), false); err != nil {
					return err
				}
				if result.Truncated {
					return nil
				}
			}
		}
		return nil
	}
	if r.pseudoTarget(clean) {
		return result, nil
	}
	err = walk(clean, true)
	return result, err
}

func skippableChildError(err error) bool {
	return errors.Is(err, fs.ErrPermission) || errors.Is(err, fs.ErrNotExist) || errors.Is(err, ErrInvalidPath)
}

func (r *Root) Checksum(ctx context.Context, name string) (string, error) {
	f, _, err := r.OpenRegular(name)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	buf := make([]byte, 64*1024)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, readErr := f.Read(buf)
		if n > 0 {
			_, _ = h.Write(buf[:n])
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", readErr
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (r *Root) isPseudo(clean string) bool {
	if !r.advanced {
		return false
	}
	first := strings.SplitN(clean, "/", 2)[0]
	switch first {
	case "proc", "sys", "dev", "run":
		return true
	default:
		return false
	}
}

func (r *Root) loadPseudoDevices() {
	if !r.advanced {
		return
	}
	r.pseudoDevices = make(map[uint64]struct{})
	for _, name := range []string{"/proc", "/sys", "/dev", "/run"} {
		info, err := os.Stat(name)
		if err != nil {
			continue
		}
		if device, ok := fileDeviceID(info); ok {
			r.pseudoDevices[device] = struct{}{}
		}
	}
}

func (r *Root) pseudoInfo(info os.FileInfo) bool {
	if !r.advanced || info == nil {
		return false
	}
	device, ok := fileDeviceID(info)
	if !ok {
		return false
	}
	_, pseudo := r.pseudoDevices[device]
	return pseudo
}

// pseudoTarget classifies aliases by filesystem identity as well as by their
// displayed path. This catches relative symlinks and bind mounts of procfs,
// sysfs, devtmpfs, and the device's /run mount without hiding their listings.
func (r *Root) pseudoTarget(clean string) bool {
	if r.isPseudo(clean) {
		return true
	}
	if !r.advanced {
		return false
	}
	for candidate := clean; ; candidate = path.Dir(candidate) {
		info, err := r.root.Stat(candidate)
		if err == nil {
			return r.pseudoInfo(info)
		}
		if !errors.Is(err, fs.ErrNotExist) || candidate == "." {
			return false
		}
	}
}

func entryFromInfo(name string, info os.FileInfo) Entry {
	mode := info.Mode()
	typeName := "special"
	switch {
	case mode.IsDir():
		typeName = "directory"
	case mode.IsRegular():
		typeName = "file"
	case mode&fs.ModeSymlink != 0:
		typeName = "symlink"
	}
	size := info.Size()
	if size < 0 {
		size = 0
	}
	return Entry{Name: path.Base(name), Path: PublicPath(name), Size: size, ModifiedAt: info.ModTime().UTC(), Type: typeName, MIMEType: mime.TypeByExtension(filepath.Ext(name)), Hidden: strings.HasPrefix(path.Base(name), "."), Writable: mode.Perm()&0o222 != 0, Mode: mode.String(), Directory: mode.IsDir(), Regular: mode.IsRegular(), Symlink: mode&fs.ModeSymlink != 0}
}

func pathAbs(name string) (string, error) {
	abs, err := canonicalAbsolute(name)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("root is not a directory")
	}
	return abs, nil
}

func canonicalAbsolute(name string) (string, error) {
	abs, err := filepath.Abs(name)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	tail := make([]string, 0)
	for current := abs; ; current = filepath.Dir(current) {
		resolved, resolveErr := filepath.EvalSymlinks(current)
		if resolveErr == nil {
			for index := len(tail) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, tail[index])
			}
			return filepath.Clean(resolved), nil
		}
		if !errors.Is(resolveErr, fs.ErrNotExist) || current == filepath.Dir(current) {
			return "", resolveErr
		}
		tail = append(tail, filepath.Base(current))
	}
}
