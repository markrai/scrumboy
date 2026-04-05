// Package tlsredirect provides a TCP listener that accepts TLS (HTTPS) connections
// and responds to accidental plain-HTTP requests on the same port with a redirect to HTTPS.
package tlsredirect

import (
	"bufio"
	"crypto/tls"
	"io"
	"log"
	"net"
	"net/http"
	"time"
)

// Listener wraps a TCP listener. Accept returns *tls.Conn for TLS handshakes; if the
// client sends an HTTP request instead, it handles the connection in a goroutine and
// keeps accepting (never returns that connection from Accept).
type Listener struct {
	Inner     net.Listener
	TLSConfig *tls.Config
	Log       *log.Logger
}

func (l *Listener) Accept() (net.Conn, error) {
	for {
		c, err := l.Inner.Accept()
		if err != nil {
			return nil, err
		}
		if err := c.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
			c.Close()
			continue
		}
		br := bufio.NewReader(c)
		peek, err := br.Peek(1)
		if err != nil {
			if l.Log != nil {
				l.Log.Printf("tlsredirect: peek: %v", err)
			}
			c.Close()
			continue
		}
		if err := c.SetReadDeadline(time.Time{}); err != nil {
			c.Close()
			continue
		}
		// TLS record type: handshake (0x16), application data (0x17), change cipher (0x14).
		// 0x80 is SSLv2-style ClientHello (rare).
		if peek[0] == 0x16 || peek[0] == 0x17 || peek[0] == 0x14 || peek[0] == 0x80 {
			return tls.Server(&bufferedConn{Reader: br, Conn: c}, l.TLSConfig), nil
		}
		go servePlainHTTPRedirect(l.Log, br, c)
	}
}

func (l *Listener) Close() error   { return l.Inner.Close() }
func (l *Listener) Addr() net.Addr { return l.Inner.Addr() }

type bufferedConn struct {
	*bufio.Reader
	net.Conn
}

func (b *bufferedConn) Read(p []byte) (int, error) {
	return b.Reader.Read(p)
}

func servePlainHTTPRedirect(logger *log.Logger, br *bufio.Reader, c net.Conn) {
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}
	defer req.Body.Close()
	_, _ = io.Copy(io.Discard, req.Body)

	host := req.Host
	if host == "" {
		return
	}
	loc := "https://" + host + req.URL.RequestURI()
	const msg = "HTTP/1.1 308 Permanent Redirect\r\n" +
		"Connection: close\r\n" +
		"Content-Length: 0\r\n" +
		"Location: "
	if _, err := io.WriteString(c, msg+loc+"\r\n\r\n"); err != nil && logger != nil {
		logger.Printf("tlsredirect: write redirect: %v", err)
	}
}
