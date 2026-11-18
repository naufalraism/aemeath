// Package streamtest holds tests that send streams (Server-Sent Events, file
// downloads, WebSockets) through every middleware that wraps the
// ResponseWriter, alone and combined. A wrapper that forgets to pass on
// Flush, Hijack or ReadFrom breaks a stream without failing any unit test of
// its own package; this is where it shows.
package streamtest
