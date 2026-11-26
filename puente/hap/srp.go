package hap

import (
	"crypto/rand"
	"crypto/sha512"
	"errors"
	"math/big"
)

// SRP-6a with the RFC 5054 3072-bit group, g = 5 and SHA-512, as HomeKit
// pair-setup uses it (username "Pair-Setup", password the setup code).
var (
	srpN, _ = new(big.Int).SetString("FFFFFFFFFFFFFFFFC90FDAA22168C234C4C6628B80DC1CD129024E088A67CC74020BBEA63B139B22514A08798E3404DDEF9519B3CD3A431B302B0A6DF25F14374FE1356D6D51C245E485B576625E7EC6F44C42E9A637ED6B0BFF5CB6F406B7EDEE386BFB5A899FA5AE9F24117C4B1FE649286651ECE45B3DC2007CB8A163BF0598DA48361C55D39A69163FA8FD24CF5F83655D23DCA3AD961C62F356208552BB9ED529077096966D670C354E4ABC9804F1746C08CA18217C32905E462E36CE3BE39E772C180E86039B2783A2EC07A28FB5C55DF06F4C52C9DE2BCBF6955817183995497CEA956AE515D2261898FA051015728E5A8AAAC42DAD33170D04507A33A85521ABDF1CBA64ECFB850458DBEF0A8AEA71575D060C7DB3970F85A6E1E4C7ABF5AE8CDB0933D71E8C94E04A25619DCEE3D2261AD2EE6BF12FFA06D98A0864D87602733EC86A64521F2B18177B200CBBE117577A615D6C770988C0BAD946E208E24FA074E5AB3143DB5BFCE0FD108E4B82D120A93AD2CAFFFFFFFFFFFFFFFF", 16)
	srpG    = big.NewInt(5)
)

const srpUser = "Pair-Setup"

func hsum(parts ...[]byte) []byte {
	h := sha512.New()
	for _, p := range parts {
		h.Write(p)
	}
	return h.Sum(nil)
}

func pad(n *big.Int) []byte {
	b := n.Bytes()
	size := (srpN.BitLen() + 7) / 8
	if len(b) >= size {
		return b
	}
	out := make([]byte, size)
	copy(out[size-len(b):], b)
	return out
}

type srpClient struct {
	a, A *big.Int
	K    []byte // session key H(S)
	M1   []byte
}

func newSRPClient() (*srpClient, error) {
	ab := make([]byte, 32)
	if _, err := rand.Read(ab); err != nil {
		return nil, err
	}
	a := new(big.Int).SetBytes(ab)
	return &srpClient{a: a, A: new(big.Int).Exp(srpG, a, srpN)}, nil
}

// process computes the session key and client proof from the accessory's
// salt and public key B.
func (c *srpClient) process(salt, Bb []byte, code string) error {
	B := new(big.Int).SetBytes(Bb)
	if new(big.Int).Mod(B, srpN).Sign() == 0 {
		return errors.New("hap: invalid srp public key")
	}
	u := new(big.Int).SetBytes(hsum(pad(c.A), pad(B)))
	if u.Sign() == 0 {
		return errors.New("hap: invalid srp scrambler")
	}
	k := new(big.Int).SetBytes(hsum(srpN.Bytes(), pad(srpG)))
	x := new(big.Int).SetBytes(hsum(salt, hsum([]byte(srpUser+":"+code))))
	// S = (B - k*g^x) ^ (a + u*x) mod N
	gx := new(big.Int).Exp(srpG, x, srpN)
	base := new(big.Int).Sub(B, new(big.Int).Mul(k, gx))
	base.Mod(base, srpN)
	exp := new(big.Int).Add(c.a, new(big.Int).Mul(u, x))
	S := new(big.Int).Exp(base, exp, srpN)
	c.K = hsum(S.Bytes())
	hn := new(big.Int).SetBytes(hsum(srpN.Bytes()))
	hg := new(big.Int).SetBytes(hsum(srpG.Bytes()))
	c.M1 = hsum(hn.Xor(hn, hg).Bytes(), hsum([]byte(srpUser)), salt, c.A.Bytes(), B.Bytes(), c.K)
	return nil
}

// verify checks the accessory proof M2 = H(A | M1 | K).
func (c *srpClient) verify(M2 []byte) bool {
	want := hsum(c.A.Bytes(), c.M1, c.K)
	if len(want) != len(M2) {
		return false
	}
	var d byte
	for i := range want {
		d |= want[i] ^ M2[i]
	}
	return d == 0
}
