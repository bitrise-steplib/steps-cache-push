package main

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/bitrise-io/go-utils/pathutil"
)

func Test_Test_cacheDescriptorModTime(t *testing.T) {
	tmpDir, err := pathutil.NormalizedOSTempDirPath("cache")
	if err != nil {
		t.Fatalf("failed to create tmp dir: %s", err)
		return
	}

	pths := map[string]string{
		filepath.Join(tmpDir, "subdir", "file1"): "some content",
	}

	// store time frame to test mod time change indicator method
	start := time.Now()
	// start and end dates needs to be rounded to second, since file info stores modtime in second precision
	start = time.Date(start.Year(), start.Month(), start.Day(), start.Hour(), start.Minute(), start.Second(), 0, start.Location())
	createDirStruct(t, pths)
	end := time.Now()
	end = time.Date(end.Year(), end.Month(), end.Day(), end.Hour(), end.Minute(), end.Second(), 0, end.Location())

	t.Log("mod time method")
	{
		descriptor, err := cacheDescriptor(map[string]map[string]bool{filepath.Join(tmpDir, "subdir", "file1"): {filepath.Join(tmpDir, "subdir", "file1"): true}}, MODTIME)
		if err != nil {
			t.Errorf("cacheDescriptor() error = %v, wantErr %v", err, false)
			return
		}

		if len(descriptor) != 1 {
			t.Errorf("want 1 descriptor item, got: %d", len(descriptor))
			return
		}

		for modTimeStr := range descriptor {
			modTime, err := strconv.Atoi(modTimeStr)
			if err != nil {
				t.Errorf("failed to int parse: %s, error: %s", modTimeStr, err)
				return
			}
			mod := time.Unix(int64(modTime), 0)
			if start.Before(mod) || end.After(mod) {
				t.Errorf("invalid modtime (%v) should be > %v && < %v", mod, start, end)
				return

			}
		}
	}
}

func Test_cacheDescriptor(t *testing.T) {
	tmpDir, err := pathutil.NormalizedOSTempDirPath("cache")
	if err != nil {
		t.Fatalf("failed to create tmp dir: %s", err)
		return
	}

	pths := map[string]string{
		filepath.Join(tmpDir, "subdir", "file1"): "some content",
		filepath.Join(tmpDir, "subdir", "file2"): "",
	}

	createDirStruct(t, pths)

	tests := []struct {
		name         string
		indicatorMap map[string]map[string]bool
		method       ChangeIndicator
		descriptor   map[string]map[string]bool
		wantErr      bool
	}{
		{
			name:         "no change indicator",
			indicatorMap: map[string]map[string]bool{"": {filepath.Join(tmpDir, "subdir", "file1"): true}},
			method:       MD5,
			descriptor:   map[string]map[string]bool{"-": {filepath.Join(tmpDir, "subdir", "file1"): true}},
			wantErr:      false,
		},
		{
			name:         "content hash method",
			indicatorMap: map[string]map[string]bool{filepath.Join(tmpDir, "subdir", "file2"): {filepath.Join(tmpDir, "subdir", "file1"): true}},
			method:       MD5,
			descriptor:   map[string]map[string]bool{"d41d8cd98f00b204e9800998ecf8427e": {filepath.Join(tmpDir, "subdir", "file1"): true}}, // empty string MD5 hash
			wantErr:      false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			descriptor, err := cacheDescriptor(tt.indicatorMap, tt.method)
			if (err != nil) != tt.wantErr {
				t.Errorf("cacheDescriptor() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(descriptor, tt.descriptor) {
				t.Errorf("cacheDescriptor() = %v, want %v", descriptor, tt.descriptor)
			}
		})
	}
}

