package lighthold

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestHub(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	t.Run("hold and expiry", func(t *testing.T) {
		hub := NewHub()
		hub.Hold("light.example", "plugin-a", now.Add(time.Minute))
		assert.True(t, hub.Held("light.example", now))
		assert.False(t, hub.Held("light.example", now.Add(time.Minute)))
	})
	t.Run("same owner replaces end time", func(t *testing.T) {
		hub := NewHub()
		hub.Hold("light.example", "plugin-a", now.Add(time.Minute))
		hub.Hold("light.example", "plugin-a", now.Add(2*time.Minute))
		assert.True(t, hub.Held("light.example", now.Add(90*time.Second)))
	})
	t.Run("release only named owner", func(t *testing.T) {
		hub := NewHub()
		hub.Hold("light.example", "plugin-a", now.Add(time.Minute))
		hub.Hold("light.example", "plugin-b", now.Add(time.Minute))
		hub.Release("light.example", "plugin-a")
		assert.Equal(t, map[string][]string{"light.example": {"plugin-b"}}, hub.HeldEntities(now))
		hub.Release("light.example", "plugin-b")
		assert.False(t, hub.Held("light.example", now))
	})
	t.Run("held entities prunes expired owners", func(t *testing.T) {
		hub := NewHub()
		hub.Hold("light.example", "expired", now)
		hub.Hold("light.example", "active", now.Add(time.Minute))
		assert.Equal(t, map[string][]string{"light.example": {"active"}}, hub.HeldEntities(now))
	})
}

func TestHubConcurrentUse(t *testing.T) {
	hub := NewHub()
	now := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			owner := fmt.Sprintf("plugin-%d", i)
			hub.Hold("light.example", owner, now.Add(time.Minute))
			_ = hub.Held("light.example", now)
			_ = hub.HeldEntities(now)
			hub.Release("light.example", owner)
		}(i)
	}
	wg.Wait()
}
