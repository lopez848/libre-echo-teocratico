package main

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// modulusBytes is the size in bytes of a 2048-bit RSA modulus, the size
// Android's public key format stores the modulus and rr at.
const modulusBytes = 2048 / 8

// adbVersion is the ADB protocol version this client speaks and adbMaxData
// the packet size it advertises (same values as the reference client).
const (
	adbVersion = 0x01000000
	adbMaxData = 1 << 20

	adbAuthToken = 1 // AUTH arg0: token for the client to sign
	adbAuthSign  = 2 // AUTH arg0: the signed token
	adbAuthPub   = 3 // AUTH arg0: the client's public key line
)

// adb commands on the wire.
const (
	cmdCNXN = "CNXN"
	cmdAUTH = "AUTH"
	cmdOKAY = "OKAY"
	cmdOPEN = "OPEN"
	cmdWRTE = "WRTE"
	cmdCLSE = "CLSE"
)

// errADBPairing means adbd knows nothing about our key yet: someone has to
// accept the pairing dialog on the TV once (the key line we just sent). The
// caller may retry right after the TV was accepted.
var errADBPairing = errors.New("adb: unknown key; accept the pairing dialog on the TV")

// adbPairWait is how long the client waits for adbd to answer the key line.
// adbd answers only once somebody accepts the debugging dialog on the screen,
// so a silent wait means the key is still unknown rather than the TV being
// unreachable. Tests shorten it.
var adbPairWait = 8 * time.Second

// wireMsg is one ADB protocol message: a 24-byte header of six little-endian
// words (command, arg0, arg1, length, checksum, magic) plus the data.
type wireMsg struct {
	command string
	arg0    uint32
	arg1    uint32
	length  uint32
	data    []byte
}

func mkMsg(command string, arg0, arg1 uint32, data []byte) wireMsg {
	return wireMsg{command: command, arg0: arg0, arg1: arg1, data: data}
}

func cmdWord(command string) uint32 { return binary.LittleEndian.Uint32([]byte(command)) }

func checksumOf(data []byte) uint32 {
	s := uint32(0)
	for _, b := range data {
		s += uint32(b)
	}
	return s
}

// packMsg serializes a message onto the wire.
func packMsg(m wireMsg) []byte {
	out := make([]byte, 24, 24+len(m.data))
	u32 := func(k int, v uint32) { binary.LittleEndian.PutUint32(out[k:k+4], v) }
	u32(0, cmdWord(m.command))
	u32(4, m.arg0)
	u32(8, m.arg1)
	u32(12, uint32(len(m.data)))
	u32(16, checksumOf(m.data))
	u32(20, ^cmdWord(m.command))
	return append(out, m.data...)
}

// unpackMsg reads the 24-byte header of a message; the caller reads m.length
// more bytes as the data.
func unpackMsg(head []byte) (wireMsg, error) {
	if len(head) < 24 {
		return wireMsg{}, errors.New("adb: short message header")
	}
	m := wireMsg{
		command: string(head[:4]),
		arg0:    binary.LittleEndian.Uint32(head[4:]),
		arg1:    binary.LittleEndian.Uint32(head[8:]),
		length:  binary.LittleEndian.Uint32(head[12:]),
	}
	if m.length > adbMaxData {
		return wireMsg{}, fmt.Errorf("adb: oversized message %d", m.length)
	}
	return m, nil
}

func sendMsg(c net.Conn, m wireMsg) error {
	_ = c.SetWriteDeadline(time.Now().Add(20 * time.Second))
	_, err := c.Write(packMsg(m))
	return err
}

// readFull reads exactly len(buf) bytes with a deadline.
func readFull(c net.Conn, buf []byte, deadline time.Time) error {
	_ = c.SetReadDeadline(deadline)
	_, err := io.ReadFull(c, buf)
	return err
}

// adbKey is the RSA key the bridge presents to TVs: the identical key every
// time, so a TV that accepted it once keeps accepting it.
type adbKey struct {
	Private *rsa.PrivateKey
	publine string // the public key line adbd accepts once
}

