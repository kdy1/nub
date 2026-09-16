package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s := New(filepath.Join(dir, "store/v1/files"), filepath.Join(dir, "cache"))
	t.Cleanup(func() { s.Close() })
	return s
}

func TestCASDeduplicationRecoveryAndModes(t *testing.T) {
	s := testStore(t)
	if got := Hash(nil); got != "af1349b9f5f9a1a6a0404dea36dcc9499bcb25c9adc112b7cc9a93cae41f3262" {
		t.Fatal(got)
	}
	data := []byte("shared content")
	first, err := s.ImportBytes(t.Context(), data, false)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.ImportBytes(t.Context(), data, true)
	if err != nil {
		t.Fatal(err)
	}
	if first.Path != second.Path || first.Executable || !second.Executable || *second.Size != uint64(len(data)) {
		t.Fatal(first, second)
	}
	if _, err := os.Stat(second.Path + "-exec"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(second.Path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0644 {
		t.Fatal(info.Mode())
	}
	if err := os.WriteFile(first.Path, []byte("torn"), 0644); err != nil {
		t.Fatal(err)
	}
	repaired, err := s.ImportBytes(t.Context(), data, false)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(repaired.Path)
	if err != nil || !bytes.Equal(actual, data) {
		t.Fatal(string(actual), err)
	}
	if _, err := s.FilePath("../../escape"); err == nil {
		t.Fatal("invalid hash accepted")
	}
	entries, _ := filepath.Glob(filepath.Join(s.Root, ".cas-*"))
	if len(entries) != 0 {
		t.Fatal(entries)
	}
}

func TestCASConcurrentImports(t *testing.T) {
	s := testStore(t)
	other := New(s.Root, s.CacheDir)
	defer other.Close()
	payload := bytes.Repeat([]byte("package contents"), 8192)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Go(func() {
			current := s
			if i%2 == 1 {
				current = other
			}
			file, err := current.ImportBytes(t.Context(), payload, i%3 == 0)
			if err != nil {
				t.Error(err)
				return
			}
			actual, err := os.ReadFile(file.Path)
			if err != nil || !bytes.Equal(actual, payload) {
				t.Errorf("partial CAS read: %d %v", len(actual), err)
			}
		})
	}
	wg.Wait()
}

type brokenReader struct{ sent bool }

func (r *brokenReader) Read(p []byte) (int, error) {
	if r.sent {
		return 0, io.ErrUnexpectedEOF
	}
	r.sent = true
	return copy(p, "partial"), nil
}
func TestCASInterruptedImport(t *testing.T) {
	s := testStore(t)
	if _, err := s.Import(t.Context(), &brokenReader{}, false); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatal(err)
	}
	path, _ := s.FilePath(Hash([]byte("partial")))
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	files, _ := os.ReadDir(s.Root)
	if len(files) != 0 {
		t.Fatal(files)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.ImportBytes(ctx, nil, false); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestStoreMaintenanceBlocksWriters(t *testing.T) {
	writer := testStore(t)
	if _, err := writer.ImportBytes(t.Context(), []byte("data"), false); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.LockMaintenance(t.Context()); err == nil {
		t.Fatal("upgraded writer lease")
	}
	maintenance := New(writer.Root, writer.CacheDir)
	defer maintenance.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 80*time.Millisecond)
	defer cancel()
	if _, err := maintenance.LockMaintenance(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	writer.Close()
	guard, err := maintenance.LockMaintenance(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()
	blocked := New(writer.Root, writer.CacheDir)
	defer blocked.Close()
	ctx2, cancel2 := context.WithTimeout(t.Context(), 80*time.Millisecond)
	defer cancel2()
	if _, err := blocked.ImportBytes(ctx2, []byte("later"), false); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	guard.Close()
	if _, err := blocked.ImportBytes(t.Context(), []byte("later"), false); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.ImportBytes(t.Context(), nil, false); !errors.Is(err, os.ErrClosed) {
		t.Fatal(err)
	}
}

func TestCASAcrossProcesses(t *testing.T) {
	if root := os.Getenv("PM_GO_CAS_HELPER"); root != "" {
		s := New(root, filepath.Join(filepath.Dir(root), "cache"))
		defer s.Close()
		for i := 0; i < 20; i++ {
			file, err := s.ImportBytes(t.Context(), bytes.Repeat([]byte("concurrent"), 4096), i%2 == 0)
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(file.Path)
			if err != nil || Hash(data) != file.Hash {
				t.Fatal("torn content", err)
			}
		}
		return
	}
	s := testStore(t)
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestCASAcrossProcesses$")
			cmd.Env = append(os.Environ(), "PM_GO_CAS_HELPER="+s.Root)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("CAS process: %v\n%s", err, out)
			}
		})
	}
	wg.Wait()
}

