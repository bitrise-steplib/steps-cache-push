package main

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/bitrise-io/go-utils/fileutil"

	"github.com/bitrise-io/go-utils/pathutil"
)

func Test_parseIgnoreListItem(t *testing.T) {
	tests := []struct {
		name        string
		item        string
		wantPattern string
		wantExclude bool
	}{
		{
			name:        "simple ignore item",
			item:        "path/to/ignore",
			wantPattern: "path/to/ignore",
			wantExclude: false,
		},
		{
			name:        "simple ignore patter",
			item:        "path/**/ignore",
			wantPattern: "path/**/ignore",
			wantExclude: false,
		},
		{
			name:        "ignore item surrounding spaces",
			item:        " path/to/ignore  ",
			wantPattern: "path/to/ignore",
			wantExclude: false,
		},
		{
			name:        "empty ignore item",
			item:        "",
			wantPattern: "",
			wantExclude: false,
		},
		{
			name:        "simple exclude item",
			item:        "!path/to/ignore",
			wantPattern: "path/to/ignore",
			wantExclude: true,
		},
		{
			name:        "exclude item surrounding spaces",
			item:        "!  path/to/ignore ",
			wantPattern: "path/to/ignore",
			wantExclude: true,
		},
		{
			name:        "empty exclude item",
			item:        "!",
			wantPattern: "",
			wantExclude: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pattern, exclude := parseIgnoreListItem(tt.item)
			if pattern != tt.wantPattern {
				t.Errorf("parseIgnoreListItem() pattern = %v, ignoreItem %v", pattern, tt.wantPattern)
			}
			if exclude != tt.wantExclude {
				t.Errorf("parseIgnoreListItem() exclude = %v, want %v", exclude, tt.wantExclude)
			}
		})
	}
}

func Test_parseIncludeListItem(t *testing.T) {
	tests := []struct {
		name          string
		item          string
		wantPth       string
		wantIndicator string
	}{
		{
			name:          "simple include path",
			item:          "path/to/include",
			wantPth:       "path/to/include",
			wantIndicator: "",
		},
		{
			name:          "simple include path surrounding spaces",
			item:          "  path/to/include ",
			wantPth:       "path/to/include",
			wantIndicator: "",
		},
		{
			name:          "empty include item",
			item:          "",
			wantPth:       "",
			wantIndicator: "",
		},
		{
			name:          "simple indicator",
			item:          "path/to/include->indicator/path",
			wantPth:       "path/to/include",
			wantIndicator: "indicator/path",
		},
		{
			name:          "simple indicator surrounding spaces",
			item:          "  path/to/include ->  indicator/path ",
			wantPth:       "path/to/include",
			wantIndicator: "indicator/path",
		},
		{
			name:          "indicator without path",
			item:          "->indicator/path",
			wantPth:       "",
			wantIndicator: "indicator/path",
		},
		{
			name:          "indicator with space path",
			item:          " ->indicator/path",
			wantPth:       "",
			wantIndicator: "indicator/path",
		},
		{
			name:          "indicator without indicator path",
			item:          "path/to/include->",
			wantPth:       "path/to/include",
			wantIndicator: "",
		},
		{
			name:          "indicator with space indicator path",
			item:          "path/to/include -> ",
			wantPth:       "path/to/include",
			wantIndicator: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pth, indicator := parseIncludeListItem(tt.item)
			if pth != tt.wantPth {
				t.Errorf("parseIncludeListItem() pth = %v, want %v", pth, tt.wantPth)
			}
			if indicator != tt.wantIndicator {
				t.Errorf("parseIncludeListItem() indicator = %v, want %v", indicator, tt.wantIndicator)
			}
		})
	}
}

func Test_parseIncludeList(t *testing.T) {
	tests := []struct {
		name           string
		list           []string
		indicatorByPth map[string]string
	}{
		{
			name:           "simple include list",
			list:           []string{"path1/to/include", "path2/to/include->indicator/path"},
			indicatorByPth: map[string]string{"path1/to/include": "", "path2/to/include": "indicator/path"},
		},
		{
			name:           "duplicated include items",
			list:           []string{"path/to/include", "path/to/include->indicator/path"},
			indicatorByPth: map[string]string{"path/to/include": "indicator/path"},
		},
		{
			name:           "empty item",
			list:           []string{"", "path/to/include->indicator/path"},
			indicatorByPth: map[string]string{"path/to/include": "indicator/path"},
		},
		{
			name:           "empty path",
			list:           []string{"->indicator/path", "path/to/include->indicator/path"},
			indicatorByPth: map[string]string{"path/to/include": "indicator/path"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseIncludeList(tt.list)
			if !reflect.DeepEqual(got, tt.indicatorByPth) {
				t.Errorf("parseIncludeList() = %v, want %v", got, tt.indicatorByPth)
			}
		})
	}
}

