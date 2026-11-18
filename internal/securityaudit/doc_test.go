// Package securityaudit holds black-box security tests against the public API.
//
// Each test states the SECURE behaviour. A failing test is a proven weakness
// (its message starts with "WEAKNESS"); a passing test is a defence that was
// verified. Run: go test -v ./internal/securityaudit/
package securityaudit
