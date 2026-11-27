package hap

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Characteristic types (short UUIDs) used for a thermostat.
const (
	TypeCurrentHeatingCoolingState = "F"
	TypeTargetHeatingCoolingState  = "33"
	TypeCurrentTemperature         = "11"
	TypeTargetTemperature          = "35"
	TypeTemperatureDisplayUnits    = "36"
	TypeCurrentRelativeHumidity    = "10"
)

// ID addresses one characteristic: accessory id and instance id.
type ID struct{ AID, IID int }

// Accessories is the parsed /accessories database.
type Accessories struct {
	Accessories []struct {
		AID      int `json:"aid"`
		Services []struct {
			Type            string `json:"type"`
			Characteristics []struct {
				Type  string `json:"type"`
				IID   int    `json:"iid"`
				Value any    `json:"value"`
			} `json:"characteristics"`
		} `json:"services"`
	} `json:"accessories"`
}

func shortType(t string) string {
	t = strings.ToUpper(t)
	if i := strings.Index(t, "-0000-1000-8000-0026BB765291"); i >= 0 {
		t = t[:i]
	}
	return strings.TrimLeft(t, "0")
}

// Find returns the first characteristic of the given short type.
func (a *Accessories) Find(typ string) (ID, bool) {
	for _, acc := range a.Accessories {
		for _, s := range acc.Services {
			for _, c := range s.Characteristics {
				if shortType(c.Type) == shortType(typ) {
					return ID{acc.AID, c.IID}, true
				}
			}
		}
	}
	return ID{}, false
}

// Client is a verified, encrypted session with one accessory.
type Client struct {
	mu   sync.Mutex
	conn *secureConn
	br   *bufio.Reader
	host string
}

// Dial opens a session and runs pair-verify with a stored pairing.
func Dial(addr string, p *Pairing) (*Client, error) {
	conn, br, err := dial(addr)
	if err != nil {
		return nil, err
	}
	conn.SetDeadline(time.Now().Add(20 * time.Second))
	w, r, err := verify(conn, br, p)
	if err != nil {
		conn.Close()
		return nil, err
	}
	conn.SetDeadline(time.Time{})
	// Some accessories switch the connection to encryption only after they
	// have sent the last pair-verify reply; a request that arrives in that
	// gap is read as plain text and never answered (seen with brutella/hap
	// about once in nine sessions). Connections stay open, so this pause
	// is paid once per session.
	time.Sleep(100 * time.Millisecond)
	sc := &secureConn{Conn: conn, wkey: w, rkey: r}
	return &Client{conn: sc, br: bufio.NewReader(sc), host: addr}, nil
}

func (c *Client) Close() error { return c.conn.Close() }

func (c *Client) do(method, path string, body []byte) ([]byte, int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.conn.SetDeadline(time.Now().Add(10 * time.Second))
	defer c.conn.SetDeadline(time.Time{})
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, _ := http.NewRequest(method, "http://"+c.host+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/hap+json")
	}
	if err := req.Write(c.conn); err != nil {
		return nil, 0, err
	}
	for {
		resp, err := http.ReadResponse(c.br, req)
		if err != nil {
			return nil, 0, err
		}
		b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if err != nil {
			return nil, 0, err
		}
		if resp.Proto == "EVENT/1.0" { // notification, not our reply
			continue
		}
		return b, resp.StatusCode, nil
	}
}

func (c *Client) Accessories() (*Accessories, error) {
	b, code, err := c.do("GET", "/accessories", nil)
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("hap: accessories: status %d", code)
	}
	var a Accessories
	if err := json.Unmarshal(b, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

// Get reads characteristic values; numbers come back as float64.
func (c *Client) Get(ids ...ID) (map[ID]any, error) {
	var q []string
	for _, id := range ids {
		q = append(q, strconv.Itoa(id.AID)+"."+strconv.Itoa(id.IID))
	}
	b, code, err := c.do("GET", "/characteristics?id="+strings.Join(q, ","), nil)
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK && code != http.StatusMultiStatus {
		return nil, fmt.Errorf("hap: get: status %d", code)
	}
	var r struct {
		Characteristics []struct {
			AID    int `json:"aid"`
			IID    int `json:"iid"`
			Value  any `json:"value"`
			Status int `json:"status"`
		} `json:"characteristics"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	out := map[ID]any{}
	for _, ch := range r.Characteristics {
		if ch.Status != 0 {
			return nil, fmt.Errorf("hap: characteristic %d.%d unavailable (status %d)", ch.AID, ch.IID, ch.Status)
		}
		out[ID{ch.AID, ch.IID}] = ch.Value
	}
	for _, id := range ids {
		if _, ok := out[id]; !ok {
			return nil, fmt.Errorf("hap: characteristic %d.%d missing from the reply", id.AID, id.IID)
		}
	}
	return out, nil
}

// Put writes characteristic values.
func (c *Client) Put(vals map[ID]any) error {
	type w struct {
		AID   int `json:"aid"`
		IID   int `json:"iid"`
		Value any `json:"value"`
	}
	var body struct {
		Characteristics []w `json:"characteristics"`
	}
	for id, v := range vals {
		body.Characteristics = append(body.Characteristics, w{id.AID, id.IID, v})
	}
	b, _ := json.Marshal(body)
	resp, code, err := c.do("PUT", "/characteristics", b)
	if err != nil {
		return err
	}
	if code != http.StatusNoContent && code != http.StatusOK {
		return fmt.Errorf("hap: put: status %d %s", code, resp)
	}
	return nil
}
