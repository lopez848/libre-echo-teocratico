package hap

import (
	"context"
	"net"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// fakeResponder answers an mDNS PTR query for _hap._tcp.local like an
// accessory does (PTR + SRV + TXT + A in one reply).
func fakeResponder(t *testing.T, id string, ip net.IP, port uint16) string {
	t.Helper()
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 1500)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			var p dnsmessage.Parser
			h, err := p.Start(buf[:n])
			if err != nil {
				continue
			}
			q, err := p.Question()
			if err != nil || q.Type != dnsmessage.TypePTR {
				continue
			}
			inst := dnsmessage.MustNewName("Aire acondicionado._hap._tcp.local.")
			host := dnsmessage.MustNewName("Aire-acondicionado.local.")
			b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: h.ID, Response: true, Authoritative: true})
			b.EnableCompression()
			b.StartQuestions()
			b.Question(q)
			b.StartAnswers()
			b.PTRResource(dnsmessage.ResourceHeader{Name: q.Name, Class: dnsmessage.ClassINET, TTL: 120}, dnsmessage.PTRResource{PTR: inst})
			b.StartAdditionals()
			b.SRVResource(dnsmessage.ResourceHeader{Name: inst, Class: dnsmessage.ClassINET, TTL: 120}, dnsmessage.SRVResource{Port: port, Target: host})
			b.TXTResource(dnsmessage.ResourceHeader{Name: inst, Class: dnsmessage.ClassINET, TTL: 120}, dnsmessage.TXTResource{TXT: []string{"c#=10", "id=" + id, "md=ecobee3 lite", "sf=0"}})
			var a [4]byte
			copy(a[:], ip.To4())
			b.AResource(dnsmessage.ResourceHeader{Name: host, Class: dnsmessage.ClassINET, TTL: 120}, dnsmessage.AResource{A: a})
			out, _ := b.Finish()
			pc.WriteTo(out, from)
		}
	}()
	return pc.LocalAddr().String()
}

func TestBrowseFindsTheAccessoryByID(t *testing.T) {
	target := fakeResponder(t, "32:03:41:A1:F9:0A", net.ParseIP("192.168.1.230"), 53940)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	addr, err := Lookup(ctx, target, "32:03:41:a1:f9:0a")
	if err != nil {
		t.Fatal(err)
	}
	if addr != "192.168.1.230:53940" {
		t.Fatalf("addr %q", addr)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel2()
	if _, err := Lookup(ctx2, target, "AA:BB:CC:DD:EE:FF"); err == nil {
		t.Fatal("an unknown accessory id must not be found")
	}
}
