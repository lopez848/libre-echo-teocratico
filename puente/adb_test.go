package main

import (
	"encoding/base64"
	"bytes"
	"context"
	"crypto"
	"crypto/rsa"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
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
	mutePub   bool   // after the key line, say nothing: the dialog is still up
	served    []string
	shellOut  func(cmd string, out func(string))

	mu       sync.Mutex
	accepted int        // CNXN handshakes served: reuse must not grow this
	live     []net.Conn // every accepted socket, so a test can cut one
	gotClose int        // CLSE answers received, one per finished stream
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
		f.mu.Lock()
		f.accepted++
		f.live = append(f.live, c)
		f.mu.Unlock()
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
				f.token = []byte("adbd-token-20-bytes!")
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
					f.token = []byte("second-token-20-byte")
					sendMsg(c, mkMsg(cmdAUTH, adbAuthToken, 0, f.token))
					continue
				}
				sendMsg(c, mkMsg(cmdCNXN, adbVersion, adbMaxData, []byte("device::ok")))
			case adbAuthPub:
				f.gotPub = append([]byte(nil), m.data...)
				if f.mutePub { // nobody accepted the dialog yet
					continue
				}
				sendMsg(c, mkMsg(cmdCNXN, adbVersion, adbMaxData, []byte("device::ok")))
			case adbAuthToken:
				sendMsg(c, mkMsg(cmdAUTH, adbAuthToken, 0, f.token))
			default:
				return
			}
		case cmdCLSE:
			f.mu.Lock()
			f.gotClose++
			f.mu.Unlock()
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

// TestADBAuthSignature proves the client signs the exact token adbd sent the
// way adbd checks it: adbd calls RSA_verify(NID_sha1, token, token_size, ...),
// and OpenSSL with NID_sha1 takes those bytes as an already-computed SHA-1
// digest. So the 20-byte token is signed prehashed, never hashed again, or
// adbd rejects every signature and falls back to the pairing dialog.
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
	if err := rsa.VerifyPKCS1v15(&key.Private.PublicKey, crypto.SHA1, f.token, f.signedTok); err != nil {
		t.Fatal("AUTH signature does not verify the way adbd checks it:", err)
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

// TestADBPairingDialogPending covers a TV that takes the key line and then
// stays silent because the debugging dialog is still waiting on its screen:
// the client must report that, not a plain unreachable TV.
func TestADBPairingDialogPending(t *testing.T) {
	f := newFakeAdbd(t, true)
	f.wantPair = true
	f.mutePub = true
	old := adbPairWait
	adbPairWait = 150 * time.Millisecond
	t.Cleanup(func() { adbPairWait = old })

	key, err := loadADBKey(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Now()
	_, err = connectADB(context.Background(), f.addr(), key)
	if !errors.Is(err, errADBPairing) {
		t.Fatalf("err = %v, want errADBPairing", err)
	}
	if d := time.Since(t0); d > 3*time.Second {
		t.Errorf("waited %v for the dialog, want about %v", d, adbPairWait)
	}
	if len(f.gotPub) == 0 {
		t.Error("the public key line never reached the TV")
	}
}

// TestADBSignRejectsOddToken covers the guard on the token length: signing
// prehashed only makes sense for the 20 bytes adbd sends, so anything else
// is reported instead of being signed into a signature adbd cannot check.
func TestADBSignRejectsOddToken(t *testing.T) {
	key, err := loadADBKey(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, tok := range [][]byte{nil, []byte("short"), make([]byte, 32)} {
		if _, err := key.sign(tok); err == nil {
			t.Errorf("sign(%d bytes) = nil error, want a refusal", len(tok))
		}
	}
	good := make([]byte, adbTokenSize)
	sig, err := key.sign(good)
	if err != nil {
		t.Fatalf("sign(%d bytes): %v", adbTokenSize, err)
	}
	if err := rsa.VerifyPKCS1v15(&key.Private.PublicKey, crypto.SHA1, good, sig); err != nil {
		t.Fatal("the 20-byte token signature does not verify:", err)
	}
}

// cut closes the fake's live sockets, the way a TV dropping off the Wi-Fi
// leaves the bridge holding a dead connection.
func (f *fakeAdbd) cut() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.live {
		_ = c.Close()
	}
	f.live = nil
}

func (f *fakeAdbd) handshakes() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.accepted
}

// TestTVReusesConnection proves consecutive commands to one TV ride the same
// ADB connection: the handshake, RSA signature included, is paid once.
func TestTVReusesConnection(t *testing.T) {
	f := newFakeAdbd(t, true)
	tvs := testTVs(t, f.addr())

	for i := 0; i < 4; i++ {
		if _, err := tvs.run(tvs.items[0], "echo hola"); err != nil {
			t.Fatalf("command %d: %v", i+1, err)
		}
	}
	if got := f.handshakes(); got != 1 {
		t.Errorf("%d handshakes for 4 commands, want 1", got)
	}
	if len(f.served) != 4 {
		t.Errorf("the TV served %d commands, want 4", len(f.served))
	}
}

// TestTVReconnectsAfterDrop covers the TV that went away and came back: the
// cached connection is dead, so the bridge makes a new one and the command
// still succeeds instead of surfacing the broken socket.
func TestTVReconnectsAfterDrop(t *testing.T) {
	f := newFakeAdbd(t, true)
	tvs := testTVs(t, f.addr())

	if _, err := tvs.run(tvs.items[0], "echo uno"); err != nil {
		t.Fatal(err)
	}
	f.cut()

	out, err := tvs.run(tvs.items[0], "echo dos")
	if err != nil {
		t.Fatalf("after the TV dropped: %v", err)
	}
	if out == "" {
		t.Error("the retried command returned nothing")
	}
	if got := f.handshakes(); got != 2 {
		t.Errorf("%d handshakes, want 2: one before the drop and one after", got)
	}
}

// TestTVDropsIdleConnection covers a connection left idle past the keepalive:
// the bridge opens a fresh one rather than waiting out a socket the TV may
// have abandoned while it slept.
func TestTVDropsIdleConnection(t *testing.T) {
	f := newFakeAdbd(t, true)
	tvs := testTVs(t, f.addr())
	tvs.idle = 30 * time.Millisecond

	if _, err := tvs.run(tvs.items[0], "echo uno"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(60 * time.Millisecond)
	if _, err := tvs.run(tvs.items[0], "echo dos"); err != nil {
		t.Fatal(err)
	}
	if got := f.handshakes(); got != 2 {
		t.Errorf("%d handshakes, want 2: the idle one must not be reused", got)
	}
}

// TestTVSerializesCommands proves commands to one TV do not interleave on the
// shared connection: two streams reading the same socket would steal each
// other's replies.
func TestTVSerializesCommands(t *testing.T) {
	f := newFakeAdbd(t, true)
	tvs := testTVs(t, f.addr())

	var wg sync.WaitGroup
	errs := make([]error, 6)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = tvs.run(tvs.items[0], fmt.Sprintf("echo %d", i))
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("command %d: %v", i, err)
		}
	}
	if got := f.handshakes(); got != 1 {
		t.Errorf("%d handshakes for 6 parallel commands, want 1", got)
	}
}

// testTVs is one TV pointed at a fake adbd, with its own key directory.
func testTVs(t *testing.T, addr string) *TVs {
	t.Helper()
	tvs := &TVs{KeyDir: t.TempDir(), items: []TV{{Name: "Sala", Host: addr}}}
	t.Cleanup(tvs.closeAll)
	return tvs
}
