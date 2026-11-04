package main

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	maxZipBytes      = 50 << 20
	maxUnpackedBytes = 200 << 20
)

func unpackZip(zipPath, dir string) error {
	if info, err := os.Stat(zipPath); err != nil {
		return err
	} else if info.Size() > maxZipBytes {
		return errors.New("The zip is larger than 50 MB.")
	}
	if existing, err := os.ReadDir(dir); err != nil {
		return err
	} else if len(existing) > 0 {
		return errors.New("The folder for the unpacked files is not empty.")
	}
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("The zip file is not valid: %w", err)
	}
	defer reader.Close()

	kept := make([]*zip.File, 0, len(reader.File))
	for _, entry := range reader.File {
		if dropsZipEntry(entry.Name) {
			continue
		}
		if entry.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("The zip has a symbolic link: %s.", entry.Name)
		}
		if !filepath.IsLocal(entry.Name) {
			return fmt.Errorf("The zip has a file outside its folder: %s.", entry.Name)
		}
		kept = append(kept, entry)
	}

	prefix := singleTopFolder(kept)
	total := 0
	for _, entry := range kept {
		name := strings.TrimSuffix(strings.TrimPrefix(entry.Name, prefix), "/")
		if name == "" {
			continue
		}
		target := filepath.Join(dir, name)
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := unpackZipEntry(entry, target, &total); err != nil {
			return err
		}
	}
	return nil
}

func unpackZipEntry(entry *zip.File, target string, total *int) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	mode := entry.Mode().Perm()
	if mode == 0 {
		mode = 0o644
	}
	file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	defer file.Close()
	source, err := entry.Open()
	if err != nil {
		return fmt.Errorf("The zip file is not valid: %w", err)
	}
	defer source.Close()
	written, copyErr := io.Copy(file, io.LimitReader(source, int64(maxUnpackedBytes-*total)))
	*total += int(written)
	var probe [1]byte
	probeN, probeErr := source.Read(probe[:])
	if probeN > 0 {
		return errors.New("The zip unpacks to more than 200 MB.")
	}
	if copyErr != nil {
		return fmt.Errorf("The zip file is not valid: %w", copyErr)
	}
	if probeErr != nil && !errors.Is(probeErr, io.EOF) {
		return fmt.Errorf("The zip file is not valid: %w", probeErr)
	}
	return nil
}

func dropsZipEntry(name string) bool {
	for _, part := range strings.Split(strings.TrimSuffix(name, "/"), "/") {
		if part == ".git" || part == "__MACOSX" {
			return true
		}
	}
	return filepath.Base(name) == ".DS_Store"
}

func singleTopFolder(entries []*zip.File) string {
	top := ""
	for _, entry := range entries {
		name := strings.TrimSuffix(entry.Name, "/")
		first, _, found := strings.Cut(name, "/")
		if !found && !entry.FileInfo().IsDir() {
			return ""
		}
		if top == "" {
			top = first
		} else if top != first {
			return ""
		}
	}
	if top == "" {
		return ""
	}
	return top + "/"
}
