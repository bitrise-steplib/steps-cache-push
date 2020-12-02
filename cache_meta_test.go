package main

import (
	"errors"
	"os"
	"reflect"
	"testing"
	"time"
)

// region cacheMetaReader

type mockCacheMetaReader struct {
	meta CacheMeta
	err  error
}

func (r mockCacheMetaReader) readCacheMeta(_ string) (CacheMeta, error) {
	return r.meta, r.err
}

// endregion

// region cachePullEndTimeReader

type mockCachePullEndTimeReader struct {
	timeStamp int64
	err       error
}

func (r mockCachePullEndTimeReader) readCachePullEndTime() (int64, error) {
	return r.timeStamp, r.err
}

// endregion

//region accessTimeProvider

type mockAccessTimeProvider struct {
	aTime int64
	err   error
}

func (p mockAccessTimeProvider) accessTime(_ string) (int64, error) {
	return p.aTime, p.err
}

// endregion

// region timeProvider

type mockTimeProvider struct {
	currentTime int64
}

func (p mockTimeProvider) now() int64 {
	return p.currentTime
}

// endregion

// region fileInfoProvider

type mockFileInfoProvider struct {
	mode  os.FileMode
	isDir bool
}

type fakeFileInfo struct {
	mode  os.FileMode
	isDir bool
}

func (f fakeFileInfo) Name() string {
	panic("implement me")
}

func (f fakeFileInfo) Size() int64 {
	panic("implement me")
}

func (f fakeFileInfo) Mode() os.FileMode {
	return f.mode
}

func (f fakeFileInfo) ModTime() time.Time {
	panic("implement me")
}

func (f fakeFileInfo) IsDir() bool {
	return f.isDir
}

func (f fakeFileInfo) Sys() interface{} {
	panic("implement me")
}

func (p mockFileInfoProvider) lstat(_ string) (os.FileInfo, error) {
	return fakeFileInfo{mode: p.mode, isDir: p.isDir}, nil
}

// endregion