// loadADBKey reads or creates the bridge key in dir: "adbkey" as a PKCS#8
// PEM with mode 600 and its public key line right next to it.
func loadADBKey(dir string) (*adbKey, error) {
	if dir == "" {
		return nil, errors.New("adb: no key directory")
	}
	privPath := filepath.Join(dir, "adbkey")
	if pemBytes, err := os.ReadFile(privPath); err == nil {
		block, _ := pem.Decode(pemBytes)
		if block == nil {
			return nil, errors.New("adb: bad adbkey PEM")
		}
		k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		priv, ok := k.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("adb: adbkey is not RSA")
		}
		return newADBKey(priv), nil
	}

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	block, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(privPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: block}), 0o600); err != nil {
		return nil, err
	}
	k := newADBKey(priv)
	if err := os.WriteFile(privPath+".pub", []byte(k.publine+"\n"), 0o600); err != nil {
		return nil, err
	}
	return k, nil
}

func newADBKey(priv *rsa.PrivateKey) *adbKey {
	// Android's public key format is not DER: it is the RSAPublicKey
	// struct of android_pubkey.c packed little-endian and base64'd, with
	// " user@host" after it.
	pub := priv.PublicKey
	n, e := pub.N, big.NewInt(int64(pub.E))
	r32 := big.NewInt(1)
	r32.Lsh(r32, 32)
	n0inv := new(big.Int).Mod(n, r32)
	n0inv.ModInverse(n0inv, r32)
	n0inv.Sub(r32, n0inv)
	rr := big.NewInt(2)
	rr.Exp(rr, big.NewInt(int64(modulusBytes*8)), n)
	rr.Exp(rr, big.NewInt(2), n)

	body := make([]byte, 0, 8+2*modulusBytes+4)
	be := func(v uint32) { body = binary.LittleEndian.AppendUint32(body, v) }
	be(uint32(modulusBytes / 4))
	be(uint32(n0inv.Uint64()))
	body = append(body, littleBytes(n, modulusBytes)...)
	body = append(body, littleBytes(rr, modulusBytes)...)
	be(uint32(e.Int64()))

	enc := base64.StdEncoding.EncodeToString(body)
	return &adbKey{Private: priv, publine: enc + " puente@echo"}
}

// littleBytes writes v little-endian into exactly size bytes.
func littleBytes(v *big.Int, size int) []byte {
	raw := v.Bytes() // big-endian
	out := make([]byte, size)
	for i := range raw {
		if i >= size {
			break
		}
		out[i] = raw[len(raw)-1-i]
	}
	return out
}

// adbConn is one open ADB connection to a device.
type adbConn struct {
	conn    net.Conn
	key     *adbKey
	maxdata uint32

	mu  sync.Mutex
	next uint32
	localID  uint32
	remoteID uint32
}

// connectADB dials host, speaks the CNXN/AUTH handshake and returns the open
// connection. When the TV answers AUTH token for the second time it wants
// the pairing dialog accepted first.
func connectADB(ctx context.Context, host string, key *adbKey) (*adbConn, error) {
	d := net.Dialer{Timeout: 5 * time.Second}
	c, err := d.DialContext(ctx, "tcp", host)
	if err != nil {
		return nil, err
	}
	conn := &adbConn{conn: c, key: key}
	if err := conn.handshake(ctx); err != nil {
		_ = c.Close()
		return nil, err
	}
	return conn, nil
}