func Test_parseIgnoreList(t *testing.T) {
	tests := []struct {
		name             string
		list             []string
		excludeByPattern map[string]bool
	}{
		{
			name:             "simple ignore list",
			list:             []string{"path/to/ignore", "!path/to/exclude"},
			excludeByPattern: map[string]bool{"path/to/ignore": false, "path/to/exclude": true},
		},
		{
			name:             "duplicated items",
			list:             []string{"path/to/ignore", "!path/to/ignore"},
			excludeByPattern: map[string]bool{"path/to/ignore": true},
		},
		{
			name:             "empty item",
			list:             []string{"", "!path/to/exclude"},
			excludeByPattern: map[string]bool{"path/to/exclude": true},
		},
		{
			name:             "empty path",
			list:             []string{"!"},
			excludeByPattern: map[string]bool{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseIgnoreList(tt.list); !reflect.DeepEqual(got, tt.excludeByPattern) {
				t.Errorf("parseIgnoreList() = %v, want %v", got, tt.excludeByPattern)
			}
		})
	}
}

func createDirStruct(t *testing.T, contentByPth map[string]string) {
	for pth, content := range contentByPth {
		dir := filepath.Dir(pth)
		if err := os.MkdirAll(dir, 0777); err != nil {
			t.Fatalf("failed to create dir: %s", err)
			return
		}
		if err := fileutil.WriteStringToFile(pth, content); err != nil {
			t.Fatalf("failed to create file: %s", err)
			return
		}
	}
}

func Test_expandPath(t *testing.T) {
	tmpDir, err := pathutil.NormalizedOSTempDirPath("cache")
	if err != nil {
		t.Errorf("failed to create tmp dir: %s", err)
		return
	}

	pths := map[string]string{
		filepath.Join(tmpDir, "subdir", "file1"): "",
		filepath.Join(tmpDir, "subdir", "file2"): "",
	}
	createDirStruct(t, pths)

	tests := []struct {
		name    string
		pth     string
		pths    []string
		wantErr bool
	}{
		{
			name:    "list files in a directory",
			pth:     filepath.Join(tmpDir, "subdir"),
			pths:    []string{filepath.Join(tmpDir, "subdir", "file1"), filepath.Join(tmpDir, "subdir", "file2")},
			wantErr: false,
		},
		{
			name:    "puts file path in an array",
			pth:     filepath.Join(tmpDir, "subdir", "file1"),
			pths:    []string{filepath.Join(tmpDir, "subdir", "file1")},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := expandPath(tt.pth)
			if (err != nil) != tt.wantErr {
				t.Errorf("expandPath() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.pths) {
				t.Errorf("expandPath() = %v, want %v", got, tt.pths)
			}
		})
	}
}

func Test_normalizeIndicatorByPath(t *testing.T) {
	tmpDir, err := pathutil.NormalizedOSTempDirPath("cache")
	if err != nil {
		t.Fatalf("failed to create tmp dir: %s", err)
		return
	}

	pths := map[string]string{
		filepath.Join(tmpDir, "subdir", "file1"): "",
		filepath.Join(tmpDir, "subdir", "file2"): "",
	}
	createDirStruct(t, pths)

	if err := os.Setenv("NORMALIZE_INDICATOR_BY_PATH_TMP_DIR", tmpDir); err != nil {
		t.Fatalf("failed to set NORMALIZE_INDICATOR_BY_PATH_TMP_DIR: %s", err)
		return
	}

	tests := []struct {
		name            string
		indicatorByPath map[string]string
		normalized      map[string]string
		wantErr         bool
	}{
		{
			name:            "drops item if indicator does not exists",
			indicatorByPath: map[string]string{filepath.Join(tmpDir, "subdir", "file1"): "non/existing/indicator"},
			normalized:      map[string]string{},
			wantErr:         false,
		},
		{
			name:            "drops item if indicator is a dir",
			indicatorByPath: map[string]string{filepath.Join(tmpDir, "subdir", "file1"): filepath.Join(tmpDir, "subdir")},
			normalized:      map[string]string{},
			wantErr:         false,
		},
		{
			name:            "expand envs in indicator",
			indicatorByPath: map[string]string{filepath.Join(tmpDir, "subdir", "file1"): filepath.Join("$NORMALIZE_INDICATOR_BY_PATH_TMP_DIR", "subdir", "file2")},
			normalized:      map[string]string{filepath.Join(tmpDir, "subdir", "file1"): filepath.Join(tmpDir, "subdir", "file2")},
			wantErr:         false,
		},
		{
			name:            "drops item if path does not exists",
			indicatorByPath: map[string]string{"non/existing/path": ""},
			normalized:      map[string]string{},
			wantErr:         false,
		},
		{
			name:            "expand envs in path",
			indicatorByPath: map[string]string{filepath.Join("$NORMALIZE_INDICATOR_BY_PATH_TMP_DIR", "subdir", "file1"): ""},
			normalized:      map[string]string{filepath.Join(tmpDir, "subdir", "file1"): ""},
			wantErr:         false,
		},
		{
			name:            "expands path if it is a dir",
			indicatorByPath: map[string]string{filepath.Join(tmpDir, "subdir"): ""},
			normalized:      map[string]string{filepath.Join(tmpDir, "subdir", "file1"): "", filepath.Join(tmpDir, "subdir", "file2"): ""},
			wantErr:         false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeIndicatorByPath(tt.indicatorByPath)
			if (err != nil) != tt.wantErr {
				t.Errorf("normalizeIndicatorByPath() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.normalized) {
				t.Errorf("normalizeIndicatorByPath() = %v, want %v", got, tt.normalized)
			}
		})
	}
}

