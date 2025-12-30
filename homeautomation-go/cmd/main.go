// Home Automation Go Application
//
// This main.go serves as the plugin registration entry point.
// The actual application logic lives in cmd/app/run.go.
//
// Downstream forks can create their own main.go that imports additional
// plugins via blank imports while reusing the core app.Run() function:
//
//	package main
//
//	import (
//		"homeautomation/cmd/app"
//		_ "github.com/example/my-private-plugin"  // Additional plugins
//	)
//
//	func main() {
//		app.Run()
//	}
package main

import (
	"homeautomation/cmd/app"

	// Import plugin packages to trigger init() registrations
	_ "homeautomation/internal/plugins/dayphase"
	_ "homeautomation/internal/plugins/energy"
	_ "homeautomation/internal/plugins/lighting"
	_ "homeautomation/internal/plugins/loadshedding"
	_ "homeautomation/internal/plugins/music"
	_ "homeautomation/internal/plugins/security"
	_ "homeautomation/internal/plugins/sexmode"
	_ "homeautomation/internal/plugins/sleephygiene"
	_ "homeautomation/internal/plugins/statetracking"
	_ "homeautomation/internal/plugins/tv"
)

func main() {
	app.Run()
}
