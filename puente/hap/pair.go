package hap

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha512"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/hkdf"
)

// Controller is this device's long-term HomeKit identity.
type Controller struct {
	ID   string             `json:"id"`
	LTSK ed25519.PrivateKey `json:"ltsk"`
}

// Pairing is everything needed to open sessions with one accessory. It
// grants control of the accessory: keep it secret.
type Pairing struct {
	Controller    Controller        `json:"controller"`
	AccessoryID   string            `json:"accessory_id"`
	AccessoryLTPK ed25519.PublicKey `json:"accessory_ltpk"`
}

func NewController(id string) (Controller, error) {
	_, sk, err := ed25519.GenerateKey(rand.Reader)
	return Controller{ID: id, LTSK: sk}, err
}

func hkdf32(ikm []byte, salt, info string) []byte {
	out := make([]byte, 32)
	io.ReadFull(hkdf.New(sha512.New, ikm, []byte(salt), []byte(info)), out)
	return out
}

func nonce(label string) []byte { return append(make([]byte, 4), label...) }

func seal(key []byte, label string, plain []byte) []byte {
	a, _ := chacha20poly1305.New(key)
	return a.Seal(nil, nonce(label), plain, nil)
}

func open(key []byte, label string, ct []byte) ([]byte, error) {
	a, _ := chacha20poly1305.New(key)
	return a.Open(nil, nonce(label), ct, nil)
}

// FormatCode turns "12344321" or "123-44-321" into the "XXX-XX-XXX" form.
func FormatCode(code string) (string, error) {
	var d []byte
	for _, r := range code {
		if r >= '0' && r <= '9' {
			d = append(d, byte(r))
		}
	}
	if len(d) != 8 {
		return "", errors.New("hap: the setup code has 8 digits")
	}
	return string(d[:3]) + "-" + string(d[3:5]) + "-" + string(d[5:]), nil
}

// post sends one TLV request on an open connection and returns its TLV reply.
func post(conn net.Conn, br *bufio.Reader, path string, body []byte) (map[byte][]byte, error) {
	req, _ := http.NewRequest("POST", "http://"+conn.RemoteAddr().String()+path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/pairing+tlv8")
	if err := req.Write(conn); err != nil {
		return nil, err
	}
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("hap: %s: %s", path, resp.Status)
	}
	m, err := decodeTLV(b)
	if err != nil {
		return nil, err
	}
	if e, ok := m[tError]; ok && len(e) > 0 {
		return nil, fmt.Errorf("hap: %s: accessory error %d%s", path, e[0], errHint(e[0]))
	}
	return m, nil
}

func errHint(code byte) string {
	switch code {
	case 2:
		return " (authentication failed: wrong setup code?)"
	case 3:
		return " (too many attempts: wait before retrying)"
	case 6:
		return " (accessory already paired with another controller)"
	case 7:
		return " (accessory busy pairing)"
	}
	return ""
}

func dial(addr string) (net.Conn, *bufio.Reader, error) {
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return nil, nil, err
	}
	return c, bufio.NewReader(c), nil
}