func Test_normalizeExcludeByPattern(t *testing.T) {
	if err := os.Setenv("NORMALIZE_EXCLUDE_BY_PATTERN_KEY", "test"); err != nil {
		t.Fatalf("failed to set NORMALIZE_EXCLUDE_BY_PATTERN_KEY: %s", err)
		return
	}

	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("failed to get caller file")
		return
	}
	currentDir := filepath.Dir(currentFile)

	tests := []struct {
		name             string
		excludeByPattern map[string]bool
		normalized       map[string]bool
		wantErr          bool
	}{
		{
			name:             "expands envs in pattern",
			excludeByPattern: map[string]bool{"/$NORMALIZE_EXCLUDE_BY_PATTERN_KEY/path/to/ignore": false},
			normalized:       map[string]bool{"/test/path/to/ignore": false},
			wantErr:          false,
		},
		{
			name:             "expands pattern",
			excludeByPattern: map[string]bool{"path/to/ignore": false},
			normalized:       map[string]bool{filepath.Join(currentDir, "path/to/ignore"): false},
			wantErr:          false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeExcludeByPattern(tt.excludeByPattern)
			if (err != nil) != tt.wantErr {
				t.Errorf("normalizeExcludeByPattern() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.normalized) {
				t.Errorf("normalizeExcludeByPattern() = %v, want %v", got, tt.normalized)
			}
		})
	}
}

func Test_match(t *testing.T) {
	tests := []struct {
		name             string
		pth              string
		excludeByPattern map[string]bool
		doNotTrack       bool
		exclude          bool
	}{
		{
			name:             "simple no match",
			pth:              "path/to/include",
			excludeByPattern: map[string]bool{"path/to/exclude": false},
			doNotTrack:       false,
			exclude:          false,
		},
		{
			name:             "full match",
			pth:              "path/to/cache",
			excludeByPattern: map[string]bool{"path/to/cache": false},
			doNotTrack:       true,
			exclude:          false,
		},
		{
			name:             "glob match",
			pth:              "path/to/cache",
			excludeByPattern: map[string]bool{"path/*/cache": false},
			doNotTrack:       true,
			exclude:          false,
		},
		{
			name:             "glob match",
			pth:              "path/to/cache",
			excludeByPattern: map[string]bool{"**/cache": false},
			doNotTrack:       true,
			exclude:          false,
		},
		{
			name:             "exclude",
			pth:              "path/to/cache",
			excludeByPattern: map[string]bool{"path/to/cache": true},
			doNotTrack:       true,
			exclude:          true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doNotTrack, exclude := match(tt.pth, tt.excludeByPattern)
			if doNotTrack != tt.doNotTrack {
				t.Errorf("match() doNotTrack = %v, want %v", doNotTrack, tt.doNotTrack)
			}
			if exclude != tt.exclude {
				t.Errorf("match() exclude = %v, want %v", exclude, tt.exclude)
			}
		})
	}
}

func Test_interleave(t *testing.T) {
	tests := []struct {
		name                string
		indicatorByPth      map[string]string
		excludeByPattern    map[string]bool
		indicatorByCachePth map[string]string
		wantErr             bool
	}{
		{
			name:                "no indicator, own content is the indicator",
			indicatorByPth:      map[string]string{"path/to/cache": ""},
			excludeByPattern:    map[string]bool{},
			indicatorByCachePth: map[string]string{"path/to/cache": "path/to/cache"},
			wantErr:             false,
		},
		{
			name:                "no ignore match",
			indicatorByPth:      map[string]string{"path/to/cache": "indicator/path"},
			excludeByPattern:    map[string]bool{"path/to/include": false},
			indicatorByCachePth: map[string]string{"path/to/cache": "indicator/path"},
			wantErr:             false,
		},
		{
			name:                "ignore match, do not track changes",
			indicatorByPth:      map[string]string{"path/to/cache": "indicator/path"},
			excludeByPattern:    map[string]bool{"path/to": false},
			indicatorByCachePth: map[string]string{"path/to/cache": ""},
			wantErr:             false,
		},
		{
			name:                "exclude match, remove",
			indicatorByPth:      map[string]string{"path/to/cache": "indicator/path"},
			excludeByPattern:    map[string]bool{"path/to": true},
			indicatorByCachePth: map[string]string{},
			wantErr:             false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := interleave(tt.indicatorByPth, tt.excludeByPattern)
			if (err != nil) != tt.wantErr {
				t.Errorf("interleave() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.indicatorByCachePth) {
				t.Errorf("interleave() = %v, want %v", got, tt.indicatorByCachePth)
			}
		})
	}
}
