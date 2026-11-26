package hap

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"

	"golang.org/x/crypto/chacha20poly1305"
)

// secureConn frames HAP session traffic: 2-byte little-endian length (also
// the additional data), ciphertext, 16-byte tag; nonce = per-direction
// 64-bit counter.
type secureConn struct {
	net.Conn
	wmu, rmu   sync.Mutex
	wkey, rkey []byte
	wn, rn     uint64
	buf        []byte
}

func counterNonce(n uint64) []byte {
	b := make([]byte, 12)
	binary.LittleEndian.PutUint64(b[4:], n)
	return b
}

func (c *secureConn) Write(p []byte) (int, error) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	a, _ := chacha20poly1305.New(c.wkey)
	total := 0
	for len(p) > 0 {
		n := min(len(p), 1024)
		ad := []byte{byte(n), byte(n >> 8)}
		frame := a.Seal(append([]byte{}, ad...), counterNonce(c.wn), p[:n], ad)
		c.wn++
		if _, err := c.Conn.Write(frame); err != nil {
			return total, err
		}
		total += n
		p = p[n:]
	}
	return total, nil
}

func (c *secureConn) Read(p []byte) (int, error) {
	c.rmu.Lock()
	defer c.rmu.Unlock()
	for len(c.buf) == 0 {
		var ad [2]byte
		if _, err := io.ReadFull(c.Conn, ad[:]); err != nil {
			return 0, err
		}
		n := int(binary.LittleEndian.Uint16(ad[:]))
		if n > 1024 {
			return 0, errors.New("hap: frame too large")
		}
		ct := make([]byte, n+16)
		if _, err := io.ReadFull(c.Conn, ct); err != nil {
			return 0, err
		}
		a, _ := chacha20poly1305.New(c.rkey)
		plain, err := a.Open(nil, counterNonce(c.rn), ct, ad[:])
		if err != nil {
			return 0, errors.New("hap: frame does not decrypt")
		}
		c.rn++
		c.buf = plain
	}
	n := copy(p, c.buf)
	c.buf = c.buf[n:]
	return n, nil
}
