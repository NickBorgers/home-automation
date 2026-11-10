package occupancy

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHubStartsEmpty(t *testing.T) {
	h := NewHub()
	occ, known := h.Occupied("kitchen")
	assert.False(t, occ)
	assert.False(t, known, "an area that was never published is not known")
	assert.Empty(t, h.Areas())
}

func TestHubPublishAndRead(t *testing.T) {
	h := NewHub()
	h.Publish(map[string]Status{
		"kitchen": {Occupied: true},
		"lounge":  {},
		"study":   {Occupied: true, Unknown: true},
	})

	tests := []struct {
		area         string
		occupied, ok bool
	}{
		{"kitchen", true, true},
		{"lounge", false, true},
		{"study", false, false},
		{"attic", false, false},
	}
	for _, tt := range tests {
		occ, known := h.Occupied(tt.area)
		assert.Equal(t, tt.occupied, occ, tt.area)
		assert.Equal(t, tt.ok, known, tt.area)
	}
	assert.Equal(t, []string{"kitchen", "lounge", "study"}, h.Areas())

	h.Clear()
	_, known := h.Occupied("kitchen")
	assert.False(t, known, "Clear forgets every area")
}

func TestHubPublishCopiesInput(t *testing.T) {
	h := NewHub()
	in := map[string]Status{"kitchen": {Occupied: true}}
	h.Publish(in)
	in["kitchen"] = Status{}
	occ, _ := h.Occupied("kitchen")
	assert.True(t, occ)
}

func TestHubSubscribersRunOnlyOnChange(t *testing.T) {
	h := NewHub()
	var calls atomic.Int32
	unsub := h.Subscribe(func() { calls.Add(1) })

	h.Publish(map[string]Status{"kitchen": {}})
	assert.Equal(t, int32(1), calls.Load())
	h.Publish(map[string]Status{"kitchen": {}})
	assert.Equal(t, int32(1), calls.Load(), "the same states again is no change")
	h.Publish(map[string]Status{"kitchen": {Occupied: true}})
	assert.Equal(t, int32(2), calls.Load())
	h.Publish(map[string]Status{"kitchen": {Occupied: true}, "lounge": {}})
	assert.Equal(t, int32(3), calls.Load(), "a new area is a change")
	h.Publish(map[string]Status{"lounge": {}, "study": {}})
	assert.Equal(t, int32(4), calls.Load(), "a replaced area is a change")

	unsub()
	h.Clear()
	assert.Equal(t, int32(4), calls.Load(), "no calls after unsubscribe")
}

func TestHubSubscriberMayReadHub(t *testing.T) {
	h := NewHub()
	var got bool
	h.Subscribe(func() { got, _ = h.Occupied("kitchen") })
	h.Publish(map[string]Status{"kitchen": {Occupied: true}})
	assert.True(t, got, "callbacks run without the lock held")
}

func TestHubConcurrentUse(t *testing.T) {
	h := NewHub()
	h.Subscribe(func() { h.Occupied("kitchen") })
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				h.Publish(map[string]Status{"kitchen": {Occupied: (i+j)%2 == 0}})
				h.Occupied("kitchen")
				h.Areas()
			}
		}(i)
	}
	wg.Wait()
}
