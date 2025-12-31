// Home Automation Go Application
//
// This main.go serves as the plugin registration entry point.
// The actual application logic lives in cmd/app/run.go.
//
// Downstream forks can create their own main.go that imports the "all" package
// to get all public plugins, then add private overrides:
//
//	package main
//
//	import (
//		"homeautomation/cmd/app"
//
//		// Import all public plugins
//		_ "homeautomation/pkg/plugins/all"
//
//		// Import private overrides (higher priority wins)
//		_ "github.com/your-org/your-private-security-plugin"
//	)
//
//	func main() {
//		app.Run()
//	}
package main

import (
	"homeautomation/cmd/app"

	// Import all plugins via the convenience package
	_ "homeautomation/pkg/plugins/all"
)

func main() {
	app.Run()
}
