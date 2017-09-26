package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/bitrise-io/go-utils/fileutil"
	"github.com/bitrise-io/go-utils/log"
	"github.com/bitrise-io/go-utils/pathutil"
	"github.com/bitrise-tools/go-steputils/input"
	glob "github.com/ryanuber/go-glob"
)

const (
	fileIndicatorSeparator             = "->"
	fingerprintMethodIDFileModTime     = "file-mod-time"
	fingerprintMethodIDContentChecksum = "file-content-hash"
	cacheInfoFilePath                  = "/tmp/cache-info.json"
	cacheArchivePath                   = "/tmp/cache-archive.tar"
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

// CacheModel ...
type CacheModel struct {
	DebugMode           bool
	CompressArchive     bool
	PathList            []string
	IgnoreList          []string
	TarFile             *os.File
	TarWriter           *tar.Writer
	GzipWriter          *gzip.Writer
	FileChangeIndicator ChangeIndicator
	FilePathMap         map[string]string
	PreviousFilePathMap map[string]string
	IndicatorHashMap    map[string]string
}

// ConfigsModel ...
type ConfigsModel struct {
	Paths               string
	IgnoredPaths        string
	DebugMode           string
	CacheAPIURL         string
	FingerprintMethodID string
	CompressArchive     string
}

func (configs *ConfigsModel) print() {
	log.Printf("- CachePaths:")
	for _, path := range strings.Split(configs.Paths, "\n") {
		clnPth := strings.TrimSpace(path)
		if clnPth == "" {
			continue
		}
		log.Printf("  * %s", clnPth)
	}

	fmt.Println()

	log.Printf("- IgnoredPaths:")
	for _, path := range strings.Split(configs.IgnoredPaths, "\n") {
		clnPth := strings.TrimSpace(path)
		if clnPth == "" {
			continue
		}
		log.Printf("  * %s", clnPth)
	}

	fmt.Println()

	log.Printf("- CompressArchive: %s", configs.CompressArchive)
	log.Printf("- FingerprintMethodID: %s", configs.FingerprintMethodID)
}

func (configs *ConfigsModel) validate() error {
	if err := input.ValidateIfNotEmpty(configs.CacheAPIURL); err != nil {
		return fmt.Errorf("CacheAPIURL: %s", err)
	}
	if err := input.ValidateWithOptions(configs.FingerprintMethodID, fingerprintMethodIDContentChecksum, fingerprintMethodIDFileModTime); err != nil {
		return fmt.Errorf("FingerprintMethodID: %s", err)
	}

	return nil
}

func createConfigsModelFromEnvs() *ConfigsModel {
	return &ConfigsModel{
		DebugMode:           os.Getenv("is_debug_mode"),
		CacheAPIURL:         os.Getenv("cache_api_url"),
		CompressArchive:     os.Getenv("compress_archive"),
		FingerprintMethodID: os.Getenv("fingerprint_method"),
		Paths:               os.Getenv("cache_paths") + "\n" + os.Getenv("bitrise_cache_include_paths"),
		IgnoredPaths:        os.Getenv("ignore_check_on_paths") + "\n" + os.Getenv("bitrise_cache_exclude_paths"),
	}
}

// NewCacheModel ...
func NewCacheModel(configs *ConfigsModel) *CacheModel {
	splittedPaths := strings.Split(configs.Paths, "\n")
	splittedIgnoredPaths := strings.Split(configs.IgnoredPaths, "\n")

	return &CacheModel{
		PathList:            splittedPaths,
		FilePathMap:         map[string]string{},
		IndicatorHashMap:    map[string]string{},
		PreviousFilePathMap: map[string]string{},
		IgnoreList:          splittedIgnoredPaths,
		DebugMode:           configs.DebugMode == "true",
		CompressArchive:     configs.CompressArchive == "true",
		FileChangeIndicator: ChangeIndicator(configs.FingerprintMethodID),
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

		cacheModel.GzipWriter = gw
		cacheModel.TarWriter = tar.NewWriter(gw)
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

	if _, err := io.Copy(cacheModel.TarWriter, bytes.NewReader(filePathMapBytes)); err != nil && err != io.EOF {
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

	return cacheModel.TarFile.Close()
}

// GenerateCacheInfoMap ...
func (cacheModel *CacheModel) GenerateCacheInfoMap() (map[string]string, error) {
	err := cacheModel.ProcessFiles(false)
	if err != nil {
		return nil, err
	}
	return cacheModel.FilePathMap, nil
}

// ProcessFiles ...
func (cacheModel *CacheModel) ProcessFiles(archiveFiles bool) error {
	for _, cachePath := range cacheModel.PathList {
		if err := filepath.Walk(cachePath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			if filepath.Dir(path) == filepath.Dir(cachePath) {
				return nil
			}

			link := ""
			if info.Mode()&os.ModeSymlink != 0 {
				link, err = os.Readlink(path)
				if err != nil {
					return err
				}
			}

			header, err := tar.FileInfoHeader(info, link)
			if err != nil {
				return err
			}

			header.Name = path
			header.ModTime = info.ModTime()

			if info.IsDir() {
				header.Name += "/"
			}

			storeMode, indicatorFileMD5 := cacheModel.GetStoreMode(path)

			if storeMode == REMOVE {
				if cacheModel.DebugMode {
					log.Printf("  Exclude: %s", path)
				}
				return nil
			}

			if archiveFiles {
				if err := cacheModel.TarWriter.WriteHeader(header); err != nil {
					return err
				}
			}

			if info.IsDir() {
				return nil
			}

			if val, ok := cacheModel.FilePathMap[path]; ok && val != "" {
				return nil
			}

			if header.Typeflag == tar.TypeReg || header.Typeflag == tar.TypeRegA {
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
					if cacheModel.DebugMode {
						log.Printf("  Ignore changes: %s", path)
					}
					cacheModel.FilePathMap[path] = "-"
				case INDICATOR:
					cacheModel.FilePathMap[path] = indicatorFileMD5
				}

				if archiveFiles {
					file, err := os.Open(path)
					if err != nil {
						return err
					}

					defer func() {
						if err := file.Close(); err != nil {
							log.Errorf("Failed to close file (%s), error: %+v", path, err)
						}
					}()

					_, err = io.CopyN(cacheModel.TarWriter, file, info.Size())
					if err != nil && err != io.EOF {
						return err
					}
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
	if err != nil {
		return false, err
	}

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
				if !cacheModel.DebugMode {
					if logLineCount >= 9 {
						log.Printf("[List truncated, turn on DebugMode to see the whole change list]")
						return true, nil
					}
				}
				logLineCount++
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
		if remainingValue == "-" {
			log.Donef("- Ignored")
			if !cacheModel.DebugMode {
				if logLineCount >= 9 {
					log.Printf("[List truncated, turn on DebugMode to see the whole change list]")
					return true, nil
				}
			}
			logLineCount++
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
	}

	return triggerNewCache, nil
}

// CleanPaths ...
func (cacheModel *CacheModel) CleanPaths() error {
	cleanedPathList := []string{}

	for _, path := range cacheModel.PathList {
		if strings.TrimSpace(path) == "" {
			continue
		}
		if strings.Contains(path, fileIndicatorSeparator) {
			splittedPath := strings.Split(path, fileIndicatorSeparator)
			cleanPath := strings.TrimSpace(splittedPath[0])
			indicatorFilePath := strings.TrimSpace(splittedPath[1])

			indicatorFileInfo, indicatorFilePathExists, err := pathutil.PathCheckAndInfos(indicatorFilePath)
			if err != nil {
				return err
			}
			if !indicatorFilePathExists {
				return fmt.Errorf("Indicator file doesn't exists: %s", cleanPath)
			}
			if indicatorFileInfo.IsDir() {
				return fmt.Errorf("Indicator path is a directory: %s", cleanPath)
			}

			pathExists, err := pathutil.IsPathExists(cleanPath)
			if err != nil {
				return err
			}
			if !pathExists {
				log.Warnf("Path ignored, does not exists: %s", cleanPath)
				continue
			}

			cleanPath, err = filepath.Abs(cleanPath)
			if err != nil {
				return err
			}

			indicatorFilePath, err = filepath.Abs(indicatorFilePath)
			if err != nil {
				return err
			}

			indicatorFileChangeIndicator := ""

			if cacheModel.FileChangeIndicator == MD5 {
				indicatorFileChangeIndicator, err = getFileContentMD5(indicatorFilePath)
				if err != nil {
					return err
				}
			} else if cacheModel.FileChangeIndicator == MODTIME {
				fi, err := os.Stat(indicatorFilePath)
				if err != nil {
					return err
				}
				indicatorFileChangeIndicator = fmt.Sprintf("%d", fi.ModTime().Unix())
			}

			cleanedPathList = append(cleanedPathList, cleanPath)
			cacheModel.IndicatorHashMap[cleanPath] = indicatorFileChangeIndicator
		} else {
			path = strings.TrimSpace(path)

			pathExists, err := pathutil.IsPathExists(path)
			if err != nil {
				return err
			}
			if !pathExists {
				log.Warnf("Path ignored, does not exists: %s", path)
				continue
			}

			path, err = filepath.Abs(path)
			if err != nil {
				return err
			}

			cleanedPathList = append(cleanedPathList, path)
		}
	}
	cacheModel.PathList = cleanedPathList

	cleanedIgnoredPathList := []string{}
	for _, path := range cacheModel.IgnoreList {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}

		if !strings.Contains(path, "*") {
			var err error
			path, err = filepath.Abs(path)
			if err != nil {
				return err
			}
		}

		cleanedIgnoredPathList = append(cleanedIgnoredPathList, path)
	}
	cacheModel.IgnoreList = cleanedIgnoredPathList

	return nil
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

	if err := tryToUploadArchive(uploadURL, cacheArchivePath); err != nil {
		fmt.Println()
		log.Printf(" ===> (!) First upload attempt failed, retrying...")
		fmt.Println()
		time.Sleep(3000 * time.Millisecond)
		return tryToUploadArchive(uploadURL, cacheArchivePath)
	}
	return nil
}

func getFileContentMD5(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}

	defer func() {
		if err := f.Close(); err != nil {
			log.Errorf("Failed to close file (%s), error: %+v", filePath, err)
		}
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
	req, err := http.NewRequest("POST", cacheAPIURL, bytes.NewReader([]byte(fmt.Sprintf(`{"file_size_in_bytes": %d}`, fileSizeInBytes))))
	if err != nil {
		return "", fmt.Errorf("Failed to create request: %s", err)
	}

	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return "", fmt.Errorf("Failed to send request: %s", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Printf(" [!] Exception: Failed to close response body, error: %s", err)
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

func main() {
	stepStartedAt := time.Now()

	log.Infof("Configs:")
	configs := createConfigsModelFromEnvs()
	configs.print()

	fmt.Println()

	if err := configs.validate(); err != nil {
		log.Errorf("Issue with input: %s", err)
		os.Exit(1)
	}

	cacheModel := NewCacheModel(configs)

	// Cleaning paths
	startTime := time.Now()
	log.Infof("Cleaning paths")
	if err := cacheModel.CleanPaths(); err != nil {
		log.Errorf("Failed to clean paths, error: %+v", err)
		os.Exit(1)
	}
	log.Printf("- Done")
	log.Printf("- Took: %s", time.Now().Sub(startTime))
	fmt.Println()

	if len(cacheModel.PathList) == 0 {
		log.Warnf("No path to cache, skip caching...")
		os.Exit(0)
	}

	// Check prev. cache
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
	fmt.Println()

	// Checking file changes
	recacheRequired := true
	if cacheInfoFileExists {
		startTime = time.Now()
		log.Infof("Checking for file changes")
		currentFilePathsMap, err := cacheModel.GenerateCacheInfoMap()
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

	// Generate cache archive
	startTime = time.Now()
	log.Infof("Generating cache archive")
	if err := cacheModel.CreateTarArchive(); err != nil {
		log.Errorf("Failed to create tar archive, error: %+v", err)
		os.Exit(1)
	}

	if err := cacheModel.ProcessFiles(true); err != nil {
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

	// Upload cache archive
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
