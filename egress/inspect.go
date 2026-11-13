package egress

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
)

// Some hosts are shared: anyone can write to storage.googleapis.com, so
// allowing the name would let a box send data to a bucket of its own. The
// allowlist can name paths on such a host instead, like
// storage.googleapis.com/chrome-for-testing-public/. The proxy then ends the
// box's TLS itself, with a certificate from the CA, reads each request, and
// sends on only GETs and HEADs under those paths, over its own TLS to the
// host. Every other host stays a plain tunnel the proxy can't read.

// upstream sends inspected requests on. It doesn't decompress, so responses
// go back as the host sent them, and it never follows redirects: the box
// does, and asks the proxy again.
var upstream = &http.Transport{
	DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		return dial(host, port)
	},
	ForceAttemptHTTP2:  true,
	DisableCompression: true,
}

// inspect serves a box's CONNECT to host, which the allowlist limits to
// paths: requests on the connection go on only if pathRefusal allows them.
func (p Policy) inspect(c net.Conn, host string, paths []string) {
	cert, err := p.CA.leaf(host)
	if err != nil {
		log.Printf("egress: %s: certificate: %v", host, err)
		fmt.Fprint(c, "HTTP/1.1 502 Bad Gateway\r\nConnection: close\r\n\r\n")
		return
	}
	fmt.Fprint(c, "HTTP/1.1 200 Connection Established\r\n\r\n")
	tc := tls.Server(c, &tls.Config{
		Certificates: []tls.Certificate{*cert},
		NextProtos:   []string{"http/1.1"},
	})
	defer tc.Close()
	br := bufio.NewReader(tc)
	for {
		req, err := http.ReadRequest(br)
		if err != nil {
			return
		}
		target := host + req.URL.EscapedPath()
		if why := pathRefusal(req, paths); why != "" {
			log.Printf("egress: REFUSED %s %s: %s", req.Method, target, why)
			msg := fmt.Sprintf("bunk: refused %s %s (%s)\n", req.Method, target, why)
			fmt.Fprintf(tc, "HTTP/1.1 403 Forbidden\r\nContent-Type: text/plain\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(msg), msg)
			return
		}
		out := &http.Request{
			Method: req.Method,
			URL:    &url.URL{Scheme: "https", Host: host, Path: req.URL.Path, RawQuery: req.URL.RawQuery},
			Header: req.Header.Clone(),
			Host:   host, // whatever the box's Host header said
		}
		out.Header.Del("Connection")
		resp, err := upstream.RoundTrip(out.WithContext(context.Background()))
		if err != nil {
			log.Printf("egress: %s: %v", target, err)
			fmt.Fprint(tc, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
			return
		}
		log.Printf("egress: allowed %s %s: %d", req.Method, target, resp.StatusCode)
		resp.Proto, resp.ProtoMajor, resp.ProtoMinor = "HTTP/1.1", 1, 1
		resp.Close = req.Close
		if resp.ContentLength < 0 && req.Method != http.MethodHead {
			// HTTP/2 may give no length; chunked keeps the connection open.
			resp.TransferEncoding = []string{"chunked"}
		}
		err = resp.Write(tc)
		resp.Body.Close()
		if err != nil || req.Close {
			return
		}
	}
}

// pathRefusal says why req may not go on, or "" if it may: a GET or HEAD,
// with no body, for a plain path under one of paths.
func pathRefusal(req *http.Request, paths []string) string {
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		return "only GET and HEAD"
	}
	if req.ContentLength != 0 || len(req.TransferEncoding) > 0 {
		io.Copy(io.Discard, req.Body)
		return "no request body"
	}
	// Plain: no escapes or dot segments a server might read differently
	// from the prefix check.
	p := req.URL.EscapedPath()
	if strings.Contains(p, "%") || path.Clean(p) != p && path.Clean(p)+"/" != p {
		return "path isn't plain"
	}
	for _, prefix := range paths {
		if strings.HasPrefix(p, prefix) {
			return ""
		}
	}
	return "path not allowlisted"
}
