package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/bitrise-io/go-utils/fileutil"

	"github.com/bitrise-io/depman/pathutil"
	"github.com/bitrise-io/go-utils/log"
	glob "github.com/ryanuber/go-glob"
)

const (
	// testing
	testDebugMode = "false"
	testPaths     = `/Users/tamaspapik/Downloads/23953/c-1`
	testIgnores   = `*.h
*.m`

	fileIndicatorSeparator = "->"
	cacheArchivePath       = "/tmp/cache-archive.tar"
	cacheInfoFilePath      = "/tmp/cache-info.json"

	fingerprintMethodIDContentChecksum = "file-content-hash"
	fingerprintMethodIDFileModTime     = "file-mod-time"
)

// ChangeIndicator ...
type ChangeIndicator string

const (
	// MD5 ...
	MD5 = ChangeIndicator(fingerprintMethodIDContentChecksum)
	// MODTIME ...
	MODTIME = ChangeIndicator(fingerprintMethodIDFileModTime)
)

// StoreMode ...
type StoreMode int

const (
	// STORE ...
	STORE = StoreMode(0)
	// REMOVE ...
	REMOVE = StoreMode(1)
	// SKIP ...
	SKIP = StoreMode(2)
	// INDICATOR ...
	INDICATOR = StoreMode(3)
)

// CacheUploadAPIRequestDataModel ...
type CacheUploadAPIRequestDataModel struct {
	FileSizeInBytes int64 `json:"file_size_in_bytes"`
}

// GenerateUploadURLRespModel ...
type GenerateUploadURLRespModel struct {
	UploadURL string `json:"upload_url"`
}

// CacheModel ...
type CacheModel struct {
	PathList            []string
	IgnoreList          []string
	FilePathMap         map[string]string
	PreviousFilePathMap map[string]string
	IndicatorHashMap    map[string]string
	TarWriter           *tar.Writer
	GzipWriter          *gzip.Writer
	TarFile             *os.File
	DebugMode           bool
	FileChangeIndicator ChangeIndicator
	CompressArchive     bool
}

// ConfigsModel ...
type ConfigsModel struct {
	Paths                 string
	IgnoredPaths          string
	GlobalCachePathList   string
	GlobalCacheIgnoreList string
	DebugMode             string
	CacheAPIURL           string
	FingerprintMethodID   string
	CompressArchive       string
}

func (configs *ConfigsModel) print() {
	log.Printf("- CachePaths:\n%s", configs.Paths)
	log.Printf("- IgnoredPaths:\n%s", configs.IgnoredPaths)
	log.Printf("- CompressArchive: %s", configs.CompressArchive)
	log.Printf("- FingerprintMethodID: %s", configs.FingerprintMethodID)
}

func createConfigsModelFromEnvs() *ConfigsModel {
	return &ConfigsModel{
		Paths:                 os.Getenv("cache_paths"),
		IgnoredPaths:          os.Getenv("ignore_check_on_paths"),
		GlobalCachePathList:   os.Getenv("bitrise_cache_include_paths"),
		GlobalCacheIgnoreList: os.Getenv("bitrise_cache_exclude_paths"),
		DebugMode:             os.Getenv("is_debug_mode"),
		CacheAPIURL:           os.Getenv("cache_api_url"),
		FingerprintMethodID:   os.Getenv("fingerprint_method"),
		CompressArchive:       os.Getenv("compress_archive"),
	}
}

// NewCacheModel ...
func NewCacheModel(configs *ConfigsModel) *CacheModel {
	splittedPaths := strings.Split(configs.Paths, "\n")
	splittedIgnoredPaths := strings.Split(configs.IgnoredPaths, "\n")

	return &CacheModel{
		PathList:            splittedPaths,
		IgnoreList:          splittedIgnoredPaths,
		FilePathMap:         map[string]string{},
		IndicatorHashMap:    map[string]string{},
		PreviousFilePathMap: map[string]string{},
		DebugMode:           configs.DebugMode == "true",
		FileChangeIndicator: ChangeIndicator(configs.FingerprintMethodID),
		CompressArchive:     configs.CompressArchive == "true",
	}
}

