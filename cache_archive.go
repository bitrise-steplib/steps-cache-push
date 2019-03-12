// Cache archive related models and functions.
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/bitrise-io/go-utils/command"
	"github.com/bitrise-io/go-utils/log"
)

// Archive ...
type Archive struct {
	file *os.File
	tar  *tar.Writer
	gzip *gzip.Writer
}

// NewArchive ...
func NewArchive(pth string, compress bool) (*Archive, error) {
	file, err := os.Create(pth)
	if err != nil {
		return nil, err
	}

	var tarWriter *tar.Writer
	var gzipWriter *gzip.Writer
	if compress {
		gzipWriter, err = gzip.NewWriterLevel(file, gzip.BestCompression)
		if err != nil {
			return nil, err
		}

		tarWriter = tar.NewWriter(gzipWriter)
	} else {
		tarWriter = tar.NewWriter(file)
	}
	return &Archive{
		file: file,
		tar:  tarWriter,
		gzip: gzipWriter,
	}, nil
}

// Write ...
func (a Archive) Write(pths []string) error {
	for _, pth := range pths {
		info, err := os.Lstat(pth)

		var link string
		if info.Mode()&os.ModeSymlink != 0 {
			link, err = os.Readlink(pth)
			if err != nil {
				return err
			}
		}

		header, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}

		header.Name = pth
		header.ModTime = info.ModTime()

		if err := a.tar.WriteHeader(header); err != nil {
			return err
		}

		file, err := os.Open(pth)
		if err != nil {
			return err
		}

		defer func() {
			if err := file.Close(); err != nil {
				log.Errorf("Failed to close file (%s): %s", pth, err)
			}
		}()

		_, err = io.CopyN(a.tar, file, info.Size())
		if err != nil && err != io.EOF {
			return err
		}
	}

	return nil
}

// WriteHeader ...
func (a *Archive) WriteHeader(descriptor map[string]string) error {
	b, err := json.Marshal(descriptor)
	if err != nil {
		return err
	}

	closingHeader := &tar.Header{
		Name:     cacheInfoFilePath,
		Size:     int64(len(b)),
		Typeflag: tar.TypeReg,
		Mode:     0600,
		ModTime:  time.Now(),
	}

	if err := a.tar.WriteHeader(closingHeader); err != nil {
		return err
	}

	if _, err := io.Copy(a.tar, bytes.NewReader(b)); err != nil && err != io.EOF {
		return err
	}
	return nil
}

// Close ...
func (a *Archive) Close() error {
	if err := a.tar.Close(); err != nil {
		return err
	}

	if a.gzip != nil {
		if err := a.gzip.Close(); err != nil {
			return err
		}
	}

	return a.file.Close()
}

func uploadArchive(pth, url string) error {
	fi, err := os.Stat(pth)
	if err != nil {
		return fmt.Errorf("Failed to get File Infos of Archive (%s): %s", pth, err)
	}
	sizeInBytes := fi.Size()
	log.Printf("Archive file size: %d bytes / %f MB", sizeInBytes, (float64(sizeInBytes) / 1024.0 / 1024.0))

	uploadURL, err := getCacheUploadURL(url, sizeInBytes)
	if err != nil {
		return fmt.Errorf("Failed to generate Upload URL: %s", err)
	}

	if err := tryToUploadArchive(uploadURL, pth); err != nil {
		fmt.Println()
		log.Warnf("First upload attempt failed, retrying...")
		fmt.Println()
		time.Sleep(3000 * time.Millisecond)
		return tryToUploadArchive(uploadURL, pth)
	}
	return nil
}

func getCacheUploadURL(cacheAPIURL string, fileSizeInBytes int64) (string, error) {
	if strings.HasPrefix(cacheAPIURL, "file://") {
		return cacheAPIURL, nil
	}

	req, err := http.NewRequest("POST", cacheAPIURL, bytes.NewReader([]byte(fmt.Sprintf(`{"file_size_in_bytes": %d}`, fileSizeInBytes))))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %s", err)
	}

	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %s", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Warnf("Failed to close response body: %s", err)
		}
	}()

	if resp.StatusCode < 200 || resp.StatusCode > 202 {
		return "", fmt.Errorf("Upload URL was rejected (http-code:%d)", resp.StatusCode)
	}

	var respModel map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&respModel); err != nil {
		return "", fmt.Errorf("Failed to decode response body, error: %+v", err)
	}

	uploadURL, ok := respModel["upload_url"]
	if !ok {
		return "", fmt.Errorf("Request sent, but Upload URL isn't received")
	}

	if uploadURL == "" {
		return "", fmt.Errorf("Request sent, but Upload URL is empty (http-code:%d)", resp.StatusCode)
	}

	return uploadURL, nil
}

func tryToUploadArchive(uploadURL string, archiveFilePath string) error {
	if strings.HasPrefix(uploadURL, "file://") {
		pth := strings.TrimPrefix(uploadURL, "file://")
		return command.CopyFile(archiveFilePath, pth)
	}

	archFile, err := os.Open(archiveFilePath)
	if err != nil {
		return fmt.Errorf("Failed to open archive file for upload (%s): %s", archiveFilePath, err)
	}

	fileClosed := false
	defer func() {
		if fileClosed {
			return
		}
		if err := archFile.Close(); err != nil {
			log.Printf(" (!) Failed to close archive file (%s): %s", archiveFilePath, err)
		}
	}()

	fileInfo, err := archFile.Stat()
	if err != nil {
		return fmt.Errorf("Failed to get File Stats of the Archive file (%s): %s", archiveFilePath, err)
	}
	fileSize := fileInfo.Size()

	req, err := http.NewRequest("PUT", uploadURL, archFile)
	if err != nil {
		return fmt.Errorf("Failed to create upload request: %s", err)
	}

	req.Header.Add("Content-Length", strconv.FormatInt(fileSize, 10))
	req.ContentLength = fileSize

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("Failed to upload: %s", err)
	}

	if resp.StatusCode != 200 {
		return fmt.Errorf("Failed to upload file, response code was: %d", resp.StatusCode)
	}

	fileClosed = true

	return nil
}