// Pair runs pair-setup with the accessory's 8-digit setup code.
func Pair(addr, code string, ctrl Controller) (*Pairing, error) {
	code, err := FormatCode(code)
	if err != nil {
		return nil, err
	}
	conn, br, err := dial(addr)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(60 * time.Second))

	m, err := post(conn, br, "/pair-setup", encodeTLV(tlvItem{tState, []byte{1}}, tlvItem{tMethod, []byte{0}}))
	if err != nil {
		return nil, err
	}
	if len(m[tSalt]) == 0 || len(m[tPublicKey]) == 0 {
		return nil, errors.New("hap: pair-setup M2 incomplete")
	}
	srp, err := newSRPClient()
	if err != nil {
		return nil, err
	}
	if err := srp.process(m[tSalt], m[tPublicKey], code); err != nil {
		return nil, err
	}
	m, err = post(conn, br, "/pair-setup", encodeTLV(tlvItem{tState, []byte{3}}, tlvItem{tPublicKey, srp.A.Bytes()}, tlvItem{tProof, srp.M1}))
	if err != nil {
		return nil, err
	}
	if !srp.verify(m[tProof]) {
		return nil, errors.New("hap: accessory proof is invalid")
	}

	key := hkdf32(srp.K, "Pair-Setup-Encrypt-Salt", "Pair-Setup-Encrypt-Info")
	x := hkdf32(srp.K, "Pair-Setup-Controller-Sign-Salt", "Pair-Setup-Controller-Sign-Info")
	ltpk := ctrl.LTSK.Public().(ed25519.PublicKey)
	sig := ed25519.Sign(ctrl.LTSK, append(append(append([]byte{}, x...), ctrl.ID...), ltpk...))
	sub := encodeTLV(tlvItem{tIdentifier, []byte(ctrl.ID)}, tlvItem{tPublicKey, ltpk}, tlvItem{tSignature, sig})
	m, err = post(conn, br, "/pair-setup", encodeTLV(tlvItem{tState, []byte{5}}, tlvItem{tEncryptedData, seal(key, "PS-Msg05", sub)}))
	if err != nil {
		return nil, err
	}
	plain, err := open(key, "PS-Msg06", m[tEncryptedData])
	if err != nil {
		return nil, errors.New("hap: pair-setup M6 does not decrypt")
	}
	acc, err := decodeTLV(plain)
	if err != nil {
		return nil, err
	}
	accID, accLTPK := acc[tIdentifier], acc[tPublicKey]
	if len(accID) == 0 || len(accLTPK) != ed25519.PublicKeySize {
		return nil, errors.New("hap: pair-setup M6 incomplete")
	}
	ax := hkdf32(srp.K, "Pair-Setup-Accessory-Sign-Salt", "Pair-Setup-Accessory-Sign-Info")
	if !ed25519.Verify(accLTPK, append(append(append([]byte{}, ax...), accID...), accLTPK...), acc[tSignature]) {
		return nil, errors.New("hap: accessory signature is invalid")
	}
	return &Pairing{Controller: ctrl, AccessoryID: string(accID), AccessoryLTPK: ed25519.PublicKey(accLTPK)}, nil
}

// verify runs pair-verify on conn and returns the session keys
// (controller-to-accessory, accessory-to-controller).
func verify(conn net.Conn, br *bufio.Reader, p *Pairing) (write, read []byte, err error) {
	var priv [32]byte
	if _, err := rand.Read(priv[:]); err != nil {
		return nil, nil, err
	}
	pub, _ := curve25519.X25519(priv[:], curve25519.Basepoint)
	m, err := post(conn, br, "/pair-verify", encodeTLV(tlvItem{tState, []byte{1}}, tlvItem{tPublicKey, pub}))
	if err != nil {
		return nil, nil, err
	}
	accPub := m[tPublicKey]
	if len(accPub) != 32 {
		return nil, nil, errors.New("hap: pair-verify M2 incomplete")
	}
	shared, err := curve25519.X25519(priv[:], accPub)
	if err != nil {
		return nil, nil, err
	}
	key := hkdf32(shared, "Pair-Verify-Encrypt-Salt", "Pair-Verify-Encrypt-Info")
	plain, err := open(key, "PV-Msg02", m[tEncryptedData])
	if err != nil {
		return nil, nil, errors.New("hap: pair-verify M2 does not decrypt")
	}
	sub, err := decodeTLV(plain)
	if err != nil {
		return nil, nil, err
	}
	if string(sub[tIdentifier]) != p.AccessoryID {
		return nil, nil, errors.New("hap: unexpected accessory identifier")
	}
	info := append(append(append([]byte{}, accPub...), p.AccessoryID...), pub...)
	if len(p.AccessoryLTPK) != ed25519.PublicKeySize || !ed25519.Verify(p.AccessoryLTPK, info, sub[tSignature]) {
		return nil, nil, errors.New("hap: accessory failed to prove its identity")
	}
	mine := append(append(append([]byte{}, pub...), p.Controller.ID...), accPub...)
	out := encodeTLV(tlvItem{tIdentifier, []byte(p.Controller.ID)}, tlvItem{tSignature, ed25519.Sign(p.Controller.LTSK, mine)})
	if _, err := post(conn, br, "/pair-verify", encodeTLV(tlvItem{tState, []byte{3}}, tlvItem{tEncryptedData, seal(key, "PV-Msg03", out)})); err != nil {
		return nil, nil, err
	}
	return hkdf32(shared, "Control-Salt", "Control-Write-Encryption-Key"),
		hkdf32(shared, "Control-Salt", "Control-Read-Encryption-Key"), nil
}
