// Package integration provides integration tests for the home automation system.
// This file re-exports types from pkg/testutil for backward compatibility.
package integration

import (
	"homeautomation/pkg/testutil"
)

// Type aliases for backward compatibility with existing tests
type MockHAServer = testutil.MockHAServer
type EntityState = testutil.EntityState
type ServiceCall = testutil.ServiceCall
type StateContext = testutil.StateContext

// NewMockHAServer creates a new mock HA server
var NewMockHAServer = testutil.NewMockHAServer

// Helper function aliases
var FilterServiceCalls = testutil.FilterServiceCalls
var FindServiceCallWithData = testutil.FindServiceCallWithData
var FindServiceCallWithEntityID = testutil.FindServiceCallWithEntityID
