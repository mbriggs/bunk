// Package egress is a box's only way out to the internet: an explicit HTTP
// proxy (CONNECT for HTTPS, plain requests for apt) that checks each hostname
// against an allowlist and dials that name itself from the host. It never
// terminates TLS.
//
// The allowlist has one entry per line: a name, ".suffix" for a domain and its
// subdomains, or "~regexp" matched against the whole hostname. "#" starts a
// comment. The allowlist and mode files are reread on every connection, so an
// edit applies to the next request.
package egress

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"regexp"
	"strings"
	"time"
)

// Policy decides which names a box may reach.
type Policy struct {
	AllowFile string // the allowlist
	ModeFile  string // "open" allows any name but GitHub's; anything else means allowlist only
	CA        *CA    // for the hosts the allowlist limits to paths; nil refuses them
}

// GitHub goes through hallpass only. These names are refused in every mode
// and can't be allowlisted. Other GitHub names need an explicit entry even in
// open mode.
var hardRefused = map[string]bool{
	"github.com": true, "api.github.com": true, "uploads.github.com": true,
	"gist.github.com": true, "ssh.github.com": true,
}

// Refusal returns "" if name may be reached, or why not.
func (p Policy) Refusal(name string) string {
	name = strings.ToLower(strings.TrimSuffix(name, "."))
	switch {
	case name == "":
		return "no name"
	case hardRefused[name]:
		return "GitHub goes through hallpass only"
	case p.listed(name):
		return ""
	case p.open() && !strings.HasSuffix(name, ".github.com") && !strings.HasSuffix(name, ".githubusercontent.com"):
		return ""
	}
	return "not allowlisted"
}

func (p Policy) listed(name string) bool {
	raw, _ := os.ReadFile(p.AllowFile)
	for _, line := range strings.Split(string(raw), "\n") {
		e, _, _ := strings.Cut(line, "#")
		e = strings.ToLower(strings.TrimSpace(e))
		switch {
		case e == "", strings.Contains(e, "/"): // a path entry; see Paths
		case strings.HasPrefix(e, "~"):
			if hostPattern(e[1:]).MatchString(name) {
				return true
			}
		case e == name, strings.HasPrefix(e, ".") && (strings.HasSuffix(name, e) || name == e[1:]):
			return true
		}
	}
	return false
}

// hostPattern compiles a "~REGEXP" entry anchored at both ends, so
// `~[a-z0-9]+\.ewr\.prisma\.build` can't match x.ewr.prisma.build.example.com.
// An entry that doesn't compile matches nothing and is logged.
// Paths returns the paths the allowlist allows on name, from entries like
// storage.googleapis.com/chrome-for-testing-public/, when the name isn't
// allowed outright. Each is a prefix, from the first slash. GitHub gets
// none, as ever. A path is a directory: an entry ending without a slash
// gets one.
func (p Policy) Paths(name string) []string {
	name = strings.ToLower(strings.TrimSuffix(name, "."))
	if hardRefused[name] || strings.HasSuffix(name, ".github.com") || strings.HasSuffix(name, ".githubusercontent.com") {
		return nil
	}
	raw, _ := os.ReadFile(p.AllowFile)
	var paths []string
	for _, line := range strings.Split(string(raw), "\n") {
		e, _, _ := strings.Cut(line, "#")
		host, path, ok := strings.Cut(strings.TrimSpace(e), "/")
		if ok && strings.ToLower(host) == name {
			// A directory, so .../public/ can't match .../public-evil/.
			paths = append(paths, "/"+strings.TrimSuffix(path, "/")+"/")
		}
	}
	return paths
}

func hostPattern(expr string) *regexp.Regexp {
	re, err := regexp.Compile(`^(?:` + expr + `)$`)
	if err != nil {
		log.Printf("egress: allowlist: bad pattern ~%s: %v", expr, err)
		return regexp.MustCompile(`$^`)
	}
	return re
}

func (p Policy) open() bool {
	raw, _ := os.ReadFile(p.ModeFile)
	return strings.TrimSpace(string(raw)) == "open"
}

// Serve runs the proxy on ln until it fails.
func Serve(ln net.Listener, p Policy) error {
	for {
		c, err := ln.Accept()
		if err != nil {
			return err
		}
		go p.handle(c)
	}
}

// handle serves one connection. The first request decides where the whole
// connection goes.
func (p Policy) handle(c net.Conn) {
	defer c.Close()
	br := bufio.NewReader(c)
	c.SetReadDeadline(time.Now().Add(10 * time.Second))
	req, err := http.ReadRequest(br)
	c.SetReadDeadline(time.Time{})
	if err != nil {
		return
	}
	host, port := req.Host, "80"
	if req.Method == http.MethodConnect {
		var err error
		if host, port, err = net.SplitHostPort(req.Host); err != nil || port != "443" {
			refuse(c, req.Host, "only CONNECT to port 443")
			return
		}
	} else if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if why := p.Refusal(host); why != "" {
		if paths := p.Paths(host); len(paths) > 0 && p.CA != nil && req.Method == http.MethodConnect {
			p.inspect(bufConn{c, br}, host, paths)
			return
		}
		refuse(c, host, why)
		return
	}
	up, err := dial(host, port)
	if err != nil {
		log.Printf("egress: %s: %v", host, err)
		fmt.Fprint(c, "HTTP/1.1 502 Bad Gateway\r\nConnection: close\r\n\r\n")
		return
	}
	defer up.Close()
	log.Printf("egress: allowed %s:%s", host, port)
	if req.Method == http.MethodConnect {
		fmt.Fprint(c, "HTTP/1.1 200 Connection Established\r\n\r\n")
	} else {
		req.Write(up)
	}
	splice(bufConn{c, br}, up)
}

func refuse(c net.Conn, host, why string) {
	log.Printf("egress: REFUSED %s: %s", host, why)
	msg := fmt.Sprintf("bunk: refused %s (%s)\n", host, why)
	fmt.Fprintf(c, "HTTP/1.1 403 Forbidden\r\nContent-Type: text/plain\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(msg), msg)
}

// dial resolves name itself and refuses any address that isn't public, so an
// allowlisted name can't be pointed at the host's own network.
func dial(name, port string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip4", name)
	if err != nil {
		return nil, err
	}
	for _, a := range addrs {
		if !public(a) {
			return nil, fmt.Errorf("resolves to non-public %s", a)
		}
	}
	return (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(addrs[0].String(), port))
}

var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// A literal address comes back from the resolver IPv4-mapped
// (::ffff:100.64.0.1), which no IPv4 prefix contains, so it's unmapped first.
func public(a netip.Addr) bool {
	a = a.Unmap()
	return a.IsGlobalUnicast() && !a.IsPrivate() && !cgnat.Contains(a)
}

// bufConn reads through the bufio.Reader that already holds bytes the client
// sent after its request.
type bufConn struct {
	net.Conn
	r *bufio.Reader
}

func (b bufConn) Read(p []byte) (int, error) { return b.r.Read(p) }

func splice(a, b net.Conn) {
	done := make(chan struct{})
	go func() { io.Copy(b, a); closeWrite(b); close(done) }()
	io.Copy(a, b)
	closeWrite(a)
	<-done
}

func closeWrite(c net.Conn) {
	if bc, ok := c.(bufConn); ok {
		c = bc.Conn
	}
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		cw.CloseWrite()
	}
}
