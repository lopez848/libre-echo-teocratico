// Package hap is a minimal HomeKit Accessory Protocol controller over IP:
// pair-setup, pair-verify, encrypted sessions and characteristic access.
// It is enough to drive a thermostat on the local network.
package hap

import "errors"

const (
	tMethod        = 0x00
	tIdentifier    = 0x01
	tSalt          = 0x02
	tPublicKey     = 0x03
	tProof         = 0x04
	tEncryptedData = 0x05
	tState         = 0x06
	tError         = 0x07
	tSignature     = 0x0A
)

type tlvItem struct {
	typ byte
	val []byte
}

// encodeTLV writes TLV8 items, splitting values longer than 255 bytes into
// consecutive fragments of the same type.
func encodeTLV(items ...tlvItem) []byte {
	var out []byte
	for _, it := range items {
		v := it.val
		for {
			n := min(len(v), 255)
			out = append(out, it.typ, byte(n))
			out = append(out, v[:n]...)
			v = v[n:]
			if len(v) == 0 {
				break
			}
		}
	}
	return out
}

// decodeTLV joins fragments: consecutive items of the same type are one value.
func decodeTLV(b []byte) (map[byte][]byte, error) {
	m := map[byte][]byte{}
	last := -1
	for len(b) > 0 {
		if len(b) < 2 || len(b) < 2+int(b[1]) {
			return nil, errors.New("hap: truncated tlv")
		}
		typ, n := b[0], int(b[1])
		if int(typ) == last {
			m[typ] = append(m[typ], b[2:2+n]...)
		} else {
			m[typ] = append([]byte(nil), b[2:2+n]...)
		}
		last = int(typ)
		b = b[2+n:]
	}
	return m, nil
}
