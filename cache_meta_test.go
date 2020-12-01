package main

import (
	"errors"
	"reflect"
	"testing"
)

func TestCacheMetaGenerator_generateCacheMeta(t *testing.T) {
	type fields struct {
		cacheMetaReader        cacheMetaReader
		cachePullEndTimeReader cachePullEndTimeReader
		accessTimeProvider     accessTimeProvider
		timeProvider           timeProvider
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
			name: "access time provider fails",
			fields: fields{
				cacheMetaReader:        mockCacheMetaReader{},
				cachePullEndTimeReader: mockCachePullEndTimeReader{},
				accessTimeProvider:     mockAccessTimeProvider{err: errors.New("missing permission")},
				timeProvider:           mockTimeProvider{},
			},
			args: args{
				oldPathToIndicatorPath: map[string]string{"a": ""},
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := cacheMetaGenerator{
				cacheMetaReader:        tt.fields.cacheMetaReader,
				cachePullEndTimeReader: tt.fields.cachePullEndTimeReader,
				accessTimeProvider:     tt.fields.accessTimeProvider,
				timeProvider:           tt.fields.timeProvider,
			}
			got, got1, err := g.generateCacheMeta(tt.args.oldPathToIndicatorPath)
			if (err != nil) != tt.wantErr {
				t.Errorf("cacheMetaGenerator.generateCacheMeta() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.wantCacheMeta) {
				t.Errorf("cacheMetaGenerator.generateCacheMeta() got = %v, want %v", got, tt.wantCacheMeta)
			}
			if !reflect.DeepEqual(got1, tt.wantPathToIndicatorPath) {
				t.Errorf("cacheMetaGenerator.generateCacheMeta() got1 = %v, want %v", got1, tt.wantPathToIndicatorPath)
			}
		})
	}
}

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
