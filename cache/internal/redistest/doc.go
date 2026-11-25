// Package redistest holds tests that need more modules than the package they
// test (a Redis server in memory, every database driver). Under internal/
// they do not reach the go.sum of projects that import the package.
package redistest
