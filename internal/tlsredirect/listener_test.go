package tlsredirect

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
)

func TestServePlainHTTPRedirect(t *testing.T) {
	t.Parallel()

	client, server := net.Pipe()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		br := bufio.NewReader(server)
		servePlainHTTPRedirect(nil, br, server)
	}()

	_, err := fmt.Fprintf(client, "GET /path?q=1 HTTP/1.1\r\nHost: example.com:8080\r\n\r\n")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(client)
	if err != nil {
		t.Fatal(err)
	}
	_ = client.Close()
	wg.Wait()

	s := string(body)
	if !strings.Contains(s, "308") {
		t.Fatalf("expected 308 in response, got %q", s)
	}
	if !strings.Contains(s, "https://example.com:8080/path?q=1") {
		t.Fatalf("expected Location target in response, got %q", s)
	}
}
