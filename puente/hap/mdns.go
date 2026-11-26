package hap

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// MDNSGroup is where mDNS queries go. A query sent from a port other than
// 5353 gets a unicast reply to that port (RFC 6762 section 6.7), so no
// multicast listener is needed.
const MDNSGroup = "224.0.0.251:5353"

// Lookup asks the local network for HomeKit accessories and returns the
// address of the one whose "id" (its pairing identifier) matches.
func Lookup(ctx context.Context, target, id string) (string, error) {
	pc, err := net.ListenPacket("udp4", ":0")
	if err != nil {
		return "", err
	}
	defer pc.Close()
	dst, err := net.ResolveUDPAddr("udp4", target)
	if err != nil {
		return "", err
	}
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{})
	b.StartQuestions()
	b.Question(dnsmessage.Question{Name: dnsmessage.MustNewName("_hap._tcp.local."), Type: dnsmessage.TypePTR, Class: dnsmessage.ClassINET})
	query, _ := b.Finish()

	found := map[string]*service{} // by instance name
	hosts := map[string]net.IP{}
	buf := make([]byte, 9000)
	for attempt := 0; ctx.Err() == nil; attempt++ {
		if _, err := pc.WriteTo(query, dst); err != nil {
			return "", err
		}
		until := time.Now().Add(700 * time.Millisecond)
		if d, ok := ctx.Deadline(); ok && d.Before(until) {
			until = d
		}
		pc.SetReadDeadline(until)
		for {
			n, _, err := pc.ReadFrom(buf)
			if err != nil {
				break
			}
			parseReply(buf[:n], found, hosts)
			if addr, ok := match(found, hosts, id); ok {
				return addr, nil
			}
		}
	}
	return "", errors.New("hap: accessory not found on the network")
}

type service struct {
	id, host string
	port     uint16
}

func parseReply(msg []byte, found map[string]*service, hosts map[string]net.IP) {
	var p dnsmessage.Parser
	h, err := p.Start(msg)
	if err != nil || !h.Response {
		return
	}
	p.SkipAllQuestions()
	get := func(name string) *service {
		if found[name] == nil {
			found[name] = &service{}
		}
		return found[name]
	}
	for {
		rh, err := p.AnswerHeader()
		if err != nil {
			if err == dnsmessage.ErrSectionDone {
				if p.SkipAllAuthorities() != nil {
					return
				}
				// additionals carry SRV, TXT and A records too
				for {
					rh, err := p.AdditionalHeader()
					if err != nil {
						return
					}
					readRecord(&p, rh, get, hosts, p.SkipAdditional)
				}
			}
			return
		}
		readRecord(&p, rh, get, hosts, p.SkipAnswer)
	}
}

func readRecord(p *dnsmessage.Parser, rh dnsmessage.ResourceHeader, get func(string) *service, hosts map[string]net.IP, skip func() error) {
	name := strings.ToLower(rh.Name.String())
	switch rh.Type {
	case dnsmessage.TypeSRV:
		r, err := p.SRVResource()
		if err == nil {
			s := get(name)
			s.host, s.port = strings.ToLower(r.Target.String()), r.Port
		}
	case dnsmessage.TypeTXT:
		r, err := p.TXTResource()
		if err == nil {
			for _, kv := range r.TXT {
				if k, v, ok := strings.Cut(kv, "="); ok && strings.EqualFold(k, "id") {
					get(name).id = v
				}
			}
		}
	case dnsmessage.TypeA:
		r, err := p.AResource()
		if err == nil {
			hosts[name] = net.IP(r.A[:])
		}
	default:
		skip()
	}
}

func match(found map[string]*service, hosts map[string]net.IP, id string) (string, bool) {
	for _, s := range found {
		if s.id == "" || !strings.EqualFold(s.id, id) || s.port == 0 {
			continue
		}
		if ip, ok := hosts[s.host]; ok {
			return net.JoinHostPort(ip.String(), strconv.Itoa(int(s.port))), true
		}
	}
	return "", false
}
