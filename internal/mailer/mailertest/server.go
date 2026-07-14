// Package mailertest provides a minimal hand-rolled fake SMTP server for
// testing internal/mailer and its callers, plus a self-signed certificate
// helper for exercising STARTTLS/implicit-TLS paths. It implements just
// enough of the protocol to drive net/smtp's client sequence (EHLO/HELO,
// optional STARTTLS, optional AUTH PLAIN, MAIL FROM, RCPT TO, DATA, QUIT) —
// it is not a general-purpose or RFC-complete SMTP server.
package mailertest

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Options configures the fake server's behavior for a single test case.
type Options struct {
	OfferSTARTTLS bool             // advertise/accept STARTTLS (ignored if ImplicitTLS)
	ImplicitTLS   bool             // wrap the listener in TLS immediately (port-465 style)
	TLSCert       *tls.Certificate // required if OfferSTARTTLS or ImplicitTLS
	RequireAuth   bool             // demand AUTH PLAIN before MAIL FROM
	Username      string
	Password      string
	RejectRCPT    bool // return 550 on RCPT TO
	FailQUIT      bool // return an error response on QUIT (after successful DATA)
}

// Message is a captured, parsed email.
type Message struct {
	From, To, Subject, Body string
}

// Server is a fake SMTP listener driven entirely by Options.
type Server struct {
	Addr string

	opts Options
	ln   net.Listener
	done chan struct{}

	mu       sync.Mutex
	messages []Message

	authAttempts atomic.Int32
}

// Start begins listening on 127.0.0.1:0 and serving connections in the background.
func Start(opts Options) (*Server, error) {
	if (opts.OfferSTARTTLS || opts.ImplicitTLS) && opts.TLSCert == nil {
		return nil, fmt.Errorf("mailertest: OfferSTARTTLS/ImplicitTLS requires TLSCert")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s := &Server{opts: opts, ln: ln, done: make(chan struct{})}
	if opts.ImplicitTLS {
		s.ln = tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{*opts.TLSCert}})
	}
	s.Addr = ln.Addr().String()
	go s.acceptLoop()
	return s, nil
}

// HostPort splits Addr into (host, port) for building a mailer.Config.
func (s *Server) HostPort() (string, int) {
	host, portStr, err := net.SplitHostPort(s.Addr)
	if err != nil {
		return s.Addr, 0
	}
	var port int
	fmt.Sscanf(portStr, "%d", &port)
	return host, port
}

// Close stops accepting connections and waits for the accept loop to exit.
func (s *Server) Close() {
	s.ln.Close()
	<-s.done
}

// Messages returns a snapshot of all successfully captured deliveries.
func (s *Server) Messages() []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Message, len(s.messages))
	copy(out, s.messages)
	return out
}

// AuthAttempts returns how many AUTH PLAIN attempts were seen.
func (s *Server) AuthAttempts() int32 {
	return s.authAttempts.Load()
}

func (s *Server) acceptLoop() {
	defer close(s.done)
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(conn)
	}
}

func (s *Server) handle(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))

	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	writeLine := func(line string) {
		writer.WriteString(line + "\r\n")
		writer.Flush()
	}
	writeLine("220 mailertest ESMTP")

	var (
		authOK   bool
		fromAddr string
		toAddr   string
	)

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		upper := strings.ToUpper(line)

		switch {
		case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
			writeLine("250-mailertest greets you")
			if s.opts.OfferSTARTTLS && !s.opts.ImplicitTLS {
				writeLine("250-STARTTLS")
			}
			if s.opts.RequireAuth {
				writeLine("250-AUTH PLAIN")
			}
			writeLine("250 OK")

		case upper == "STARTTLS":
			if !s.opts.OfferSTARTTLS || s.opts.ImplicitTLS {
				writeLine("502 not supported")
				continue
			}
			writeLine("220 go ahead")
			tlsConn := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{*s.opts.TLSCert}})
			if err := tlsConn.Handshake(); err != nil {
				return
			}
			conn = tlsConn
			reader = bufio.NewReader(conn)
			writer = bufio.NewWriter(conn)

		case strings.HasPrefix(upper, "AUTH PLAIN"):
			s.authAttempts.Add(1)
			parts := strings.SplitN(line, " ", 3)
			var b64 string
			if len(parts) == 3 {
				b64 = parts[2]
			} else {
				writeLine("334 ")
				cont, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				b64 = strings.TrimRight(cont, "\r\n")
			}
			decoded, err := base64.StdEncoding.DecodeString(b64)
			if err != nil {
				writeLine("501 bad base64")
				continue
			}
			segs := strings.Split(string(decoded), "\x00")
			if len(segs) == 3 && segs[1] == s.opts.Username && segs[2] == s.opts.Password {
				authOK = true
				writeLine("235 authenticated")
			} else {
				writeLine("535 authentication failed")
			}

		case strings.HasPrefix(upper, "MAIL FROM:"):
			if s.opts.RequireAuth && !authOK {
				writeLine("530 authentication required")
				continue
			}
			fromAddr = extractAddr(line)
			writeLine("250 OK")

		case strings.HasPrefix(upper, "RCPT TO:"):
			if s.opts.RejectRCPT {
				writeLine("550 no such user")
				continue
			}
			toAddr = extractAddr(line)
			writeLine("250 OK")

		case upper == "DATA":
			writeLine("354 go ahead")
			var raw strings.Builder
			for {
				dl, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				if dl == ".\r\n" || dl == ".\n" {
					break
				}
				raw.WriteString(dl)
			}
			subject, body := splitHeaders(raw.String())
			s.mu.Lock()
			s.messages = append(s.messages, Message{From: fromAddr, To: toAddr, Subject: subject, Body: body})
			s.mu.Unlock()
			writeLine("250 OK: queued")

		case upper == "QUIT":
			if s.opts.FailQUIT {
				writeLine("421 closing connection")
				return
			}
			writeLine("221 bye")
			return

		default:
			writeLine("500 unrecognized command")
		}
	}
}

func extractAddr(line string) string {
	i := strings.Index(line, "<")
	j := strings.LastIndex(line, ">")
	if i >= 0 && j > i {
		return line[i+1 : j]
	}
	if idx := strings.Index(line, ":"); idx >= 0 {
		return strings.TrimSpace(line[idx+1:])
	}
	return line
}

// splitHeaders separates the Subject header and body from a raw DATA payload
// (built by internal/mailer's buildMessage: headers, blank line, body).
func splitHeaders(raw string) (subject, body string) {
	lines := strings.Split(raw, "\r\n")
	i := 0
	for ; i < len(lines); i++ {
		if lines[i] == "" {
			i++
			break
		}
		if strings.HasPrefix(lines[i], "Subject: ") {
			subject = strings.TrimPrefix(lines[i], "Subject: ")
		}
	}
	body = strings.Join(lines[i:], "\r\n")
	body = strings.TrimSuffix(body, "\r\n")
	return subject, body
}

// GenerateSelfSignedCert creates an in-memory self-signed certificate valid
// for host (IP or DNS name), for use with OfferSTARTTLS/ImplicitTLS in tests.
func GenerateSelfSignedCert(host string) (tls.Certificate, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: host},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	if ip := net.ParseIP(host); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
	} else {
		tmpl.DNSNames = []string{host}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		return tls.Certificate{}, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyBytes, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})
	return tls.X509KeyPair(certPEM, keyPEM)
}

// CertPool returns an *x509.CertPool trusting cert's leaf, for use as a
// test-only Sender root CA override.
func CertPool(cert tls.Certificate) (*x509.CertPool, error) {
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return pool, nil
}
