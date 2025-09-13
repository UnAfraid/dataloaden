package dataloaden

import (
	"errors"
	"sync"
	"time"
)

// DataLoader batches and caches requests
type DataLoader[K comparable, V any] interface {
	// Load a User by key, batching and caching will be applied automatically
	Load(key K) (*V, error)

	// LoadThunk returns a function that when called will block waiting for a User.
	// This method should be used if you want one goroutine to make requests to many
	// different data loaders without blocking until the thunk is called.
	LoadThunk(key K) func() (*V, error)

	// LoadAll fetches many keys at once. It will be broken into appropriate sized
	// sub batches depending on how the loader is configured
	LoadAll(keys []K) ([]*V, []error)

	// LoadAllThunk returns a function that when called will block waiting for a Users.
	// This method should be used if you want one goroutine to make requests to many
	// different data loaders without blocking until the thunk is called.
	LoadAllThunk(keys []K) func() ([]*V, []error)

	// Prime the cache with the provided key and value. If the key already exists, no change is made
	// and false is returned.
	// (To forcefully prime the cache, clear the key first with loader.clear(key).prime(key, value).)
	Prime(key K, value *V) bool

	// Clear the value at a key from the cache if it exists
	Clear(key K)
}

// NewDataLoader creates a new data loader given a fetch, wait and maxBatch
func NewDataLoader[K comparable, V any](fetchFn func(keys []K) ([]*V, []error), waitDuration time.Duration, maxBatch int) DataLoader[K, V] {
	return &genericLoader[K, V]{
		fetch:    fetchFn,
		wait:     waitDuration,
		maxBatch: maxBatch,
	}
}

type genericLoader[K comparable, V any] struct {
	// this method provides the data for the loader
	fetch func(keys []K) ([]*V, []error)

	// how long to done before sending a batch
	wait time.Duration

	// this will limit the maximum number of keys to send in one batch, 0 = no limit
	maxBatch int

	// lazily created cache
	cache map[K]*V

	// the current batch. keys will continue to be collected until timeout is hit,
	// then everything will be sent to the fetch method and out to the listeners
	batch *genericLoaderBatch[K, V]

	// mutex to prevent races
	mu sync.Mutex
}

type genericLoaderBatch[K comparable, V any] struct {
	keys    []K
	data    []*V
	error   []error
	closing bool
	done    chan struct{}
}

// Load a genericLoader by key, batching and caching will be applied automatically
func (l *genericLoader[K, V]) Load(key K) (*V, error) {
	return l.LoadThunk(key)()
}

// LoadThunk returns a function that when called will block waiting for a genericLoader.
// This method should be used if you want one goroutine to make requests to many
// different data loaders without blocking until the thunk is called.
func (l *genericLoader[K, V]) LoadThunk(key K) func() (*V, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if it, ok := l.cache[key]; ok {
		return func() (*V, error) {
			return it, nil
		}
	}
	if l.batch == nil {
		l.batch = &genericLoaderBatch[K, V]{done: make(chan struct{})}
	}
	batch := l.batch
	pos := batch.keyIndex(l, key)

	return func() (*V, error) {
		<-batch.done

		var data *V
		if pos < len(batch.data) {
			data = batch.data[pos]
		}

		var errs error
		for _, err := range batch.error {
			if err == nil {
				continue
			}
			errs = errors.Join(errs, err)
		}
		if errs != nil {
			return data, errs
		}

		l.mu.Lock()
		defer l.mu.Unlock()
		l.unsafeSet(key, data)

		return data, nil
	}
}

// LoadAll fetches many keys at once. It will be broken into appropriate sized
// sub batches depending on how the loader is configured
func (l *genericLoader[K, V]) LoadAll(keys []K) ([]*V, []error) {
	results := make([]func() (*V, error), len(keys))

	for i, key := range keys {
		results[i] = l.LoadThunk(key)
	}

	users := make([]*V, len(keys))
	errs := make([]error, len(keys))
	for i, thunk := range results {
		users[i], errs[i] = thunk()
	}
	return users, errs
}

// LoadAllThunk returns a function that when called will block waiting for a Generic Data.
// This method should be used if you want one goroutine to make requests to many
// different data loaders without blocking until the thunk is called.
func (l *genericLoader[K, V]) LoadAllThunk(keys []K) func() ([]*V, []error) {
	results := make([]func() (*V, error), len(keys))
	for i, key := range keys {
		results[i] = l.LoadThunk(key)
	}
	return func() ([]*V, []error) {
		users := make([]*V, len(keys))
		errs := make([]error, len(keys))
		for i, thunk := range results {
			users[i], errs[i] = thunk()
		}
		return users, errs
	}
}

// Prime the cache with the provided key and value. If the key already exists, no change is made
// and false is returned.
// (To forcefully prime the cache, clear the key first with loader.clear(key).prime(key, value).)
func (l *genericLoader[K, V]) Prime(key K, value *V) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	var found bool
	if _, found = l.cache[key]; !found {
		// to make a copy when writing to the cache, it's easy to pass a pointer in from a loop var
		// and end up with the whole cache pointing to the same value.
		cpy := *value
		l.unsafeSet(key, &cpy)
	}
	return !found
}

// Clear the value at key from the cache, if it exists
func (l *genericLoader[K, V]) Clear(key K) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.cache, key)
}

func (l *genericLoader[K, V]) unsafeSet(key K, value *V) {
	if l.cache == nil {
		l.cache = map[K]*V{}
	}
	l.cache[key] = value
}

// keyIndex will return the location of the key in the batch, if it's not found,
// it will add the key to the batch
func (b *genericLoaderBatch[K, V]) keyIndex(l *genericLoader[K, V], key K) int {
	for i, existingKey := range b.keys {
		if key == existingKey {
			return i
		}
	}

	pos := len(b.keys)
	b.keys = append(b.keys, key)
	if pos == 0 {
		go b.startTimer(l)
	}

	if l.maxBatch != 0 && pos >= l.maxBatch-1 {
		if !b.closing {
			b.closing = true
			l.batch = nil
			go b.end(l)
		}
	}

	return pos
}

func (b *genericLoaderBatch[K, V]) startTimer(l *genericLoader[K, V]) {
	time.Sleep(l.wait)
	l.mu.Lock()
	defer l.mu.Unlock()

	// we must have hit a batch limit and are already finalizing this batch
	if b.closing {
		return
	}

	l.batch = nil
	b.end(l)
}

func (b *genericLoaderBatch[K, V]) end(l *genericLoader[K, V]) {
	b.data, b.error = l.fetch(b.keys)
	close(b.done)
}
