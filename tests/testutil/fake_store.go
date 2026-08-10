package testutil

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"sort"
	"strings"
	"sync"

	"Vylux/internal/storage"
)

var _ storage.Storage = (*FakeStore)(nil)

type fakeObject struct {
	data []byte
}

// FakeStore is an in-memory storage implementation for fast unit tests.
type FakeStore struct {
	mu      sync.RWMutex
	buckets map[string]struct{}
	objects map[string]fakeObject
}

func NewFakeStore() *FakeStore {
	return &FakeStore{
		buckets: map[string]struct{}{},
		objects: map[string]fakeObject{},
	}
}

func (s *FakeStore) Get(_ context.Context, bucket, key string) (io.ReadCloser, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	obj, ok := s.objects[storeKey(bucket, key)]
	if !ok {
		return nil, fs.ErrNotExist
	}

	return io.NopCloser(bytes.NewReader(append([]byte(nil), obj.data...))), nil
}

func (s *FakeStore) Put(_ context.Context, bucket, key string, data io.Reader, _ string) error {
	body, err := io.ReadAll(data)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.buckets[bucket] = struct{}{}
	s.objects[storeKey(bucket, key)] = fakeObject{data: body}

	return nil
}

func (s *FakeStore) Exists(_ context.Context, bucket, key string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.objects[storeKey(bucket, key)]
	return ok, nil
}

func (s *FakeStore) Size(_ context.Context, bucket, key string) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	obj, ok := s.objects[storeKey(bucket, key)]
	if !ok {
		return 0, fs.ErrNotExist
	}

	return int64(len(obj.data)), nil
}

func (s *FakeStore) Delete(_ context.Context, bucket, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, storeKey(bucket, key))
	return nil
}

func (s *FakeStore) List(_ context.Context, bucket, prefix string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	keys := make([]string, 0)
	needle := bucket + "\x00"
	for compound := range s.objects {
		if !strings.HasPrefix(compound, needle) {
			continue
		}
		key := strings.TrimPrefix(compound, needle)
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)

	return keys, nil
}

func (s *FakeStore) ListPage(
	ctx context.Context,
	bucket, prefix, continuation string,
	limit int32,
) ([]string, string, bool, error) {
	keys, err := s.List(ctx, bucket, prefix)
	if err != nil {
		return nil, "", false, err
	}

	start := sort.SearchStrings(keys, continuation)
	if continuation != "" && start < len(keys) && keys[start] == continuation {
		start++
	}
	if start > len(keys) {
		start = len(keys)
	}
	end := start + int(limit)
	if end > len(keys) {
		end = len(keys)
	}
	page := append([]string(nil), keys[start:end]...)
	done := end == len(keys)
	next := ""
	if !done && len(page) > 0 {
		next = page[len(page)-1]
	}
	return page, next, done, nil
}

func (s *FakeStore) HeadBucket(_ context.Context, bucket string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.buckets[bucket]; ok {
		return nil
	}
	for compound := range s.objects {
		if strings.HasPrefix(compound, bucket+"\x00") {
			return nil
		}
	}

	return errors.New("bucket does not exist")
}

func (s *FakeStore) CheckUnversioned(context.Context, string) error {
	return nil
}

func storeKey(bucket, key string) string {
	return bucket + "\x00" + key
}
