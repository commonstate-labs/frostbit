//go:build !linux

package main

func managedNodeUpdateRestart() bool { return false }
