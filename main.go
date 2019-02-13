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
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/bitrise-io/go-utils/command"

	"github.com/bitrise-io/go-utils/fileutil"
	"github.com/bitrise-io/go-utils/log"
	"github.com/bitrise-io/go-utils/pathutil"
	"github.com/bitrise-io/go-utils/sliceutil"
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

// NewCacheModel ...
func NewCacheModel(configs *Config) *CacheModel {
	splittedPaths := strings.Split(configs.Paths, "\n")
	splittedIgnoredPaths := strings.Split(configs.IgnoredPaths, "\n")

	return &CacheModel{
		PathList: splittedPaths,
		//FilePathMap:       map[string]string{},
		IndicatorHashMap:    map[string]string{},
		PreviousFilePathMap: map[string]string{},
		IgnoreList:          splittedIgnoredPaths,
		DebugMode:           configs.DebugMode == "true",
		CompressArchive:     configs.CompressArchive == "true",
		FileChangeIndicator: ChangeIndicator(configs.FingerprintMethodID),
	}
}

// CreateTarArchive ...
func CreateTarArchive(compressArchive bool) (gzipWriter *gzip.Writer, tarWriter *tar.Writer, tarFile *os.File, err error) {
	tarFile, err = os.Create(cacheArchivePath)
	if err != nil {
		return
	}

	if compressArchive {
		gzipWriter, err = gzip.NewWriterLevel(tarFile, gzip.BestCompression)
		if err != nil {
			return
		}

		tarWriter = tar.NewWriter(gzipWriter)
	} else {
		tarWriter = tar.NewWriter(tarFile)
	}

	return
}

// CloseTarArchive ...
func CloseTarArchive(filePathMap map[string]string, tarWriter *tar.Writer, gzipWriter *gzip.Writer, tarFile *os.File, compressArchive bool) error {
	filePathMapBytes, err := json.Marshal(filePathMap)
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

	if err := tarWriter.WriteHeader(closingHeader); err != nil {
		return err
	}

	if _, err := io.Copy(tarWriter, bytes.NewReader(filePathMapBytes)); err != nil && err != io.EOF {
		return err
	}

	if err := tarWriter.Close(); err != nil {
		return err
	}

	if compressArchive {
		if err := gzipWriter.Close(); err != nil {
			return err
		}
	}

	return tarFile.Close()
}

// GenerateCacheInfoMap ...
func GenerateCacheInfoMap(indicatorHashMap, filePathMap map[string]string, pathList, ignoreList []string, indicator ChangeIndicator, tarWriter *tar.Writer, archiveFiles bool) (newFilePathMap map[string]string, err error) {
	filePathMap, err = ProcessFiles(indicatorHashMap, filePathMap, pathList, ignoreList, indicator, tarWriter, archiveFiles, false)
	if err != nil {
		return
	}
	return
}

