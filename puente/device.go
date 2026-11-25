package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// Device is a minimal client for the LibreEcho web API (/api/v1).
type Device struct {
	Base     string // e.g. http://127.0.0.1:8080/api/v1
	User     string
	Password string
	HTTP     *http.Client

	mu    sync.Mutex
	token string
	csrf  string
	exp   time.Time
}

type envelope struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// LoadCreds reads "username\npassword" from a file.
func LoadCreds(path string) (string, string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	parts := strings.SplitN(strings.TrimSpace(string(b)), "\n", 2)
	if len(parts) != 2 {
		return "", "", errors.New("credentials file must hold username and password on two lines")
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), nil
}

func (d *Device) login() error {
	cfg, err := d.raw("GET", "/config", nil, "", "")
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	var c struct {
		CSRF string `json:"csrf_token"`
	}
	if err := json.Unmarshal(cfg, &c); err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]string{"username": d.User, "password": d.Password})
	res, err := d.raw("POST", "/auth/login", body, "", c.CSRF)
	if err != nil {
		return fmt.Errorf("login: %w", err)
	}
	var l struct {
		Token     string `json:"token"`
		ExpiresIn int    `json:"expires_in"`
	}
	if err := json.Unmarshal(res, &l); err != nil {
		return err
	}
	if l.ExpiresIn <= 0 {
		l.ExpiresIn = 3600
	}
	d.token, d.csrf = l.Token, c.CSRF
	d.exp = time.Now().Add(time.Duration(l.ExpiresIn)*time.Second - 5*time.Minute)
	return nil
}

func (d *Device) raw(method, path string, body []byte, token, csrf string) (json.RawMessage, error) {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, d.Base+path, r)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if csrf != "" {
		req.Header.Set("X-LibreEcho-CSRF", csrf)
	}
	resp, err := d.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var e envelope
	if err := json.Unmarshal(b, &e); err != nil {
		return nil, fmt.Errorf("HTTP %d: invalid JSON", resp.StatusCode)
	}
	if !e.OK {
		if e.Error != nil {
			return nil, &apiError{Status: resp.StatusCode, Code: e.Error.Code, Message: e.Error.Message}
		}
		return nil, &apiError{Status: resp.StatusCode}
	}
	return e.Data, nil
}

type apiError struct {
	Status        int
	Code, Message string
}

func (e *apiError) Error() string { return fmt.Sprintf("HTTP %d %s: %s", e.Status, e.Code, e.Message) }

// Call performs an authenticated request, logging in again once on 401.
func (d *Device) Call(method, path string, payload any, out any) error {
	var body []byte
	if payload != nil {
		var err error
		if body, err = json.Marshal(payload); err != nil {
			return err
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for attempt := 0; attempt < 2; attempt++ {
		if d.token == "" || time.Now().After(d.exp) {
			if err := d.login(); err != nil {
				return err
			}
		}
		data, err := d.raw(method, path, body, d.token, d.csrf)
		var ae *apiError
		if errors.As(err, &ae) && ae.Status == http.StatusUnauthorized && attempt == 0 {
			d.token = ""
			continue
		}
		if err != nil {
			return err
		}
		if out != nil && len(data) > 0 && string(data) != "null" {
			return json.Unmarshal(data, out)
		}
		return nil
	}
	return errors.New("unauthorized")
}
