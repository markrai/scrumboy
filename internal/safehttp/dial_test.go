package safehttp

import (
	"context"
	"errors"
	"net"
	"testing"
)

func TestDialContextDialsVettedIPNotHostname(t *testing.T) {
	t.Parallel()

	var dialed []string
	d := Dialer{
		LookupIP: func(context.Context, string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("8.8.8.8")}, nil
		},
		Dial: func(_ context.Context, network, address string) (net.Conn, error) {
			dialed = append(dialed, network+" "+address)
			c1, c2 := net.Pipe()
			_ = c2.Close()
			return c1, nil
		},
	}

	conn, err := d.DialContext(context.Background(), "tcp", "hooks.example:443")
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	_ = conn.Close()
	if len(dialed) != 1 || dialed[0] != "tcp 8.8.8.8:443" {
		t.Fatalf("dialed %v, want tcp 8.8.8.8:443 (not the hostname)", dialed)
	}
}

func TestDialContextSkipsForbiddenAndDialsAllowed(t *testing.T) {
	t.Parallel()

	var dialed []string
	d := Dialer{
		LookupIP: func(context.Context, string) ([]net.IP, error) {
			return []net.IP{
				net.ParseIP("10.1.2.3"),
				net.ParseIP("8.8.8.8"),
				net.ParseIP("192.168.0.9"),
			}, nil
		},
		Dial: func(_ context.Context, _, address string) (net.Conn, error) {
			dialed = append(dialed, address)
			c1, c2 := net.Pipe()
			_ = c2.Close()
			return c1, nil
		},
	}

	conn, err := d.DialContext(context.Background(), "tcp", "mixed.example:443")
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	_ = conn.Close()
	if len(dialed) != 1 || dialed[0] != "8.8.8.8:443" {
		t.Fatalf("dialed %v, want only 8.8.8.8:443", dialed)
	}
}

func TestDialContextAllForbidden(t *testing.T) {
	t.Parallel()

	dialed := 0
	d := Dialer{
		LookupIP: func(context.Context, string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("10.1.2.3"), net.ParseIP("169.254.169.254")}, nil
		},
		Dial: func(context.Context, string, string) (net.Conn, error) {
			dialed++
			return nil, errors.New("should not dial")
		},
	}
	_, err := d.DialContext(context.Background(), "tcp", "evil.example:80")
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("err=%v, want ErrForbidden", err)
	}
	if dialed != 0 {
		t.Fatalf("dialed %d times", dialed)
	}
}

func TestDialContextLiteralLoopback(t *testing.T) {
	t.Parallel()

	dialed := 0
	d := Dialer{
		Dial: func(context.Context, string, string) (net.Conn, error) {
			dialed++
			return nil, errors.New("should not dial")
		},
	}
	_, err := d.DialContext(context.Background(), "tcp", "127.0.0.1:80")
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("err=%v, want ErrForbidden", err)
	}
	if dialed != 0 {
		t.Fatalf("dialed %d times", dialed)
	}
}

func TestDialContextResolvesOnce(t *testing.T) {
	t.Parallel()

	lookups := 0
	d := Dialer{
		LookupIP: func(context.Context, string) ([]net.IP, error) {
			lookups++
			if lookups == 1 {
				return []net.IP{net.ParseIP("8.8.8.8")}, nil
			}
			return []net.IP{net.ParseIP("10.0.0.1")}, nil
		},
		Dial: func(_ context.Context, _, address string) (net.Conn, error) {
			if address != "8.8.8.8:443" {
				t.Errorf("dialed %s", address)
			}
			c1, c2 := net.Pipe()
			_ = c2.Close()
			return c1, nil
		},
	}
	conn, err := d.DialContext(context.Background(), "tcp", "rebind.example:443")
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	_ = conn.Close()
	if lookups != 1 {
		t.Fatalf("lookups=%d, want 1", lookups)
	}
}

func TestDialContextPublicThenPrivateDialFailureIsNotForbidden(t *testing.T) {
	t.Parallel()

	transient := errors.New("public dial failed")
	var dialed []string
	d := Dialer{
		LookupIP: func(context.Context, string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("8.8.8.8"), net.ParseIP("10.0.0.1")}, nil
		},
		Dial: func(_ context.Context, _, address string) (net.Conn, error) {
			dialed = append(dialed, address)
			return nil, transient
		},
	}
	_, err := d.DialContext(context.Background(), "tcp", "mixed.example:443")
	if !errors.Is(err, transient) {
		t.Fatalf("err=%v, want transient dial error", err)
	}
	if errors.Is(err, ErrForbidden) {
		t.Fatal("ErrForbidden must not overwrite a permitted-IP dial failure")
	}
	if len(dialed) != 1 || dialed[0] != "8.8.8.8:443" {
		t.Fatalf("dialed %v, want only 8.8.8.8:443", dialed)
	}
}

func TestDialContextPrivateThenPublicDialFailureIsNotForbidden(t *testing.T) {
	t.Parallel()

	transient := errors.New("public dial failed")
	var dialed []string
	d := Dialer{
		LookupIP: func(context.Context, string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("10.0.0.1"), net.ParseIP("8.8.8.8")}, nil
		},
		Dial: func(_ context.Context, _, address string) (net.Conn, error) {
			dialed = append(dialed, address)
			return nil, transient
		},
	}
	_, err := d.DialContext(context.Background(), "tcp", "mixed.example:443")
	if !errors.Is(err, transient) {
		t.Fatalf("err=%v, want transient dial error", err)
	}
	if errors.Is(err, ErrForbidden) {
		t.Fatal("ErrForbidden must not overwrite a permitted-IP dial failure")
	}
	if len(dialed) != 1 || dialed[0] != "8.8.8.8:443" {
		t.Fatalf("dialed %v, want only 8.8.8.8:443 (private skipped)", dialed)
	}
}

func TestNewDialContextUsesDialer(t *testing.T) {
	t.Parallel()

	fn := NewDialContext(Dialer{
		LookupIP: func(context.Context, string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("127.0.0.1")}, nil
		},
	})
	_, err := fn(context.Background(), "tcp", "localhost:80")
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("err=%v, want ErrForbidden", err)
	}
}
