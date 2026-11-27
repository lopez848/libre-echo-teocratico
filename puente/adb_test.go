package main

import (
	"encoding/base64"
	"bytes"
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"net"
	"strings"
	"testing"
	"time"
)

// fakeAdbd is a minimal adbd speaking the ADB wire protocol on a local
// listener: CNXN → (optional AUTH) → OPEN → OKAY → WRTE → CLSE.
type fakeAdbd struct {
	ln        net.Listener
	wantAuth  bool // reply AUTH(token) instead of a plain CNXN
	wantPair  bool // the first signature is unknown: ask for the key line
	token     []byte
	signedTok []byte // AUTH SIGNATURE data received
	gotPub    []byte // the public key line received, when wantPair was set
	served    []string
	shellOut  func(cmd string, out func(string))
}

func newFakeAdbd(t *testing.T, wantAuth bool) *fakeAdbd {
	t.Helper()
	f := &fakeAdbd{wantAuth: wantAuth}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f.ln = ln
	go f.serve()
	t.Cleanup(func() { _ = ln.Close() })
	return f
}

func (f *fakeAdbd) addr() string {
	_, port, err := net.SplitHostPort(f.ln.Addr().String())
	if err != nil {
		panic(err)
	}
	return "127.0.0.1:" + port
}

func (f *fakeAdbd) serve() {
	for {
		c, err := f.ln.Accept()
		if err != nil {
			return
		}
		go func() { defer c.Close(); f.handle(c) }()
	}
}

func (f *fakeAdbd) handle(c net.Conn) {
	for {
		head := make([]byte, 24)
		deadline := time.Now().Add(5 * time.Second)
		if err := readFull(c, head, deadline); err != nil {
			return
		}
		m, err := unpackMsg(head)
		if err != nil || m.length > 4096 {
			return
		}
		if m.length > 0 {
			body := make([]byte, m.length)
			if err := readFull(c, body, deadline); err != nil {
				return
			}
			m.data = body
		}
		switch string(m.command) {
		case cmdCNXN:
			if f.wantAuth && f.signedTok == nil {
				f.token = bytes.Repeat([]byte("deadbeef"), 4)
				sendMsg(c, mkMsg(cmdAUTH, adbAuthToken, 0, f.token))
				continue
			}
			sendMsg(c, mkMsg(cmdCNXN, adbVersion, adbMaxData, []byte("device::ro.build.version.release=12\x00")))
		case cmdAUTH:
			switch m.arg0 {
			case adbAuthSign:
				f.signedTok = append([]byte(nil), m.data...)
				if f.wantPair { // unknown key: ask for the public key line
					f.wantPair = false
					f.token = bytes.Repeat([]byte("dEaDbEeF"), 4)
					sendMsg(c, mkMsg(cmdAUTH, adbAuthToken, 0, f.token))
					continue
				}
				sendMsg(c, mkMsg(cmdCNXN, adbVersion, adbMaxData, []byte("device::ok")))
			case adbAuthPub:
				f.gotPub = append([]byte(nil), m.data...)
				sendMsg(c, mkMsg(cmdCNXN, adbVersion, adbMaxData, []byte("device::ok")))
			case adbAuthToken:
				sendMsg(c, mkMsg(cmdAUTH, adbAuthToken, 0, f.token))
			default:
				return
			}
		case cmdOPEN:
			if !strings.HasPrefix(string(m.data), "shell:") {
				return
			}
			emitted := false
			f.served = append(f.served, string(m.data)[len("shell:"):])
			if f.shellOut != nil {
				f.shellOut(string(m.data)[len("shell:"):], func(out string) {
					emitted = true
					sendMsg(c, mkMsg(cmdOKAY, 4242, m.arg0, nil))
					sendMsg(c, mkMsg(cmdWRTE, 4242, m.arg0, []byte(out)))
					sendMsg(c, mkMsg(cmdCLSE, 4242, m.arg0, nil))
				})
			}
			if emitted {
				continue
			}
			sendMsg(c, mkMsg(cmdOKAY, 4242, m.arg0, nil))
			sendMsg(c, mkMsg(cmdWRTE, 4242, m.arg0, []byte("hi\n")))
			sendMsg(c, mkMsg(cmdCLSE, 4242, m.arg0, nil))
		}
	}
}

// TestADBHandshakeAndShell walks CNXN → OPEN → shell output → CLSE.
func TestADBHandshakeAndShell(t *testing.T) {
	f := newFakeAdbd(t, false)
	key, err := loadADBKey(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	c, err := connectADB(ctx, f.addr(), key)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer c.Close()
	out, err := c.shell(ctx, "echo hi")
	if err != nil {
		t.Fatalf("shell: %v", err)
	}
	if out != "hi\n" {
		t.Fatalf("shell output = %q, want %q", out, "hi\n")
	}
	if len(f.served) != 1 || f.served[0] != "echo hi" {
		t.Fatalf("served = %v", f.served)
	}
}

// TestADBAuthSignature proves the client signs the exact token adbd sent
// with RSA-SHA256, so adbd can verify against the key it already stores.
func TestADBAuthSignature(t *testing.T) {
	f := newFakeAdbd(t, true)
	key, err := loadADBKey(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	c, err := connectADB(ctx, f.addr(), key)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer c.Close()

	if len(f.signedTok) == 0 {
		t.Fatal("client sent no AUTH signature")
	}
	h := sha256.Sum256(f.token)
	if err := rsa.VerifyPKCS1v15(&key.Private.PublicKey, crypto.SHA256, h[:], f.signedTok); err != nil {
		t.Fatal("AUTH signature does not verify against the sending key:", err)
	}
}

// TestADBUnknownKeySendsPubline walks the path of a TV that does not know
// the bridge key: after the first signature it asks for the public key and
// accepts once it receives the Android RSAPublicKey coding.
func TestADBUnknownKeySendsPubline(t *testing.T) {
	f := newFakeAdbd(t, true)
	f.wantPair = true
	key, err := loadADBKey(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	c, err := connectADB(ctx, f.addr(), key)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer c.Close()
	if len(f.gotPub) == 0 {
		t.Fatal("no public key line was sent")
	}
	line := string(bytes.TrimRight(f.gotPub, "\x00"))
	sp := strings.LastIndexByte(line, ' ')
	if sp < 0 {
		t.Fatal("public key line has no user part")
	}
	body, err := base64.StdEncoding.DecodeString(line[:sp])
	if err != nil {
		t.Fatalf("bad base64 pub key line: %v", err)
	}
	if len(body) != 8+2*modulusBytes+4 {
		t.Fatalf("Android RSAPublicKey is %d bytes, want %d", len(body), 8+2*modulusBytes+4)
	}
}

// TestADBTimeoutOnDeadHost keeps the client honest about hangs.
func TestADBTimeoutOnDeadHost(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dead := l.Addr().String()
	_ = l.Close()
	key, err := loadADBKey(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer cancel()
	if _, err := connectADB(ctx, dead, key); err == nil {
		t.Fatal("connect to dead host must fail")
	}
}
