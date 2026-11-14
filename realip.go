package aemeath

import (
	"fmt"
	"net"
	"net/netip"
	"strings"
)

// PrivateNetworks lists loopback and private address ranges. Platforms such as
// Railway, Fly.io, Render and a Kubernetes ingress reach your app from one of
// them, so RealIP(PrivateNetworks...) fits most deployments. Only use it when
// the app cannot be reached directly from the internet, or anyone could send
// X-Forwarded-For.
var PrivateNetworks = []string{
	"127.0.0.0/8", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "100.64.0.0/10",
	"::1/128", "fc00::/7",
}

// RealIP restores the client's address behind reverse proxies. When the
// connection comes from one of the trusted proxies (IP addresses or CIDR
// ranges), it reads X-Forwarded-For from right to left and uses the first
// address that is not a trusted proxy, falling back to X-Real-IP. The result
// replaces Request.RemoteAddr, so RateLimiter, SlogLogger and your handlers
// all see the real client.
//
// Requests from any other address are left untouched: a client cannot spoof
// its address by sending the header itself. Put RealIP before RateLimiter and
// the loggers.
//
//	app.Use(aemeath.RealIP(aemeath.PrivateNetworks...), aemeath.RateLimiter(100, time.Minute))
func RealIP(trusted ...string) Middleware {
	var nets []netip.Prefix
	for _, t := range trusted {
		t = strings.TrimSpace(t)
		p, err := netip.ParsePrefix(t)
		if err != nil {
			a, aerr := netip.ParseAddr(t)
			if aerr != nil {
				panic(fmt.Sprintf("aemeath: RealIP: %q is neither an IP address nor a CIDR range", t))
			}
			p = netip.PrefixFrom(a, a.BitLen())
		}
		nets = append(nets, p.Masked())
	}
	if len(nets) == 0 {
		panic("aemeath: RealIP needs at least one trusted proxy address or range")
	}
	isTrusted := func(a netip.Addr) bool {
		a = a.Unmap()
		for _, n := range nets {
			if n.Contains(a) {
				return true
			}
		}
		return false
	}

	return func(next HandlerFunc) HandlerFunc {
		return func(c *Ctx) error {
			host, port, err := net.SplitHostPort(c.R.RemoteAddr)
			if err != nil {
				host, port = c.R.RemoteAddr, ""
			}
			peer, err := netip.ParseAddr(host)
			if err != nil || !isTrusted(peer) {
				return next(c)
			}
			if client, ok := clientFromHeaders(c, isTrusted); ok {
				r := c.R.WithContext(c.R.Context()) // shallow copy: do not mutate the caller's request
				if port == "" {
					port = "0"
				}
				r.RemoteAddr = net.JoinHostPort(client.String(), port)
				c.R = r
			}
			return next(c)
		}
	}
}

// clientFromHeaders returns the right-most address in X-Forwarded-For that is
// not a trusted proxy (entries further left can be forged by the client), or
// the X-Real-IP header when there is no X-Forwarded-For.
func clientFromHeaders(c *Ctx, isTrusted func(netip.Addr) bool) (netip.Addr, bool) {
	var hops []string
	for _, h := range c.R.Header.Values("X-Forwarded-For") {
		hops = append(hops, strings.Split(h, ",")...)
	}
	if len(hops) == 0 {
		if a, err := netip.ParseAddr(strings.TrimSpace(c.R.Header.Get("X-Real-IP"))); err == nil {
			return a.Unmap(), true
		}
		return netip.Addr{}, false
	}
	var last netip.Addr
	for i := len(hops) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			// Garbage in the chain: stop and trust nothing further left.
			break
		}
		a = a.Unmap()
		last = a
		if !isTrusted(a) {
			return a, true
		}
	}
	// Every hop was a trusted proxy (or the chain was invalid): use the
	// left-most valid one we reached.
	return last, last.IsValid()
}
