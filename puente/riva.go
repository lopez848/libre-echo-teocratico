package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Riva calls NVIDIA's hosted Magpie TTS through gRPC (unary Synthesize),
// hand-encoded so the binary needs no gRPC dependency.
type Riva struct {
	URL        string // https://grpc.nvcf.nvidia.com/nvidia.riva.tts.RivaSpeechSynthesis/Synthesize
	FunctionID string
	KeyFile    string // JSON with "api_key" (agentd secrets file)
	Voice      string // e.g. Magpie-Multilingual.ES-US.Mia
	Language   string // es-US
	Rate       int
	HTTP       *http.Client
}

func pbVarint(v uint64) []byte {
	var b []byte
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

func pbString(field int, s string) []byte {
	out := pbVarint(uint64(field<<3 | 2))
	out = append(out, pbVarint(uint64(len(s)))...)
	return append(out, s...)
}

func pbInt(field int, v uint64) []byte {
	return append(pbVarint(uint64(field<<3)), pbVarint(v)...)
}

// synthRequest encodes nvidia.riva.tts.SynthesizeSpeechRequest.
func synthRequest(text, lang, voice string, rate int) []byte {
	var m []byte
	m = append(m, pbString(1, text)...)
	m = append(m, pbString(2, lang)...)
	m = append(m, pbInt(3, 1)...) // LINEAR_PCM
	m = append(m, pbInt(4, uint64(rate))...)
	m = append(m, pbString(5, voice)...)
	return m
}

// audioField extracts field 1 (bytes) from SynthesizeSpeechResponse.
func audioField(msg []byte) ([]byte, error) {
	for i := 0; i < len(msg); {
		key, n := binary.Uvarint(msg[i:])
		if n <= 0 {
			return nil, errors.New("bad protobuf key")
		}
		i += n
		field, wire := key>>3, key&7
		switch wire {
		case 0:
			_, n = binary.Uvarint(msg[i:])
			if n <= 0 {
				return nil, errors.New("bad varint")
			}
			i += n
		case 2:
			l, n := binary.Uvarint(msg[i:])
			if n <= 0 || i+n+int(l) > len(msg) {
				return nil, errors.New("bad length")
			}
			i += n
			if field == 1 {
				return msg[i : i+int(l)], nil
			}
			i += int(l)
		case 5:
			i += 4
		case 1:
			i += 8
		default:
			return nil, fmt.Errorf("unsupported wire type %d", wire)
		}
	}
	return nil, errors.New("no audio in response")
}

func (r *Riva) key() (string, error) {
	b, err := os.ReadFile(r.KeyFile)
	if err != nil {
		return "", err
	}
	var s struct {
		APIKey string `json:"api_key"`
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return "", err
	}
	if s.APIKey == "" {
		return "", errors.New("api_key is empty")
	}
	return s.APIKey, nil
}

// Synthesize returns 16-bit mono PCM at r.Rate.
func (r *Riva) Synthesize(ctx context.Context, text string) ([]byte, error) {
	key, err := r.key()
	if err != nil {
		return nil, err
	}
	msg := synthRequest(text, r.Language, r.Voice, r.Rate)
	frame := make([]byte, 5, 5+len(msg))
	binary.BigEndian.PutUint32(frame[1:], uint32(len(msg)))
	frame = append(frame, msg...)
	req, err := http.NewRequestWithContext(ctx, "POST", r.URL, bytes.NewReader(frame))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/grpc")
	req.Header.Set("TE", "trailers")
	req.Header.Set("function-id", r.FunctionID)
	req.Header.Set("authorization", "Bearer "+key)
	start := time.Now()
	resp, err := r.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	status := firstNonEmpty(resp.Trailer.Get("grpc-status"), resp.Header.Get("grpc-status"))
	if resp.StatusCode != 200 || (status != "" && status != "0") {
		m := firstNonEmpty(resp.Trailer.Get("grpc-message"), resp.Header.Get("grpc-message"))
		return nil, fmt.Errorf("riva HTTP %d grpc-status %s: %s", resp.StatusCode, status, m)
	}
	if len(body) < 5 {
		return nil, errors.New("riva: empty response")
	}
	l := int(binary.BigEndian.Uint32(body[1:5]))
	if body[0] != 0 || 5+l > len(body) {
		return nil, errors.New("riva: bad gRPC frame")
	}
	pcm, err := audioField(body[5 : 5+l])
	if err != nil {
		return nil, err
	}
	if verbose {
		logf("riva %d bytes in %dms", len(pcm), time.Since(start).Milliseconds())
	}
	return pcm, nil
}

// cleanForCloud undoes the local-voice workarounds before cloud TTS.
func cleanForCloud(s string) string {
	return strings.TrimSpace(nfc(s))
}