// ProcessFiles ...
func ProcessFiles(indicatorHashMap, filePathMap map[string]string, pathList, ignoreList []string, indicator ChangeIndicator, writer *tar.Writer, archiveFiles, debug bool) (map[string]string, error) {
	isFilePathMapGeneratedAlready := false

	if filePathMap != nil {
		isFilePathMapGeneratedAlready = true
	} else {
		filePathMap = map[string]string{}
	}

	for _, cachePath := range pathList {
		if err := filepath.Walk(cachePath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			if info.IsDir() && filepath.Dir(path) == filepath.Dir(cachePath) {
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

			storeMode, indicatorFileMD5 := GetStoreMode(indicatorHashMap, ignoreList, path)

			if storeMode == REMOVE {
				if debug {
					log.Printf("  Exclude: %s", path)
				}
				return nil
			}

			if archiveFiles {
				if err := writer.WriteHeader(header); err != nil {
					return err
				}
			}

			if info.IsDir() {
				return nil
			}

			if header.Typeflag == tar.TypeReg || header.Typeflag == tar.TypeRegA {
				if !isFilePathMapGeneratedAlready {
					switch storeMode {
					case STORE:
						if indicator == MD5 {
							fileMD5, err := getFileContentMD5(path)
							if err != nil {
								return err
							}
							filePathMap[path] = fileMD5
						} else if indicator == MODTIME {
							filePathMap[path] = fmt.Sprintf("%d", info.ModTime().Unix())
						}
					case SKIP:
						if debug {
							log.Printf("  Ignore changes: %s", path)
						}
						filePathMap[path] = "-"
					case INDICATOR:
						filePathMap[path] = indicatorFileMD5
					}
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

					_, err = io.CopyN(writer, file, info.Size())
					if err != nil && err != io.EOF {
						return err
					}
				}
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}
	return filePathMap, nil
}

// GetStoreMode ...
func GetStoreMode(indicatorHashMap map[string]string, ignoreList []string, path string) (StoreMode, string) {
	for key, value := range indicatorHashMap {
		if strings.HasPrefix(path, key) {
			return INDICATOR, value
		}
	}

	for _, ignoreFilter := range ignoreList {
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
func LoadPreviousFilePathMap() (map[string]string, bool, error) {
	exists, err := pathutil.IsPathExists(cacheInfoFilePath)
	if err != nil {
		return nil, false, err
	}
	if !exists {
		return nil, false, nil
	}

	fileBytes, err := fileutil.ReadBytesFromFile(cacheInfoFilePath)
	if err != nil {
		return nil, false, err
	}

	var previousFilePathMap map[string]string
	err = json.Unmarshal(fileBytes, &previousFilePathMap)
	if err != nil {
		return nil, false, err
	}

	return previousFilePathMap, true, nil
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

func cleanDuplicatePaths(paths []string) []string {
	cleanedPaths := []string{}
	for _, item := range paths {
		if item == "" {
			continue
		}
		cleanPath := path.Clean(item)
		if !sliceutil.IsStringInSlice(cleanPath, cleanedPaths) {
			cleanedPaths = append(cleanedPaths, cleanPath)
		}
	}

	return cleanedPaths
}

// CleanPaths ...
func CleanPaths(pathList, ignorePathList []string, indicator ChangeIndicator) ([]string, []string, map[string]string, error) {
	indicatorHashMap := map[string]string{}
	var cleanedPathList []string
	var cleanedIgnoredPathList []string

	pathListWithoutDuplicates := cleanDuplicatePaths(pathList)

	for _, path := range pathListWithoutDuplicates {
		if strings.TrimSpace(path) == "" {
			continue
		}
		if strings.Contains(path, fileIndicatorSeparator) {
			splittedPath := strings.Split(path, fileIndicatorSeparator)
			cleanPath := strings.TrimSpace(splittedPath[0])
			indicatorFilePath := strings.TrimSpace(splittedPath[1])

			var err error
			cleanPath, err = pathutil.ExpandTilde(cleanPath)
			if err != nil {
				log.Warnf("%s, ignoring...", err)
				continue
			}
			indicatorFilePath, err = pathutil.ExpandTilde(indicatorFilePath)
			if err != nil {
				log.Warnf("%s, ignoring...", err)
				continue
			}

			indicatorFileInfo, indicatorFilePathExists, err := pathutil.PathCheckAndInfos(indicatorFilePath)
			if err != nil {
				return nil, nil, nil, err
			}
			if !indicatorFilePathExists {
				log.Warnf("Path ignored, indicator file (%s) doesn't exists: %s", indicatorFilePath, cleanPath)
				continue
			}
			if indicatorFileInfo.IsDir() {
				log.Warnf("Path ignored, indicator path is a directory: %s", cleanPath)
				continue
			}

			pathExists, err := pathutil.IsPathExists(cleanPath)
			if err != nil {
				return nil, nil, nil, err
			}
			if !pathExists {
				log.Warnf("Path ignored, does not exists: %s", cleanPath)
				continue
			}

			cleanPath, err = filepath.Abs(cleanPath)
			if err != nil {
				return nil, nil, nil, err
			}

			indicatorFilePath, err = filepath.Abs(indicatorFilePath)
			if err != nil {
				return nil, nil, nil, err
			}

			indicatorFileChangeIndicator := ""

			if indicator == MD5 {
				indicatorFileChangeIndicator, err = getFileContentMD5(indicatorFilePath)
				if err != nil {
					return nil, nil, nil, err
				}
			} else if indicator == MODTIME {
				fi, err := os.Stat(indicatorFilePath)
				if err != nil {
					return nil, nil, nil, err
				}
				indicatorFileChangeIndicator = fmt.Sprintf("%d", fi.ModTime().Unix())
			}

			cleanedPathList = append(cleanedPathList, cleanPath)
			indicatorHashMap[cleanPath] = indicatorFileChangeIndicator
		} else {
			path = strings.TrimSpace(path)

			pathExists, err := pathutil.IsPathExists(path)
			if err != nil {
				return nil, nil, nil, err
			}
			if !pathExists {
				log.Warnf("Path ignored, does not exists: %s", path)
				continue
			}

			path, err = filepath.Abs(path)
			if err != nil {
				return nil, nil, nil, err
			}

			cleanedPathList = append(cleanedPathList, path)
		}
	}

	for _, path := range ignorePathList {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}

		if strings.HasPrefix(path, "!") {
			expandedPth, err := pathutil.ExpandTilde(strings.TrimPrefix(path, "!"))
			if err != nil {
				log.Warnf("%s, ignoring...", err)
				continue
			}

			path = "!" + expandedPth
		} else {
			expandedPth, err := pathutil.ExpandTilde(path)
			if err != nil {
				log.Warnf("%s, ignoring...", err)
				continue
			}
			path = expandedPth
		}

		if !strings.Contains(path, "*") {
			var err error
			path, err = filepath.Abs(path)
			if err != nil {
				return nil, nil, nil, err
			}
		}

		cleanedIgnoredPathList = append(cleanedIgnoredPathList, path)
	}

	return cleanedPathList, cleanedIgnoredPathList, indicatorHashMap, nil
}

func uploadArchive(cacheAPIURL string) error {
	fi, err := os.Stat(cacheArchivePath)
	if err != nil {
		return fmt.Errorf("Failed to get File Infos of Archive (%s): %s", cacheArchivePath, err)
	}
	sizeInBytes := fi.Size()
	log.Printf("   Archive file size: %d bytes / %f MB", sizeInBytes, (float64(sizeInBytes) / 1024.0 / 1024.0))

	uploadURL, err := getCacheUploadURL(cacheAPIURL, sizeInBytes)
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
	if strings.HasPrefix(cacheAPIURL, "file://") {
		return cacheAPIURL, nil
	}

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

func main() {
	stepStartedAt := time.Now()

	configs, err := ParseConfig()
	if err != nil {
		log.Errorf(err.Error())
		os.Exit(1)
	}

	configs.Print()
	fmt.Println()

	cacheModel := NewCacheModel(&configs)

	// Cleaning paths
	startTime := time.Now()
	log.Infof("Cleaning paths")
	pathList, ignorePathList, indicatorHashMap, err := CleanPaths(cacheModel.PathList, cacheModel.IgnoreList, cacheModel.FileChangeIndicator)
	if err != nil {
		log.Errorf("Failed to clean paths, error: %+v", err)
		os.Exit(1)
	}

	cacheModel.PathList = pathList
	cacheModel.IgnoreList = ignorePathList
	cacheModel.IndicatorHashMap = indicatorHashMap

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
	previousFilePathMap, cacheInfoFileExists, err := LoadPreviousFilePathMap()
	if err != nil {
		log.Errorf("Failed to load previous cache info file, error: %+v", err)
		os.Exit(1)
	}
	cacheModel.PreviousFilePathMap = previousFilePathMap

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
		filePathsMap, err := GenerateCacheInfoMap(cacheModel.IndicatorHashMap, cacheModel.FilePathMap, cacheModel.PathList, cacheModel.IgnoreList, cacheModel.FileChangeIndicator, cacheModel.TarWriter, cacheModel.CompressArchive)
		if err != nil {
			log.Errorf("Failed to generate files map, error: %+v", err)
			os.Exit(1)
		}
		cacheModel.FilePathMap = filePathsMap

		recacheRequired, err = cacheModel.CompareFilePathMaps(filePathsMap)
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
	gzipWriter, tarWriter, tarFile, err := CreateTarArchive(cacheModel.CompressArchive)
	if err != nil {
		log.Errorf("Failed to create tar archive, error: %+v", err)
		os.Exit(1)
	}
	cacheModel.GzipWriter = gzipWriter
	cacheModel.TarWriter = tarWriter
	cacheModel.TarFile = tarFile

	filePathMap, err := ProcessFiles(cacheModel.IndicatorHashMap, cacheModel.FilePathMap, cacheModel.PathList, cacheModel.IgnoreList, cacheModel.FileChangeIndicator, cacheModel.TarWriter, cacheModel.CompressArchive, true)
	if err != nil {
		log.Errorf("Failed to process files, error: %+v", err)
		os.Exit(1)
	}
	cacheModel.FilePathMap = filePathMap

	if err := CloseTarArchive(cacheModel.FilePathMap, cacheModel.TarWriter, cacheModel.GzipWriter, cacheModel.TarFile, cacheModel.CompressArchive); err != nil {
		log.Errorf("Failed to close tar archive, error: %+v", err)
		os.Exit(1)
	}
	log.Printf("- Done")
	log.Printf("- Took: %s", time.Now().Sub(startTime))
	fmt.Println()

	// Upload cache archive
	startTime = time.Now()
	log.Infof("Uploading cache archive")
	if err := uploadArchive(configs.CacheAPIURL); err != nil {
		log.Errorf("Failed to upload archive, error: %+v", err)
		os.Exit(1)
	}
	log.Printf("- Done")
	log.Printf("- Took: %s", time.Now().Sub(startTime))
	fmt.Println()

	log.Printf("Total time: %s", time.Now().Sub(stepStartedAt))
}
