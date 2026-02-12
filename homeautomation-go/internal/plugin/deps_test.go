package plugin

import (
	"testing"
	"time"

	pkgplugin "homeautomation/pkg/plugin"

	"github.com/stretchr/testify/assert"
)

func TestOptionalDeps_TimeProviderOrDefault(t *testing.T) {
	t.Run("returns RealTimeProvider when nil", func(t *testing.T) {
		opts := OptionalDeps{}
		tp := opts.TimeProviderOrDefault()
		assert.IsType(t, pkgplugin.RealTimeProvider{}, tp)
	})

	t.Run("returns provided TimeProvider when set", func(t *testing.T) {
		fixed := pkgplugin.FixedTimeProvider{FixedTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
		opts := OptionalDeps{TimeProvider: fixed}
		tp := opts.TimeProviderOrDefault()
		assert.Equal(t, fixed, tp)
	})
}

func TestOptionalDeps_TimezoneOrDefault(t *testing.T) {
	t.Run("returns time.Local when nil", func(t *testing.T) {
		opts := OptionalDeps{}
		tz := opts.TimezoneOrDefault()
		assert.Equal(t, time.Local, tz)
	})

	t.Run("returns provided timezone when set", func(t *testing.T) {
		chicago, _ := time.LoadLocation("America/Chicago")
		opts := OptionalDeps{Timezone: chicago}
		tz := opts.TimezoneOrDefault()
		assert.Equal(t, chicago, tz)
	})
}
