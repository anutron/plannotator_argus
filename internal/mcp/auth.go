// Package mcp implements the plannotator-argus MCP callback HTTP server,
// the registrar that keeps tool registrations alive against argus, and the
// in-memory session store used by the verb handlers and the polling tool.
package mcp

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
)

// GenerateAuthHeader returns a fresh `Bearer <hex>` string for use as the
// per-process secret on MCP callback registrations. 32 random bytes hex
// encoded = 64 hex chars = effectively un-guessable.
func GenerateAuthHeader() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("rand: %w", err)
	}
	return "Bearer " + hex.EncodeToString(buf), nil
}

// EqualConstantTime returns true iff a and b are byte-identical, comparing in
// constant time relative to the longer of the two.
func EqualConstantTime(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