// CreateTarArchive ...
func (cacheModel *CacheModel) CreateTarArchive() error {
	tarFile, err := os.Create(cacheArchivePath)
	if err != nil {
		return err
	}

	if cacheModel.CompressArchive {
		gw, err := gzip.NewWriterLevel(tarFile, gzip.BestCompression)
		if err != nil {
			return err
		}
		defer gw.Close()
		cacheModel.TarWriter = tar.NewWriter(gw)
		cacheModel.GzipWriter = gw
	} else {
		cacheModel.TarWriter = tar.NewWriter(tarFile)
	}
	cacheModel.TarFile = tarFile

	return nil
}

// CloseTarArchive ...
func (cacheModel *CacheModel) CloseTarArchive() error {
	filePathMapBytes, err := json.Marshal(cacheModel.FilePathMap)
	if err != nil {
		return err
	}

	filePathMapSize := int64(len(filePathMapBytes))

	closingHeader := &tar.Header{
		Name:     cacheInfoFilePath,
		Size:     filePathMapSize,
		Typeflag: tar.TypeReg,
		Mode:     0600,
		ModTime:  time.Now(),
	}

	if err := cacheModel.TarWriter.WriteHeader(closingHeader); err != nil {
		return err
	}

	if _, err := io.CopyN(cacheModel.TarWriter, bytes.NewReader(filePathMapBytes), filePathMapSize); err != nil && err != io.EOF {
		return err
	}

	if err := cacheModel.TarWriter.Close(); err != nil {
		return err
	}

	if cacheModel.CompressArchive {
		if err := cacheModel.GzipWriter.Close(); err != nil {
			return err
		}
	}

	if err := cacheModel.TarFile.Close(); err != nil {
		return err
	}

	return nil
}

