package main

import (
	"path/filepath"
	"testing"

	"github.com/bitrise-io/go-utils/pathutil"
)

func TestNewArchive(t *testing.T) {
	tmpDir, err := pathutil.NormalizedOSTempDirPath("cache")
	if err != nil {
		t.Fatalf("failed to create tmp dir: %s", err)
		return
	}
	pth := filepath.Join(tmpDir, "cache.gzip")

	tests := []struct {
		name     string
		pth      string
		compress bool
		wantGzip bool
		wantErr  bool
	}{
		{
			name:     "no path provided",
			pth:      "",
			compress: false,
			wantGzip: false,
			wantErr:  true,
		},
		{
			name:     "no compress",
			pth:      pth,
			compress: false,
			wantGzip: false,
			wantErr:  false,
		},
		{
			name:     "compress",
			pth:      pth,
			compress: true,
			wantGzip: true,
			wantErr:  false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewArchive(tt.pth, tt.compress)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewArchive() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			hasGzip := got != nil && got.gzip != nil
			if tt.wantGzip != hasGzip {
				t.Errorf("NewArchive() has gzip = %v, want %v", hasGzip, tt.wantGzip)
			}
		})
	}
}

func TestArchive_Write(t *testing.T) {
	tmpDir, err := pathutil.NormalizedOSTempDirPath("cache")
	if err != nil {
		t.Fatalf("failed to create tmp dir: %s", err)
	}
	pth := filepath.Join(tmpDir, "cache.gzip")

	fileToArchive := filepath.Join(tmpDir, "file")
	createDirStruct(t, map[string]string{fileToArchive: ""})

	t.Log("no compress")
	{
		archive, err := NewArchive(pth, false)
		if err != nil {
			t.Fatalf("failed to create archive: %s", err)
		}

		if err := archive.Write(map[string]string{fileToArchive: "indicator"}); err != nil {
			t.Fatalf("failed to write archive: %s", err)
		}
	}

	t.Log("compress")
	{
		archive, err := NewArchive(pth, true)
		if err != nil {
			t.Fatalf("failed to create archive: %s", err)
		}

		if err := archive.Write(map[string]string{fileToArchive: "indicator"}); err != nil {
			t.Fatalf("failed to write archive: %s", err)
		}
	}
}

func TestArchive_WriteHeader(t *testing.T) {
	tmpDir, err := pathutil.NormalizedOSTempDirPath("cache")
	if err != nil {
		t.Fatalf("failed to create tmp dir: %s", err)
	}
	pth := filepath.Join(tmpDir, "cache.gzip")

	fileToArchive := filepath.Join(tmpDir, "file")
	createDirStruct(t, map[string]string{fileToArchive: ""})

	archive, err := NewArchive(pth, false)
	if err != nil {
		t.Fatalf("failed to create archive: %s", err)
	}

	if err := archive.WriteHeader(map[string]string{"file/to/cache": "indicator/file"}, cacheInfoFilePath); err != nil {
		t.Fatalf("failed to write archive header: %s", err)
	}
}

func TestArchive_Close(t *testing.T) {
	tmpDir, err := pathutil.NormalizedOSTempDirPath("cache")
	if err != nil {
		t.Fatalf("failed to create tmp dir: %s", err)
	}
	pth := filepath.Join(tmpDir, "cache.gzip")

	fileToArchive := filepath.Join(tmpDir, "file")
	createDirStruct(t, map[string]string{fileToArchive: ""})

	t.Log("no compress")
	{
		archive, err := NewArchive(pth, false)
		if err != nil {
			t.Fatalf("failed to create archive: %s", err)
		}

		if err := archive.Write(map[string]string{fileToArchive: ""}); err != nil {
			t.Fatalf("failed to write archive: %s", err)
		}

		if err := archive.Close(); err != nil {
			t.Fatalf("failed to close archive: %s", err)
		}
	}

	t.Log("compress")
	{
		archive, err := NewArchive(pth, true)
		if err != nil {
			t.Fatalf("failed to create archive: %s", err)
		}

		if err := archive.Write(map[string]string{fileToArchive: ""}); err != nil {
			t.Fatalf("failed to write archive: %s", err)
		}

		if err := archive.Close(); err != nil {
			t.Fatalf("failed to close archive: %s", err)
		}
	}
}
