package main

import (
	"archive/zip"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeZip(t *testing.T, path string, entries map[string]string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	archive := zip.NewWriter(file)
	for _, name := range slices.Sorted(maps.Keys(entries)) {
		entry, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(entries[name])); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
}

func unpackTestZip(t *testing.T, entries map[string]string) (string, error) {
	t.Helper()
	zipPath := filepath.Join(t.TempDir(), "app.zip")
	writeZip(t, zipPath, entries)
	dir := t.TempDir()
	return dir, unpackZip(zipPath, dir)
}

func readFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(strings.TrimPrefix(path, dir+"/"))] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestUnpackZipWritesTheFiles(t *testing.T) {
	dir, err := unpackTestZip(t, map[string]string{"Dockerfile": "FROM ruby\n", "app/": "", "app/main.rb": "run\n"})
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]string{"Dockerfile": "FROM ruby\n", "app/main.rb": "run\n"}
	if got := readFiles(t, dir); !maps.Equal(got, want) {
		t.Errorf("unpacked %v", got)
	}
}

func TestUnpackZipDropsASingleTopFolder(t *testing.T) {
	dir, err := unpackTestZip(t, map[string]string{
		"leave-tracker/": "", "leave-tracker/Dockerfile": "FROM ruby\n", "leave-tracker/app/main.rb": "run\n"})
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]string{"Dockerfile": "FROM ruby\n", "app/main.rb": "run\n"}
	if got := readFiles(t, dir); !maps.Equal(got, want) {
		t.Errorf("unpacked %v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "leave-tracker")); err == nil {
		t.Error("the top folder was kept")
	}
}

func TestUnpackZipKeepsTwoTopFolders(t *testing.T) {
	dir, err := unpackTestZip(t, map[string]string{"a/x": "1", "b/y": "2"})
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]string{"a/x": "1", "b/y": "2"}
	if got := readFiles(t, dir); !maps.Equal(got, want) {
		t.Errorf("unpacked %v", got)
	}
}

func TestUnpackZipRefusesDotDot(t *testing.T) {
	dir, err := unpackTestZip(t, map[string]string{"../evil": "bad"})
	if err == nil || err.Error() != "The zip has a file outside its folder: ../evil." {
		t.Fatalf("error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "evil")); err == nil {
		t.Error("unpackZip wrote outside the folder")
	}
}

func TestUnpackZipRefusesDotDotThatLeavesTheTopFolder(t *testing.T) {
	dir, err := unpackTestZip(t, map[string]string{"leave-tracker/Dockerfile": "FROM ruby\n", "leave-tracker/../escaped": "bad"})
	if err == nil || err.Error() != "The zip has a file outside its folder: leave-tracker/../escaped." {
		t.Fatalf("error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "escaped")); err == nil {
		t.Error("unpackZip wrote outside the folder")
	}
}

func TestUnpackZipRefusesAnAbsolutePath(t *testing.T) {
	_, err := unpackTestZip(t, map[string]string{"/evil.txt": "bad"})
	if err == nil || err.Error() != "The zip has a file outside its folder: /evil.txt." {
		t.Fatalf("error: %v", err)
	}
}

func TestUnpackZipRefusesASymlink(t *testing.T) {
	zipPath := filepath.Join(t.TempDir(), "app.zip")
	file, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	header := &zip.FileHeader{Name: "link"}
	header.SetMode(os.ModeSymlink | 0o644)
	entry, err := archive.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte("/etc/passwd")); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	err = unpackZip(zipPath, dir)
	if err == nil || err.Error() != "The zip has a symbolic link: link." {
		t.Fatalf("error: %v", err)
	}
	if got := readFiles(t, dir); len(got) > 0 {
		t.Errorf("unpacked %v", got)
	}
}

func TestUnpackZipRefusesAZipOver50MB(t *testing.T) {
	zipPath := filepath.Join(t.TempDir(), "big.zip")
	file, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	entry, err := archive.CreateHeader(&zip.FileHeader{Name: "big.bin", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	chunk := make([]byte, 1<<20)
	for range maxZipBytes/len(chunk) + 2 {
		if _, err := entry.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	err = unpackZip(zipPath, t.TempDir())
	if err == nil || err.Error() != "The zip is larger than 50 MB." {
		t.Fatalf("error: %v", err)
	}
}

func TestUnpackZipStopsAtTheUnpackedLimit(t *testing.T) {
	zipPath := filepath.Join(t.TempDir(), "big.zip")
	file, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	entry, err := archive.Create("big.bin")
	if err != nil {
		t.Fatal(err)
	}
	chunk := make([]byte, 1<<20)
	for range maxUnpackedBytes/len(chunk) + 1 {
		if _, err := entry.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	err = unpackZip(zipPath, dir)
	if err == nil || err.Error() != "The zip unpacks to more than 200 MB." {
		t.Fatalf("error: %v", err)
	}
	info, statErr := os.Stat(filepath.Join(dir, "big.bin"))
	if statErr != nil || info.Size() > maxUnpackedBytes {
		t.Fatalf("big.bin: stat %v, %v bytes", statErr, info)
	}
}

func TestUnpackZipDropsGitAndMacFiles(t *testing.T) {
	dir, err := unpackTestZip(t, map[string]string{
		".git/config": "x", "__MACOSX/leave-tracker/._Dockerfile": "junk", "leave-tracker/": "",
		"leave-tracker/Dockerfile": "FROM ruby\n", "leave-tracker/a/.DS_Store": "x"})
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]string{"Dockerfile": "FROM ruby\n"}
	if got := readFiles(t, dir); !maps.Equal(got, want) {
		t.Errorf("unpacked %v", got)
	}
}

func TestUnpackZipMakesAScriptExecutableInAZipWithNoUnixModes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "windows.zip")
	writeZip(t, path, map[string]string{"bin/run": "#!/bin/sh\necho hi\n", "Dockerfile": "FROM ruby\n"})
	dir := t.TempDir()

	if err := unpackZip(path, dir); err != nil {
		t.Fatal(err)
	}

	script, err := os.Stat(filepath.Join(dir, "bin/run"))
	if err != nil {
		t.Fatal(err)
	}
	if script.Mode().Perm() != 0o755 {
		t.Errorf("bin/run mode: %o", script.Mode().Perm())
	}
	plain, err := os.Stat(filepath.Join(dir, "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	if plain.Mode().Perm()&0o111 != 0 {
		t.Errorf("Dockerfile mode: %o", plain.Mode().Perm())
	}
}

func TestUnpackZipRefusesAFolderThatIsNotEmpty(t *testing.T) {
	zipPath := filepath.Join(t.TempDir(), "app.zip")
	writeZip(t, zipPath, map[string]string{"Dockerfile": "FROM ruby\n"})
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "old.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := unpackZip(zipPath, dir)
	if err == nil || err.Error() != "The folder for the unpacked files is not empty." {
		t.Fatalf("error: %v", err)
	}
}