func TestIndexIntegrityIsolationAndCorruption(t *testing.T) {
	s := testStore(t)
	file, err := s.ImportBytes(t.Context(), []byte("manifest"), false)
	if err != nil {
		t.Fatal(err)
	}
	index := PackageIndex{"package.json": file}
	first, second := Integrity([]byte("first tarball")), Integrity([]byte("second tarball"))
	for _, integrity := range []*string{nil, &first, &second} {
		if err := s.SaveIndex(t.Context(), "@scope/pkg", "1.0.0", integrity, index); err != nil {
			t.Fatal(err)
		}
	}
	paths := map[string]bool{}
	for _, integrity := range []*string{nil, &first, &second} {
		path, _ := s.indexPath("@scope/pkg", "1.0.0", integrity, false)
		if paths[path] {
			t.Fatal("index alias", path)
		}
		paths[path] = true
		got, ok := s.LoadIndex("@scope/pkg", "1.0.0", integrity, true)
		if !ok || IndexFingerprint(got) != IndexFingerprint(index) {
			t.Fatal(got, ok)
		}
	}
	bad := "sha512-!!!"
	if err := s.SaveIndex(t.Context(), "x", "1.0.0", &bad, index); err == nil {
		t.Fatal("bad SRI accepted")
	}
	for _, pair := range [][2]string{{"../../bad", "1.0.0"}, {"x", "../bad"}, {"x", "1\x00"}, {"x", ""}} {
		if err := s.SaveIndex(t.Context(), pair[0], pair[1], nil, index); err == nil {
			t.Fatal(pair)
		}
	}
	p, _ := s.indexPath("@scope/pkg", "1.0.0", nil, false)
	os.WriteFile(p, []byte("truncated"), 0644)
	if _, ok := s.LoadIndex("@scope/pkg", "1.0.0", nil, true); ok {
		t.Fatal("corrupt JSON hit")
	}
	os.WriteFile(file.Path, nil, 0644)
	if _, ok := s.LoadIndex("@scope/pkg", "1.0.0", &first, true); ok {
		t.Fatal("truncated CAS hit")
	}
	if ok, err := s.InvalidateIndex(t.Context(), "@scope/pkg", "1.0.0", &second); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if ok, err := s.InvalidateIndex(t.Context(), "@scope/pkg", "1.0.0", &second); err != nil || ok {
		t.Fatal(ok, err)
	}
}

func TestReadOnlyFallback(t *testing.T) {
	global := testStore(t)
	file, err := global.ImportBytes(t.Context(), []byte("from shared store"), false)
	if err != nil {
		t.Fatal(err)
	}
	index := PackageIndex{"package.json": file}
	if err := global.SaveIndex(t.Context(), "pkg", "1.0.0", nil, index); err != nil {
		t.Fatal(err)
	}
	global.Close()
	local := testStore(t)
	local.ReadFallback = global.Root
	reused, err := local.ImportBytes(t.Context(), []byte("from shared store"), true)
	if err != nil || reused.Path != file.Path {
		t.Fatal(reused, err)
	}
	if _, err := os.Stat(file.Path + "-exec"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("wrote marker into fallback", err)
	}
	if _, ok := local.LoadIndex("pkg", "1.0.0", nil, true); !ok {
		t.Fatal("fallback index miss")
	}
	if ok, err := local.InvalidateIndex(t.Context(), "pkg", "1.0.0", nil); ok || err != nil {
		t.Fatal(ok, err)
	}
	if err := local.SaveIndex(t.Context(), "pkg", "1.0.0", nil, PackageIndex{}); err != nil {
		t.Fatal(err)
	}
	got, ok := local.LoadIndex("pkg", "1.0.0", nil, true)
	if !ok || len(got) != 0 {
		t.Fatal("primary did not shadow fallback", got, ok)
	}
	if got, ok := global.LoadIndex("pkg", "1.0.0", nil, true); !ok || len(got) != 1 {
		t.Fatal("fallback was modified", got, ok)
	}
}

func TestIndexFingerprintAndPaths(t *testing.T) {
	a := StoredFile{Hash: Hash([]byte("a"))}
	b := StoredFile{Hash: Hash([]byte("b"))}
	first := PackageIndex{"one": a, "two": b}
	second := PackageIndex{"two": b, "one": a}
	fingerprint := IndexFingerprint(first)
	if fingerprint != IndexFingerprint(second) {
		t.Fatal("unstable fingerprint")
	}
	b.Executable = true
	second["two"] = b
	if fingerprint == IndexFingerprint(second) {
		t.Fatal("mode ignored")
	}
	delete(second, "two")
	if fingerprint == IndexFingerprint(second) {
		t.Fatal("file set ignored")
	}
	for _, version := range []string{"1.0.0+abcdef0123456789", "1.0.0-beta.2", "v1"} {
		if !ValidVersion(version) {
			t.Fatal(version)
		}
	}
	s := testStore(t)
	outside := PackageIndex{"package.json": {Hash: Hash(nil), Path: filepath.Join(t.TempDir(), "outside")}}
	if err := s.SaveIndex(t.Context(), "x", "1", nil, outside); err == nil {
		t.Fatal("foreign CAS reference accepted")
	}
	path, _ := s.writePath(Hash(nil))
	for _, key := range []string{"../outside", "/absolute", "./x", "a/../x", "a\\b"} {
		index := PackageIndex{key: {Hash: Hash(nil), Path: path}}
		if err := s.SaveIndex(t.Context(), "x", "1", nil, index); err == nil {
			t.Fatal("unsafe key", key)
		}
	}
}

func TestStoredNativePath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix byte paths")
	}
	f := StoredFile{Hash: Hash(nil), Path: "/store/\xff", Executable: false}
	data, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "unixBytes") {
		t.Fatal(string(data))
	}
	var decoded StoredFile
	if err := json.Unmarshal(data, &decoded); err != nil || decoded.Path != f.Path {
		t.Fatal(decoded, err)
	}
	for _, data := range []string{`{}`, `{"hex_hash":"x","store_path":null,"executable":false}`, fmt.Sprintf(`{"hex_hash":"x","store_path":{"unixBytes":[%d]},"executable":false}`, 256)} {
		if err := json.Unmarshal([]byte(data), &decoded); err == nil {
			t.Fatal("bad stored file", data)
		}
	}
}
