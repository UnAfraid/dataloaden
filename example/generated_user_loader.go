// Code generated by github.com/UnAfraid/dataloaden, DO NOT EDIT.

package example

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/UnAfraid/dataloaden/v2/dataloader"
	"github.com/hashicorp/go-multierror"
)

// NewUserLoader creates a new UserLoader given a fetch, wait, and maxBatch
func NewUserLoader(config dataloader.Config[string, *User]) dataloader.DataLoader[string, *User] {
	dl := &UserLoader{
		fetch:        config.Fetch,
		wait:         config.Wait,
		formatErrors: config.FormatErrors,
		maxBatch:     config.MaxBatch,
	}
	if dl.formatErrors == nil {
		dl.formatErrors = dl.defaultFormatErrors
	}
	return dl
}

// UserLoader batches and caches requests
type UserLoader struct {
	// this method provides the data for the loader
	fetch func(keys []string) ([]*User, []error)

	// how long to done before sending a batch
	wait time.Duration

	// this will limit the maximum number of keys to send in one batch, 0 = no limit
	maxBatch int

	// this method will format errors
	formatErrors func([]error) string

	// INTERNAL

	// lazily created cache
	cache map[string]*User

	// the current batch. keys will continue to be collected until timeout is hit,
	// then everything will be sent to the fetch method and out to the listeners
	batch *userLoaderBatch

	// mutex to prevent races
	mu sync.Mutex
}

type userLoaderBatch struct {
	keys    []string
	data    []*User
	error   []error
	closing bool
	done    chan struct{}
}

// Load a User by key, batching and caching will be applied automatically
func (l *UserLoader) Load(key string) (*User, error) {
	return l.LoadThunk(key)()
}

// LoadThunk returns a function that when called will block waiting for a User.
// This method should be used if you want one goroutine to make requests to many
// different data loaders without blocking until the thunk is called.
func (l *UserLoader) LoadThunk(key string) func() (*User, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if it, ok := l.cache[key]; ok {
		return func() (*User, error) {
			return it, nil
		}
	}
	if l.batch == nil {
		l.batch = &userLoaderBatch{done: make(chan struct{})}
	}
	batch := l.batch
	pos := batch.keyIndex(l, key)

	return func() (*User, error) {
		<-batch.done

		var data *User
		if pos < len(batch.data) {
			data = batch.data[pos]
		}

		var errs error
		for _, err := range batch.error {
			if err == nil {
				continue
			}
			if errs == nil {
				errs = err
			} else {
				errs = multierror.Append(errs, err)
			}
		}
		if errs != nil {
			if multiErr, ok := errs.(*multierror.Error); ok {
				multiErr.ErrorFormat = l.formatErrors
			}
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
func (l *UserLoader) LoadAll(keys []string) ([]*User, []error) {
	results := make([]func() (*User, error), len(keys))

	for i, key := range keys {
		results[i] = l.LoadThunk(key)
	}

	users := make([]*User, len(keys))
	errors := make([]error, len(keys))
	for i, thunk := range results {
		users[i], errors[i] = thunk()
	}
	return users, errors
}

// LoadAllThunk returns a function that when called will block waiting for a Users.
// This method should be used if you want one goroutine to make requests to many
// different data loaders without blocking until the thunk is called.
func (l *UserLoader) LoadAllThunk(keys []string) func() ([]*User, []error) {
	results := make([]func() (*User, error), len(keys))
	for i, key := range keys {
		results[i] = l.LoadThunk(key)
	}
	return func() ([]*User, []error) {
		users := make([]*User, len(keys))
		errors := make([]error, len(keys))
		for i, thunk := range results {
			users[i], errors[i] = thunk()
		}
		return users, errors
	}
}

// Prime the cache with the provided key and value. If the key already exists, no change is made
// and false is returned.
// (To forcefully prime the cache, clear the key first with loader.clear(key).prime(key, value).)
func (l *UserLoader) Prime(key string, value *User) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	var found bool
	if _, found = l.cache[key]; !found {
		// make a copy when writing to the cache, its easy to pass a pointer in from a loop var
		// and end up with the whole cache pointing to the same value.
		cpy := *value
		l.unsafeSet(key, &cpy)
	}
	return !found
}

// Clear the value at key from the cache, if it exists
func (l *UserLoader) Clear(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.cache, key)
}

// defaultFormatErrors would format multiple errors
func (l *UserLoader) defaultFormatErrors(errors []error) string {
	if len(errors) == 1 {
		return errors[0].Error()
	}

	countsByErrors := make(map[string]int)
	for _, err := range errors {
		countsByErrors[err.Error()]++
	}

	type errorOccurrences struct {
		error       string
		occurrences int
	}

	var sortedErrorOccurrences []errorOccurrences
	for err, count := range countsByErrors {
		sortedErrorOccurrences = append(sortedErrorOccurrences, errorOccurrences{
			error:       err,
			occurrences: count,
		})
	}

	sort.Slice(sortedErrorOccurrences, func(i, j int) bool {
		return sortedErrorOccurrences[i].occurrences > sortedErrorOccurrences[j].occurrences
	})

	var sb strings.Builder
	for _, seo := range sortedErrorOccurrences {
		sb.WriteString(" * ")
		sb.WriteString(strconv.Itoa(seo.occurrences))
		sb.WriteString(" ")
		sb.WriteString(seo.error)
		sb.WriteString("\n")
	}

	return fmt.Sprintf("%d errors occurred:\n%s\n", len(errors), sb.String())
}

func (l *UserLoader) unsafeSet(key string, value *User) {
	if l.cache == nil {
		l.cache = map[string]*User{}
	}
	l.cache[key] = value
}

// keyIndex will return the location of the key in the batch, if its not found
// it will add the key to the batch
func (b *userLoaderBatch) keyIndex(l *UserLoader, key string) int {
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

func (b *userLoaderBatch) startTimer(l *UserLoader) {
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

func (b *userLoaderBatch) end(l *UserLoader) {
	b.data, b.error = l.fetch(b.keys)
	close(b.done)
}