func TestCacheMetaGenerator_generateCacheMeta(t *testing.T) {
	type fields struct {
		cacheMetaReader        cacheMetaReader
		cachePullEndTimeReader cachePullEndTimeReader
		accessTimeProvider     accessTimeProvider
		timeProvider           timeProvider
		fileInfoProvider       fileInfoProvider
	}
	type args struct {
		oldPathToIndicatorPath map[string]string
	}
	tests := []struct {
		name                    string
		fields                  fields
		args                    args
		wantCacheMeta           CacheMeta
		wantPathToIndicatorPath map[string]string
		wantErr                 bool
	}{
		{
			name: "generates cache meta for new cache archive",
			fields: fields{
				cacheMetaReader:        mockCacheMetaReader{meta: nil},
				cachePullEndTimeReader: mockCachePullEndTimeReader{timeStamp: 0},
				accessTimeProvider:     mockAccessTimeProvider{aTime: 1},
				timeProvider:           mockTimeProvider{currentTime: 2},
				fileInfoProvider:       mockFileInfoProvider{},
			},
			args: args{
				oldPathToIndicatorPath: map[string]string{"a": ""},
			},
			wantCacheMeta:           CacheMeta{"a": Meta{AccessTime: 1}},
			wantPathToIndicatorPath: map[string]string{"a": ""},
		},
		{
			name: "removes expired files",
			fields: fields{
				cacheMetaReader:        mockCacheMetaReader{meta: CacheMeta{"a": Meta{AccessTime: 1}}},
				cachePullEndTimeReader: mockCachePullEndTimeReader{timeStamp: 2},
				accessTimeProvider:     mockAccessTimeProvider{aTime: 1},
				timeProvider:           mockTimeProvider{currentTime: 2 + maxAge},
				fileInfoProvider:       mockFileInfoProvider{},
			},
			args: args{
				oldPathToIndicatorPath: map[string]string{"a": ""},
			},
			wantCacheMeta:           CacheMeta{},
			wantPathToIndicatorPath: map[string]string{},
		},
		{
			name: "keeps not yet expired files",
			fields: fields{
				cacheMetaReader:        mockCacheMetaReader{meta: CacheMeta{"a": Meta{AccessTime: 1}}},
				cachePullEndTimeReader: mockCachePullEndTimeReader{timeStamp: 2},
				accessTimeProvider:     mockAccessTimeProvider{aTime: 2},
				timeProvider:           mockTimeProvider{currentTime: 3},
				fileInfoProvider:       mockFileInfoProvider{},
			},
			args: args{
				oldPathToIndicatorPath: map[string]string{"a": ""},
			},
			wantCacheMeta:           CacheMeta{"a": Meta{AccessTime: 1}},
			wantPathToIndicatorPath: map[string]string{"a": ""},
		},
		{
			name: "adds meta to new accessed files",
			fields: fields{
				cacheMetaReader:        mockCacheMetaReader{meta: CacheMeta{"a": Meta{AccessTime: 1}}},
				cachePullEndTimeReader: mockCachePullEndTimeReader{timeStamp: 2},
				accessTimeProvider:     mockAccessTimeProvider{aTime: 3},
				timeProvider:           mockTimeProvider{currentTime: 4},
				fileInfoProvider:       mockFileInfoProvider{},
			},
			args: args{
				oldPathToIndicatorPath: map[string]string{"a": "", "b": ""},
			},
			wantCacheMeta:           CacheMeta{"a": Meta{AccessTime: 3}, "b": Meta{AccessTime: 3}},
			wantPathToIndicatorPath: map[string]string{"a": "", "b": ""},
		},
		{
			name: "adds meta to new not accessed files",
			fields: fields{
				cacheMetaReader:        mockCacheMetaReader{meta: CacheMeta{"a": Meta{AccessTime: 1}}},
				cachePullEndTimeReader: mockCachePullEndTimeReader{timeStamp: 3},
				accessTimeProvider:     mockAccessTimeProvider{aTime: 2},
				timeProvider:           mockTimeProvider{currentTime: 4},
				fileInfoProvider:       mockFileInfoProvider{},
			},
			args: args{
				oldPathToIndicatorPath: map[string]string{"a": "", "b": ""},
			},
			wantCacheMeta:           CacheMeta{"a": Meta{AccessTime: 1}, "b": Meta{AccessTime: 2}},
			wantPathToIndicatorPath: map[string]string{"a": "", "b": ""},
		},
		{
			name: "cache meta reader fails: file not found",
			fields: fields{
				cacheMetaReader:        mockCacheMetaReader{err: fileNotFoundError{}},
				cachePullEndTimeReader: mockCachePullEndTimeReader{timeStamp: 0},
				accessTimeProvider:     mockAccessTimeProvider{aTime: 1},
				timeProvider:           mockTimeProvider{currentTime: 2},
				fileInfoProvider:       mockFileInfoProvider{},
			},
			args: args{
				oldPathToIndicatorPath: map[string]string{"a": ""},
			},
			wantCacheMeta:           CacheMeta{"a": Meta{AccessTime: 1}},
			wantPathToIndicatorPath: map[string]string{"a": ""},
		},
		{
			name: "cache meta reader fails: other error",
			fields: fields{
				cacheMetaReader: mockCacheMetaReader{err: errors.New("missing permission")},
			},
			wantErr: true,
		},
		{
			name: "cache pull end time reader fails: file not found",
			fields: fields{
				cacheMetaReader:        mockCacheMetaReader{},
				cachePullEndTimeReader: mockCachePullEndTimeReader{err: fileNotFoundError{}},
				accessTimeProvider:     mockAccessTimeProvider{},
				timeProvider:           mockTimeProvider{},
				fileInfoProvider:       mockFileInfoProvider{},
			},
			args: args{
				oldPathToIndicatorPath: map[string]string{"a": ""},
			},
			wantCacheMeta:           CacheMeta{"a": Meta{AccessTime: 0}},
			wantPathToIndicatorPath: map[string]string{"a": ""},
		},
		{
			name: "cache pull end time reader fails: other error",
			fields: fields{
				cacheMetaReader:        mockCacheMetaReader{},
				cachePullEndTimeReader: mockCachePullEndTimeReader{err: errors.New("missing permission")},
			},
			wantErr: true,
		},
		{
			name: "access time provider error recovers",
			fields: fields{
				cacheMetaReader:        mockCacheMetaReader{meta: CacheMeta{"a": Meta{AccessTime: 1}}},
				cachePullEndTimeReader: mockCachePullEndTimeReader{timeStamp: 3},
				accessTimeProvider:     mockAccessTimeProvider{err: errors.New("missing permission")},
				timeProvider:           mockTimeProvider{currentTime: 4},
				fileInfoProvider:       mockFileInfoProvider{},
			},
			args: args{
				oldPathToIndicatorPath: map[string]string{"a": ""},
			},
			wantCacheMeta:           CacheMeta{},
			wantPathToIndicatorPath: map[string]string{"a": ""},
		},
		{
			name: "skip access time check on dirs",
			fields: fields{
				cacheMetaReader:        mockCacheMetaReader{},
				cachePullEndTimeReader: mockCachePullEndTimeReader{timeStamp: 3},
				accessTimeProvider:     mockAccessTimeProvider{aTime: 1},
				timeProvider:           mockTimeProvider{currentTime: 3 + maxAge},
				fileInfoProvider:       mockFileInfoProvider{isDir: true},
			},
			args: args{
				oldPathToIndicatorPath: map[string]string{"a": ""},
			},
			wantCacheMeta:           CacheMeta{},
			wantPathToIndicatorPath: map[string]string{"a": ""},
		},
		{
			name: "skip access time check on symlinks",
			fields: fields{
				cacheMetaReader:        mockCacheMetaReader{},
				cachePullEndTimeReader: mockCachePullEndTimeReader{timeStamp: 3},
				accessTimeProvider:     mockAccessTimeProvider{aTime: 1},
				timeProvider:           mockTimeProvider{currentTime: 3 + maxAge},
				fileInfoProvider:       mockFileInfoProvider{mode: os.ModeSymlink},
			},
			args: args{
				oldPathToIndicatorPath: map[string]string{"a": ""},
			},
			wantCacheMeta:           CacheMeta{},
			wantPathToIndicatorPath: map[string]string{"a": ""},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := cacheMetaGenerator{
				cacheMetaReader:        tt.fields.cacheMetaReader,
				cachePullEndTimeReader: tt.fields.cachePullEndTimeReader,
				accessTimeProvider:     tt.fields.accessTimeProvider,
				timeProvider:           tt.fields.timeProvider,
				fileInfoProvider:       tt.fields.fileInfoProvider,
			}
			got, got1, err := g.filterOldPathsAndUpdateMeta(tt.args.oldPathToIndicatorPath)
			if (err != nil) != tt.wantErr {
				t.Errorf("cacheMetaGenerator.filterOldPathsAndUpdateMeta() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.wantCacheMeta) {
				t.Errorf("cacheMetaGenerator.filterOldPathsAndUpdateMeta() got = %v, want %v", got, tt.wantCacheMeta)
			}
			if !reflect.DeepEqual(got1, tt.wantPathToIndicatorPath) {
				t.Errorf("cacheMetaGenerator.filterOldPathsAndUpdateMeta() got1 = %v, want %v", got1, tt.wantPathToIndicatorPath)
			}
		})
	}
}
