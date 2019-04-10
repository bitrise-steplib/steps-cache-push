package main

import (
	"archive/tar"
	"compress/gzip"
	"io/ioutil"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

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

		if err := archive.Write([]string{fileToArchive}); err != nil {
			t.Fatalf("failed to write archive: %s", err)
		}
	}

	t.Log("compress")
	{
		archive, err := NewArchive(pth, true)
		if err != nil {
			t.Fatalf("failed to create archive: %s", err)
		}

		if err := archive.Write([]string{fileToArchive}); err != nil {
			t.Fatalf("failed to write archive: %s", err)
		}
	}
}

func TestArchive_WriteFirstFile(t *testing.T) {
	// make it very seeded
	rand.Seed(time.Now().UnixNano())

	// return random string with N length
	randomString := func(n int) string {
		letterRunes := []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")
		b := make([]rune, n)
		for i := range b {
			b[i] = letterRunes[rand.Intn(len(letterRunes))]
		}
		return string(b)
	}

	// get the unordered list of map keys
	keys := func(mymap map[string]string) (ret []string) {
		for k := range mymap {
			ret = append(ret, k)
		}
		return
	}

	// work in a tmp dir
	tmpDir, err := pathutil.NormalizedOSTempDirPath("cache")
	if err != nil {
		t.Fatalf("failed to create tmp dir: %s", err)
	}

	// the archive to write and read from
	archivePath := filepath.Join(tmpDir, "cache.gzip")

	// the files and structure  to be archived
	structure := map[string]string{
		filepath.Join(tmpDir, "file1"):           "dummmy",
		filepath.Join(tmpDir, "test/file1"):      "dummmy",
		filepath.Join(tmpDir, "dir/ok/file1"):    "dummmy",
		filepath.Join(tmpDir, "ok/bigfile"):      randomString(1024 * 1024),
		filepath.Join(tmpDir, "bigfile2"):        randomString(1024 * 1024),
		filepath.Join(tmpDir, "dir/ok/lastfile"): "I'am the last file",
	}

	pathsInArchiveSturcture := keys(structure)

	// create the file structure on the FS
	createDirStruct(t, structure)

	t.Log("no compress")
	{
		// create archive
		archive, err := NewArchive(archivePath, false)
		if err != nil {
			t.Fatalf("failed to create archive: %s", err)
		}

		// write all the file sin the archive
		if err := archive.Write(pathsInArchiveSturcture); err != nil {
			t.Fatalf("failed to write archive: %s", err)
		}

		// close the archive
		if err := archive.Close(); err != nil {
			t.Fatalf("failed to close archive: %s", err)
		}

		// open the archive for reading
		archiveFile, err := os.Open(archivePath)
		if err != nil {
			t.Fatalf("failed to open archive: %s", err)
		}

		// check bytes read
		currentOffset, err := archiveFile.Seek(0, 1)
		if err != nil {
			t.Fatalf("failed to get file index: %s", err)
		}
		t.Log("initial bytes read:", currentOffset)

		// utilize the tar reader
		tr := tar.NewReader(archiveFile)

		// advance to the next entry in the tar archive
		if _, err := tr.Next(); err != nil {
			t.Fatalf("failed to read next header: %s", err)
		}

		// read the current header file
		b, err := ioutil.ReadAll(tr)
		if err != nil {
			t.Fatalf("failed to read from archive: %s", err)
		}

		// compare if the first read file is the first file added to the archive
		if string(b) != structure[pathsInArchiveSturcture[0]] {
			t.Fatal("NOT FIRST FILE", string(b), structure[pathsInArchiveSturcture[0]])
		}

		// check bytes read after first file read
		currentOffset, err = archiveFile.Seek(0, 1)
		if err != nil {
			t.Fatalf("failed to get file index: %s", err)
		}
		t.Log("after first file read:", currentOffset)

		// check archive file size
		fi, err := archiveFile.Stat()
		if err != nil {
			t.Fatalf("failed to get file stat: %s", err)
		}
		t.Log("archive size:", fi.Size())
		t.Log("read file size:", len([]byte(structure[pathsInArchiveSturcture[0]])))

		// need to know that tar header uses a 512-byte blocking
		// https://www.gnu.org/software/tar/manual/html_node/Blocking.html
		tarHeaderBlockSize := 512

		if len([]byte(structure[pathsInArchiveSturcture[0]])) != int(currentOffset)-tarHeaderBlockSize {
			t.Fatal("invalid read")
		}
	}

	t.Log("compress")
	{
		archivePath = filepath.Join(tmpDir, "cache-compressed.gzip")

		// create archive
		archive, err := NewArchive(archivePath, true)
		if err != nil {
			t.Fatalf("failed to create archive: %s", err)
		}

		// write all the file sin the archive
		if err := archive.Write(pathsInArchiveSturcture); err != nil {
			t.Fatalf("failed to write archive: %s", err)
		}

		// close the archive
		if err := archive.Close(); err != nil {
			t.Fatalf("failed to close archive: %s", err)
		}

		// open the archive for reading
		archiveFile, err := os.Open(archivePath)
		if err != nil {
			t.Fatalf("failed to open archive: %s", err)
		}

		// check bytes read
		currentOffset, err := archiveFile.Seek(0, 1)
		if err != nil {
			t.Fatalf("failed to get file index: %s", err)
		}
		t.Log("initial bytes read:", currentOffset)

		// utilize gzip reader
		gzr, err := gzip.NewReader(archiveFile)
		if err != nil {
			t.Fatalf("failed to uncompress archive: %s", err)
		}
		defer gzr.Close()

		// utilize the tar reader
		tr := tar.NewReader(gzr)

		// advance to the next entry in the tar archive
		if _, err := tr.Next(); err != nil {
			t.Fatalf("failed to read next header: %s", err)
		}

		// read the current header file
		b, err := ioutil.ReadAll(tr)
		if err != nil {
			t.Fatalf("failed to read from archive: %s", err)
		}

		// compare if the first read file is the first file added to the archive
		if string(b) != structure[pathsInArchiveSturcture[0]] {
			t.Fatal("NOT FIRST FILE", string(b), structure[pathsInArchiveSturcture[0]])
		}

		// check bytes read after first file read
		currentOffset, err = archiveFile.Seek(0, 1)
		if err != nil {
			t.Fatalf("failed to get file index: %s", err)
		}
		t.Log("after first file read:", currentOffset)

		// check archive file size
		fi, err := archiveFile.Stat()
		if err != nil {
			t.Fatalf("failed to get file stat: %s", err)
		}
		t.Log("archive size:", fi.Size())
		t.Log("read file size:", len([]byte(structure[pathsInArchiveSturcture[0]])))

		// cannot compare bytes read len to the file size in case of compressed archives
	}

	// t.FailNow() // to see the test logs
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

		if err := archive.Write([]string{fileToArchive}); err != nil {
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

		if err := archive.Write([]string{fileToArchive}); err != nil {
			t.Fatalf("failed to write archive: %s", err)
		}

		if err := archive.Close(); err != nil {
			t.Fatalf("failed to close archive: %s", err)
		}
	}
}