func Test_compare(t *testing.T) {
	tests := []struct {
		name string
		old  map[string]map[string]bool
		new  map[string]map[string]bool
		want result
	}{
		{
			name: "empty test",
			old:  map[string]map[string]bool{},
			new:  map[string]map[string]bool{},
			want: result{},
		},
		{
			name: "removed",
			old:  map[string]map[string]bool{"indicator": {"pth": true}},
			new:  map[string]map[string]bool{},
			want: result{removed: []string{"pth"}},
		},
		{
			name: "ignored removed",
			old:  map[string]map[string]bool{"-": {"pth": true}},
			new:  map[string]map[string]bool{},
			want: result{removedIgnored: []string{"pth"}},
		},
		{
			name: "changed",
			old:  map[string]map[string]bool{"indicator1": {"pth": true}},
			new:  map[string]map[string]bool{"indicator2": {"pth": true}},
			want: result{changed: []string{"pth"}},
		},
		{
			name: "matching",
			old:  map[string]map[string]bool{"indicator": {"pth": true}},
			new:  map[string]map[string]bool{"indicator": {"pth": true}},
			want: result{matching: []string{"pth"}},
		},
		{
			name: "added",
			old:  map[string]map[string]bool{},
			new:  map[string]map[string]bool{"indicator": {"pth": true}},
			want: result{added: []string{"pth"}},
		},
		{
			name: "ignored added",
			old:  map[string]map[string]bool{},
			new:  map[string]map[string]bool{"-": {"pth": true}},
			want: result{addedIgnored: []string{"pth"}},
		},
		{
			name: "complex",
			old: map[string]map[string]bool{
				"indicator":  {"removedPth": true, "matching": true},
				"-":          {"ignoredRemovedPth": true},
				"indicator1": {"changed": true},
				// "added":             "indicator",
				// "ignoredAdded":      "-",
			},
			new: map[string]map[string]bool{
				// "removedPth":        "indicator",
				// "ignoredRemovedPth": "-",
				"indicator2": {"changed": true},
				"indicator":  {"matching": true, "added": true},
				"-":          {"ignoredAdded": true},
			},
			want: result{
				removed:        []string{"removedPth"},
				removedIgnored: []string{"ignoredRemovedPth"},
				changed:        []string{"changed"},
				matching:       []string{"matching"},
				added:          []string{"added"},
				addedIgnored:   []string{"ignoredAdded"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if gotR := compare(tt.old, tt.new); !reflect.DeepEqual(gotR, tt.want) {
				t.Errorf("compare() = %v, want %v", gotR, tt.want)
			}
		})
	}
}

func Test_result_hasChanges(t *testing.T) {
	tests := []struct {
		name            string
		removedIgnored  []string
		removed         []string
		changed         []string
		matching        []string
		addedIgnored    []string
		added           []string
		triggerNewCache bool
	}{
		// do not trigger new cache
		{
			name:            "empty",
			triggerNewCache: false,
		},
		{
			name:            "ignored removed",
			removedIgnored:  []string{"pth"},
			triggerNewCache: false,
		},
		{
			name:            "matching",
			matching:        []string{"pth"},
			triggerNewCache: false,
		},
		{
			name:            "ignored added",
			addedIgnored:    []string{"pth"},
			triggerNewCache: false,
		},
		// trigger new cache
		{
			name:            "removed",
			removed:         []string{"pth"},
			triggerNewCache: true,
		},
		{
			name:            "changed",
			changed:         []string{"pth"},
			triggerNewCache: true,
		},
		{
			name:            "added",
			added:           []string{"pth"},
			triggerNewCache: true,
		},
		{
			name:            "complex",
			removedIgnored:  []string{"pth"},
			removed:         []string{"pth"},
			changed:         []string{"pth"},
			matching:        []string{"pth"},
			addedIgnored:    []string{"pth"},
			added:           []string{"pth"},
			triggerNewCache: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := result{
				removedIgnored: tt.removedIgnored,
				removed:        tt.removed,
				changed:        tt.changed,
				matching:       tt.matching,
				addedIgnored:   tt.addedIgnored,
				added:          tt.added,
			}
			if got := r.hasChanges(); got != tt.triggerNewCache {
				t.Errorf("result.triggerNewCache() = %v, want %v", got, tt.triggerNewCache)
			}
		})
	}
}

func Test_readCacheDescriptor(t *testing.T) {
	desired := map[string]string{
		"path/to/cache": "indicator",
	}

	content, err := json.Marshal(desired)
	if err != nil {
		t.Fatalf("Failed to create descriptor: %s", err)
	}

	tmpDir, err := pathutil.NormalizedOSTempDirPath("cache")
	if err != nil {
		t.Fatalf("failed to create tmp dir: %s", err)
		return
	}
	pth := filepath.Join(tmpDir, "descriptor")

	createDirStruct(t, map[string]string{pth: string(content)})

	tests := []struct {
		name       string
		pth        string
		descriptor map[string]string
		wantErr    bool
	}{
		{
			name:       "No path provided",
			pth:        "",
			descriptor: nil,
			wantErr:    true,
		},
		{
			name:       "Not existing path",
			pth:        "/not/existing/path",
			descriptor: nil,
			wantErr:    false,
		},
		{
			name:       "Existing descriptor",
			pth:        pth,
			descriptor: desired,
			wantErr:    false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			descriptor, err := readCacheDescriptor(tt.pth)
			if (err != nil) != tt.wantErr {
				t.Errorf("readCacheDescriptor() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(descriptor, tt.descriptor) {
				t.Errorf("readCacheDescriptor() descriptor = %v, want %v", descriptor, tt.descriptor)
			}
		})
	}
}
