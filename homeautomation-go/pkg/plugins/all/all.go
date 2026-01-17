// Package all imports all public plugins for convenience.
// Downstream forks can import this package to get all plugins,
// then add their own overrides separately.
//
// Usage in downstream main.go:
//
//	import (
//	    "homeautomation/cmd/app"
//
//	    // Import all public plugins
//	    _ "homeautomation/pkg/plugins/all"
//
//	    // Import private overrides (these take priority)
//	    _ "github.com/your-org/your-private-security-plugin"
//	)
//
//	func main() {
//	    app.Run()
//	}
package all

import (
	_ "homeautomation/internal/plugins/christmas"
	_ "homeautomation/internal/plugins/dayphase"
	_ "homeautomation/internal/plugins/energy"
	_ "homeautomation/internal/plugins/environmental"
	_ "homeautomation/internal/plugins/lighting"
	_ "homeautomation/internal/plugins/loadshedding"
	_ "homeautomation/internal/plugins/music"
	_ "homeautomation/internal/plugins/security"
	_ "homeautomation/internal/plugins/sexmode"
	_ "homeautomation/internal/plugins/sleephygiene"
	_ "homeautomation/internal/plugins/statetracking"
	_ "homeautomation/internal/plugins/tv"
)