func (c *adbConn) handshake(ctx context.Context) error {
	if err := sendMsg(c.conn, mkMsg(cmdCNXN, adbVersion, adbMaxData, []byte("host::puente\x00"))); err != nil {
		return err
	}
	m, err := c.readMsg(ctx)
	if err != nil {
		return err
	}
	switch m.command {
	case cmdCNXN:
		c.maxdata = m.arg1
		return nil
	case cmdAUTH:
		if m.arg0 != adbAuthToken {
			return fmt.Errorf("adb: unknown AUTH arg0 %d", m.arg0)
		}
		sig, err := c.key.sign(m.data)
		if err != nil {
			return err
		}
		if err := sendMsg(c.conn, mkMsg(cmdAUTH, adbAuthSign, 0, sig)); err != nil {
			return err
		}
		m, err := c.readMsg(ctx)
		if err != nil {
			return err
		}
		switch m.command {
		case cmdCNXN:
			c.maxdata = m.arg1
			return nil
		case cmdAUTH:
			if m.arg0 != adbAuthToken {
				return fmt.Errorf("adb: unknown second AUTH arg0 %d", m.arg0)
			}
			if err := sendMsg(c.conn, mkMsg(cmdAUTH, adbAuthPub, 0, []byte(c.key.publine+"\x00"))); err != nil {
				return err
			}
			// adbd answers this key line only once somebody accepts the
			// debugging dialog on the screen. Silence, a closed connection
			// and anything but CNXN all mean the same thing: the key is
			// still unknown, so say that instead of letting the wait look
			// like an unreachable TV.
			pairCtx, cancel := context.WithTimeout(ctx, adbPairWait)
			defer cancel()
			m, err := c.readMsg(pairCtx)
			if err != nil {
				return fmt.Errorf("%w: %v", errADBPairing, err)
			}
			if m.command != cmdCNXN {
				return errADBPairing
			}
			c.maxdata = m.arg1
			return nil
		default:
			return fmt.Errorf("adb: unexpected %q after signature", m.command)
		}
	default:
		return fmt.Errorf("adb: unexpected %q response", m.command)
	}
}

// readMsg reads one full message (header plus its data).
func (c *adbConn) readMsg(ctx context.Context) (wireMsg, error) {
	deadline := time.Now().Add(30 * time.Second)
	if d, ok := ctx.Deadline(); ok {
		deadline = d
	}
	head := make([]byte, 24)
	if err := readFull(c.conn, head, deadline); err != nil {
		return wireMsg{}, err
	}
	m, err := unpackMsg(head)
	if err != nil {
		return wireMsg{}, err
	}
	if m.length > 0 {
		body := make([]byte, m.length)
		if err := readFull(c.conn, body, deadline); err != nil {
			return wireMsg{}, err
		}
		m.data = body
	}
	return m, nil
}

// shell opens a "shell:" stream, sends the command and returns everything
// the device prints until it closes the stream.
func (c *adbConn) shell(ctx context.Context, command string) (string, error) {
	c.mu.Lock()
	c.next++
	local := c.next
	c.mu.Unlock()

	if err := sendMsg(c.conn, mkMsg(cmdOPEN, local, 0, []byte("shell:"+command))); err != nil {
		return "", err
	}
	var out bytes.Buffer
	for {
		m, err := c.readMsg(ctx)
		if err != nil {
			return "", err
		}
		// On a connection that serves more than one command, a late message
		// from a finished stream must not be read as part of this one: adbd
		// addresses every stream message to our local id.
		if m.arg1 != 0 && m.arg1 != local {
			continue
		}
		switch m.command {
		case cmdOKAY:
			c.remoteID = m.arg0
		case cmdWRTE:
			out.Write(m.data)
			_ = sendMsg(c.conn, mkMsg(cmdOKAY, local, m.arg0, nil))
		case cmdCLSE:
			// Answer the close, or adbd keeps the stream half-open and its
			// leftovers land in whatever command runs next on this
			// connection.
			_ = sendMsg(c.conn, mkMsg(cmdCLSE, local, m.arg0, nil))
			return strings.ReplaceAll(out.String(), "\r\n", "\n"), nil
		default:
			return "", fmt.Errorf("adb: unexpected %q in shell stream", m.command)
		}
	}
}

func (c *adbConn) Close() error { return c.conn.Close() }

// adbTokenSize is the length of the AUTH token adbd sends: a SHA-1 digest,
// 20 bytes. Measured on the TCL Google TV, and the size adbd's own
// adb_auth.h declares.
const adbTokenSize = 20

// sign returns the signature adbd asked for with the token. adbd checks it
// with RSA_verify(NID_sha1, token, token_size, ...), and OpenSSL with
// NID_sha1 takes those bytes as an already-computed SHA-1 digest. So the
// token is signed prehashed: hashing it again produces a signature adbd
// rejects, which drops every connection into the pairing dialog even for a
// key the TV already stores.
func (k *adbKey) sign(token []byte) ([]byte, error) {
	if len(token) != adbTokenSize {
		return nil, fmt.Errorf("adb: AUTH token is %d bytes, want %d", len(token), adbTokenSize)
	}
	return rsa.SignPKCS1v15(rand.Reader, k.Private, crypto.SHA1, token)
}
