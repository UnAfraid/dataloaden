package dataloader

// DataLoader batches and caches requests
type DataLoader[T any] interface {
	// Load a User by key, batching and caching will be applied automatically
	Load(key string) (T, error)

	// LoadThunk returns a function that when called will block waiting for a User.
	// This method should be used if you want one goroutine to make requests to many
	// different data loaders without blocking until the thunk is called.
	LoadThunk(key string) func() (T, error)

	// LoadAll fetches many keys at once. It will be broken into appropriate sized
	// sub batches depending on how the loader is configured
	LoadAll(keys []string) ([]T, []error)

	// LoadAllThunk returns a function that when called will block waiting for a Users.
	// This method should be used if you want one goroutine to make requests to many
	// different data loaders without blocking until the thunk is called.
	LoadAllThunk(keys []string) func() ([]T, []error)

	// Prime the cache with the provided key and value. If the key already exists, no change is made
	// and false is returned.
	// (To forcefully prime the cache, clear the key first with loader.clear(key).prime(key, value).)
	Prime(key string, value T) bool

	// Clear the value at key from the cache, if it exists
	Clear(key string)
}
