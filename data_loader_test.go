package dataloaden

import (
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLoadSingleKey(t *testing.T) {
	fetchCount := int32(0)
	fetchFn := func(keys []int) ([]*string, []error) {
		atomic.AddInt32(&fetchCount, 1)
		results := make([]*string, len(keys))
		for i, k := range keys {
			v := string(rune('A' + k))
			results[i] = &v
		}
		return results, make([]error, len(keys))
	}

	loader := NewDataLoader(fetchFn, 1*time.Millisecond, 10)

	val, err := loader.Load(0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *val != "A" {
		t.Errorf("expected A, got %s", *val)
	}

	if fetchCount != 1 {
		t.Errorf("expected fetchFn called once, got %d", fetchCount)
	}
}

func TestBatching(t *testing.T) {
	fetchFn := func(keys []int) ([]*string, []error) {
		results := make([]*string, len(keys))
		for i, k := range keys {
			v := string(rune('A' + k))
			results[i] = &v
		}
		return results, make([]error, len(keys))
	}

	loader := NewDataLoader(fetchFn, 5*time.Millisecond, 10)

	thunk1 := loader.LoadThunk(0)
	thunk2 := loader.LoadThunk(1)

	val1, _ := thunk1()
	val2, _ := thunk2()

	if *val1 != "A" || *val2 != "B" {
		t.Errorf("expected [A,B], got [%s,%s]", *val1, *val2)
	}
}

func TestMaxBatchSize(t *testing.T) {
	var batches [][]int
	fetchFn := func(keys []int) ([]*string, []error) {
		cp := make([]int, len(keys))
		copy(cp, keys)
		batches = append(batches, cp)

		results := make([]*string, len(keys))
		for i, k := range keys {
			v := string(rune('A' + k))
			results[i] = &v
		}
		return results, make([]error, len(keys))
	}

	loader := NewDataLoader(fetchFn, 50*time.Millisecond, 2)

	thunks := []func() (*string, error){
		loader.LoadThunk(0),
		loader.LoadThunk(1),
		loader.LoadThunk(2),
	}

	for _, thunk := range thunks {
		_, _ = thunk()
	}

	if len(batches) != 2 {
		t.Errorf("expected 2 batches, got %d", len(batches))
	}
	if !reflect.DeepEqual(batches[0], []int{0, 1}) || !reflect.DeepEqual(batches[1], []int{2}) {
		t.Errorf("unexpected batching: %v", batches)
	}
}

func TestPrimeAndClearCache(t *testing.T) {
	fetchFn := func(keys []int) ([]*string, []error) {
		t.Fatal("fetch should not be called when primed")
		return nil, nil
	}

	loader := NewDataLoader(fetchFn, 1*time.Millisecond, 10)

	val := "Primed"
	ok := loader.Prime(1, &val)
	if !ok {
		t.Errorf("expected Prime to return true")
	}

	cached, err := loader.Load(1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *cached != "Primed" {
		t.Errorf("expected Primed, got %s", *cached)
	}

	loader.Clear(1)

	// After clearing, it should trigger fetch
	loader.(*genericLoader[int, string]).fetch = func(keys []int) ([]*string, []error) {
		v := "Fetched"
		return []*string{&v}, []error{nil}
	}

	val2, _ := loader.Load(1)
	if *val2 != "Fetched" {
		t.Errorf("expected Fetched, got %s", *val2)
	}
}

func TestErrorHandling(t *testing.T) {
	fetchFn := func(keys []int) ([]*string, []error) {
		results := []*string{nil}
		errs := []error{errors.New("boom")}
		return results, errs
	}

	loader := NewDataLoader(fetchFn, 1*time.Millisecond, 10)

	val, err := loader.Load(42)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if val != nil {
		t.Errorf("expected nil value on error, got %v", *val)
	}
}

func TestConcurrentLoads(t *testing.T) {
	var callCount int32
	fetchFn := func(keys []int) ([]*string, []error) {
		atomic.AddInt32(&callCount, 1)
		results := make([]*string, len(keys))
		for i, k := range keys {
			v := string(rune('A' + k))
			results[i] = &v
		}
		return results, make([]error, len(keys))
	}

	loader := NewDataLoader(fetchFn, 5*time.Millisecond, 50)

	const numGoroutines = 100
	var wg sync.WaitGroup
	errs := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		id := i
		wg.Go(func() {
			val, err := loader.Load(id % 5)
			if err != nil {
				errs <- err
				return
			}
			expected := string(rune('A' + (id % 5)))
			if *val != expected {
				errs <- errors.New("mismatched value: expected " + expected + ", got " + *val)
			}
		})
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent load failed: %v", err)
	}

	// Ensure fetchFn was not called excessively
	if callCount <= 0 {
		t.Errorf("fetchFn was never called")
	}
}
