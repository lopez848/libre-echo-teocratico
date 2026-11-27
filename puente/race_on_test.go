//go:build race

package main

// raceEnabled skips the tests that run a brutella/hap accessory: its mDNS
// responder (brutella/dnssd) has a data race of its own, outside our code.
const raceEnabled = true