// GenerateCacheInfoMAP ...
func (cacheModel *CacheModel) GenerateCacheInfoMAP() (map[string]string, error) {
	filePathMap := map[string]string{}

	for _, cachePath := range cacheModel.PathList {

		if err := filepath.Walk(cachePath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			if filepath.Dir(path) == filepath.Dir(cachePath) {
				return nil
			}

			header, err := tar.FileInfoHeader(info, path)
			if err != nil {
				return err
			}

			header.Name = path

			if info.IsDir() {
				header.Name += "/"
			}

			storeMode, indicatorFileMD5 := cacheModel.GetStoreMode(path)

			if storeMode == REMOVE {
				return nil
			}

			if info.IsDir() {
				return nil
			}

			if header.Typeflag == tar.TypeReg {
				switch storeMode {
				case STORE:
					if cacheModel.FileChangeIndicator == MD5 {
						fileMD5, err := getFileContentMD5(path)
						if err != nil {
							return err
						}
						filePathMap[path] = fileMD5
					} else if cacheModel.FileChangeIndicator == MODTIME {
						filePathMap[path] = fmt.Sprintf("%d", info.ModTime().Unix())
					}
				case SKIP:
					filePathMap[path] = "-"
				case INDICATOR:
					filePathMap[path] = indicatorFileMD5
				}
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}
	return filePathMap, nil
}

// ProcessFiles ...
func (cacheModel *CacheModel) ProcessFiles() error {

	for _, cachePath := range cacheModel.PathList {

		if err := filepath.Walk(cachePath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			if filepath.Dir(path) == filepath.Dir(cachePath) {
				return nil
			}

			header, err := tar.FileInfoHeader(info, path)
			if err != nil {
				return err
			}

			header.Name = path

			if info.IsDir() {
				header.Name += "/"
			}

			storeMode, indicatorFileMD5 := cacheModel.GetStoreMode(path)

			if storeMode == REMOVE {
				return nil
			}

			if header.Typeflag == tar.TypeReg {
				fStat := info.Sys().(*syscall.Stat_t)
				header.AccessTime = timespecToTime(fStat.Atimespec)
				header.ModTime = timespecToTime(fStat.Mtimespec)
				header.ChangeTime = timespecToTime(fStat.Ctimespec)
			}

			if err := cacheModel.TarWriter.WriteHeader(header); err != nil {
				return err
			}

			if info.IsDir() {
				return nil
			}

			if header.Typeflag == tar.TypeReg {
				switch storeMode {
				case STORE:
					if cacheModel.FileChangeIndicator == MD5 {
						fileMD5, err := getFileContentMD5(path)
						if err != nil {
							return err
						}
						cacheModel.FilePathMap[path] = fileMD5
					} else if cacheModel.FileChangeIndicator == MODTIME {
						cacheModel.FilePathMap[path] = fmt.Sprintf("%d", info.ModTime().Unix())
					}
				case SKIP:
					cacheModel.FilePathMap[path] = "-"
				case INDICATOR:
					cacheModel.FilePathMap[path] = indicatorFileMD5
				}

				file, err := os.Open(path)
				if err != nil {
					return err
				}

				defer func() error {
					if err := file.Close(); err != nil {
						return err
					}
					return nil
				}()

				_, err = io.CopyN(cacheModel.TarWriter, file, info.Size())
				if err != nil && err != io.EOF {
					return err
				}
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

// GetStoreMode ...
func (cacheModel *CacheModel) GetStoreMode(path string) (StoreMode, string) {
	for key, value := range cacheModel.IndicatorHashMap {
		if strings.HasPrefix(path, key) {
			return INDICATOR, value
		}
	}

	for _, ignoreFilter := range cacheModel.IgnoreList {
		ignoreFromArchive := strings.HasPrefix(ignoreFilter, "!")
		cleanedIgnoreFilter := strings.TrimSpace(strings.TrimPrefix(ignoreFilter, "!"))

		if strings.Contains(cleanedIgnoreFilter, "*") {
			if glob.Glob(cleanedIgnoreFilter, path) {
				if ignoreFromArchive {
					return REMOVE, ""
				}
				return SKIP, ""
			}
		} else {
			if strings.HasPrefix(path, cleanedIgnoreFilter) {
				if ignoreFromArchive {
					return REMOVE, ""
				}
				return SKIP, ""
			}
		}
	}
	return STORE, ""
}

// LoadPreviousFilePathMap ...
func (cacheModel *CacheModel) LoadPreviousFilePathMap() (bool, error) {
	exists, err := pathutil.IsPathExists(cacheInfoFilePath)
	if err != nil {
		return false, err
	}
	if !exists {
		return false, nil
	}

	fileBytes, err := fileutil.ReadBytesFromFile(cacheInfoFilePath)

	err = json.Unmarshal(fileBytes, &cacheModel.PreviousFilePathMap)
	if err != nil {
		return false, err
	}

	return true, nil
}

// CompareFilePathMaps ...
func (cacheModel *CacheModel) CompareFilePathMaps(currentFilePathsMap map[string]string) (bool, error) {
	triggerNewCache := false
	logLineCount := 0
	for prevKey, prevValue := range cacheModel.PreviousFilePathMap {
		currentValue, ok := currentFilePathsMap[prevKey]
		if !ok {
			log.Warnf("REMOVED: %s", prevKey)
			if prevValue == "-" {
				log.Donef("- Ignored")
			} else {
				triggerNewCache = true
				if !cacheModel.DebugMode {
					if logLineCount >= 9 {
						log.Printf("[List truncated, turn on DebugMode to see the whole change list]")
						return true, nil
					}
				}
				logLineCount++
			}
		} else {
			if currentValue != prevValue {
				log.Warnf("CHANGED: %s, Current: %s != Previous: %s", prevKey, currentValue, prevValue)
				triggerNewCache = true
				if !cacheModel.DebugMode {
					if logLineCount >= 9 {
						log.Printf("[List truncated, turn on DebugMode to see the whole change list]")
						return true, nil
					}
				}
				logLineCount++
			}
		}
		delete(cacheModel.PreviousFilePathMap, prevKey)
		delete(currentFilePathsMap, prevKey)
	}

	for remainingKey, remainingValue := range currentFilePathsMap {
		log.Warnf("ADDED: %s", remainingKey)
		if remainingValue != "-" {
			triggerNewCache = true
			if !cacheModel.DebugMode {
				if logLineCount >= 9 {
					log.Printf("[List truncated, turn on DebugMode to see the whole change list]")
					return true, nil
				}
			}
			logLineCount++
		} else {
			log.Donef("- Ignored")
		}
	}

	return triggerNewCache, nil
}

// CleanPaths ...
func (cacheModel *CacheModel) CleanPaths() error {
	cleanedPathList := []string{}

	for _, path := range cacheModel.PathList {
		if strings.Contains(path, fileIndicatorSeparator) {
			splittedPath := strings.Split(path, fileIndicatorSeparator)
			cleanPath := strings.TrimSpace(splittedPath[0])

			indicatorFileChangeIndicator := ""
			var err error

			if cacheModel.FileChangeIndicator == MD5 {
				indicatorFileChangeIndicator, err = getFileContentMD5(strings.TrimSpace(splittedPath[1]))
				if err != nil {
					return err
				}
			} else if cacheModel.FileChangeIndicator == MODTIME {
				fi, err := os.Stat(strings.TrimSpace(splittedPath[1]))
				if err != nil {
					return err
				}
				indicatorFileChangeIndicator = fmt.Sprintf("%d", fi.ModTime().Unix())
			}

			cleanedPathList = append(cleanedPathList, cleanPath)
			cacheModel.IndicatorHashMap[cleanPath] = indicatorFileChangeIndicator
		} else {
			cleanedPathList = append(cleanedPathList, strings.TrimSpace(path))
		}
	}
	cacheModel.PathList = cleanedPathList

	return nil
}

func main() {
	stepStartedAt := time.Now()

	log.Infof("Configs:")
	configs := createConfigsModelFromEnvs()
	configs.print()

	fmt.Println()

	cacheModel := NewCacheModel(configs)

	startTime := time.Now()
	log.Infof("Cleaning paths")
	if err := cacheModel.CleanPaths(); err != nil {
		log.Errorf("Failed to clean paths, error: %+v", err)
		os.Exit(1)
	}
	log.Printf("- Done")
	log.Printf("- Took: %s", time.Now().Sub(startTime))

	fmt.Println()

	startTime = time.Now()
	log.Infof("Checking previous cache status")
	cacheInfoFileExists, err := cacheModel.LoadPreviousFilePathMap()
	if err != nil {
		log.Errorf("Failed to load previous cache info file, error: %+v", err)
		os.Exit(1)
	}

	if cacheInfoFileExists {
		log.Printf("- Previous cache info found")
	} else {
		log.Printf("- No previous cache info found")
	}
	log.Printf("- Done")
	log.Printf("- Took: %s", time.Now().Sub(startTime))

	recacheRequired := true
	if cacheInfoFileExists {
		fmt.Println()
		startTime = time.Now()
		log.Infof("Checking for file changes")
		currentFilePathsMap, err := cacheModel.GenerateCacheInfoMAP()
		if err != nil {
			log.Errorf("Failed to generate files map, error: %+v", err)
			os.Exit(1)
		}

		recacheRequired, err = cacheModel.CompareFilePathMaps(currentFilePathsMap)
		if err != nil {
			log.Errorf("Failed to compare file path maps, error: %+v", err)
			os.Exit(1)
		}

		if recacheRequired {
			log.Printf("- File changes found")
			log.Printf("- Done")
			log.Printf("- Took: %s", time.Now().Sub(startTime))
			fmt.Println()
		} else {
			log.Printf("- No files changed")
			log.Printf("- Done")
			log.Printf("- Took: %s", time.Now().Sub(startTime))
			fmt.Println()
			log.Printf("Total time: %s", time.Now().Sub(stepStartedAt))
			os.Exit(0)
		}
	}

	startTime = time.Now()
	log.Infof("Generating cache archive")
	if err := cacheModel.CreateTarArchive(); err != nil {
		log.Errorf("Failed to create tar archive, error: %+v", err)
		os.Exit(1)
	}

	if err := cacheModel.ProcessFiles(); err != nil {
		log.Errorf("Failed to process files, error: %+v", err)
		os.Exit(1)
	}

	if err := cacheModel.CloseTarArchive(); err != nil {
		log.Errorf("Failed to close tar archive, error: %+v", err)
		os.Exit(1)
	}
	log.Printf("- Done")
	log.Printf("- Took: %s", time.Now().Sub(startTime))

	fmt.Println()

	startTime = time.Now()
	log.Infof("Uploading cache archive")
	if err := configs.uploadArchive(); err != nil {
		log.Errorf("Failed to upload archive, error: %+v", err)
		os.Exit(1)
	}
	log.Printf("- Done")
	log.Printf("- Took: %s", time.Now().Sub(startTime))

	fmt.Println()
	log.Printf("Total time: %s", time.Now().Sub(stepStartedAt))
}

func (configs *ConfigsModel) uploadArchive() error {
	fi, err := os.Stat(cacheArchivePath)
	if err != nil {
		return fmt.Errorf("Failed to get File Infos of Archive (%s): %s", cacheArchivePath, err)
	}
	sizeInBytes := fi.Size()
	log.Printf("   Archive file size: %d bytes / %f MB", sizeInBytes, (float64(sizeInBytes) / 1024.0 / 1024.0))

	uploadURL, err := getCacheUploadURL(configs.CacheAPIURL, sizeInBytes)
	if err != nil {
		return fmt.Errorf("Failed to generate Upload URL: %s", err)
	}

	if err := _tryToUploadArchive(uploadURL, cacheArchivePath); err != nil {
		fmt.Println()
		log.Printf(" ===> (!) First upload attempt failed, retrying...")
		fmt.Println()
		time.Sleep(3000 * time.Millisecond)
		return _tryToUploadArchive(uploadURL, cacheArchivePath)
	}
	return nil
}

func getFileContentMD5(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}

	defer func() (string, error) {
		err := f.Close()
		if err != nil {
			return "", err
		}
		return "", nil
	}()

	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func timespecToTime(ts syscall.Timespec) time.Time {
	return time.Unix(int64(ts.Sec), int64(ts.Nsec))
}

func getCacheUploadURL(cacheAPIURL string, fileSizeInBytes int64) (string, error) {
	requestDataModel := CacheUploadAPIRequestDataModel{
		FileSizeInBytes: fileSizeInBytes,
	}

	requestJSONBytes, err := json.Marshal(requestDataModel)
	if err != nil {
		return "", fmt.Errorf("Failed to JSON marshal CacheUploadAPIRequestDataModel: %s", err)
	}

	req, err := http.NewRequest("POST", cacheAPIURL, bytes.NewBuffer(requestJSONBytes))
	if err != nil {
		return "", fmt.Errorf("Failed to create request: %s", err)
	}

	client := &http.Client{
		Timeout: 20 * time.Second,
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("Failed to send request: %s", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Printf(" [!] Exception: Failed to close response body, error: %s", err)
		}
	}()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("Request sent, but failed to read response body (http-code:%d): %s", resp.StatusCode, body)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 202 {
		return "", fmt.Errorf("Upload URL was rejected (http-code:%d): %s", resp.StatusCode, body)
	}

	var respModel GenerateUploadURLRespModel
	if err := json.Unmarshal(body, &respModel); err != nil {
		return "", fmt.Errorf("Request sent, but failed to parse JSON response (http-code:%d): %s", resp.StatusCode, body)
	}

	if respModel.UploadURL == "" {
		return "", fmt.Errorf("Request sent, but Upload URL is empty (http-code:%d): %s", resp.StatusCode, body)
	}

	return respModel.UploadURL, nil
}

func _tryToUploadArchive(uploadURL string, archiveFilePath string) error {
	archFile, err := os.Open(archiveFilePath)
	if err != nil {
		return fmt.Errorf("Failed to open archive file for upload (%s): %s", archiveFilePath, err)
	}
	isFileCloseRequired := true
	defer func() {
		if !isFileCloseRequired {
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

	// req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Add("Content-Length", strconv.FormatInt(fileSize, 10))
	req.ContentLength = fileSize

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("Failed to upload: %s", err)
	}
	isFileCloseRequired = false
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Printf(" [!] Failed to close response body: %s", err)
		}
	}()

	responseBytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("Failed to read response: %s", err)
	}

	if resp.StatusCode != 200 {
		log.Printf("=> Upload response: %s", responseBytes)
		return fmt.Errorf("Failed to upload file, response code was: %d", resp.StatusCode)
	}

	return nil
}
