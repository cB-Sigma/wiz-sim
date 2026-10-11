// Test-only module: pywizlight's client against github.com/cB-Sigma/wiz-sim's
// simulated devices, in Docker (see interop_test.go). Not part of the
// published module.
module github.com/cB-Sigma/wiz-sim/interop

go 1.22

replace github.com/cB-Sigma/wiz-sim => ../

require (
	github.com/cB-Sigma/wiz v0.3.0
	github.com/cB-Sigma/wiz-sim v0.0.0
)

require github.com/cB-Sigma/lanbcast v0.2.0 // indirect
