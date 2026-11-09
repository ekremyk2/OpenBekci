// SPDX-License-Identifier: AGPL-3.0-or-later

package trtest

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
)

// SMS is a message the fake received.
type SMS struct {
	Number, Text, Sender, IYS string
}

// IletiMerkezi is the fake İleti Merkezi server: POST /v1/send-sms/json and
// POST /v1/get-balance/json with the key and hash in the request body.
type IletiMerkezi struct {
	Server    *httptest.Server
	Key, Hash string

	mu   sync.Mutex
	sent []SMS
	seq  int
	// Code, when non-zero, is answered instead of 200 on the next send calls.
	Code int
	// Balance calls counts get-balance requests.
	Balance int
}

// NewIletiMerkezi starts a fake accepting the key and hash.
func NewIletiMerkezi(key, hash string) *IletiMerkezi {
	f := &IletiMerkezi{Key: key, Hash: hash}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	return f
}

// Close stops the server.
func (f *IletiMerkezi) Close() { f.Server.Close() }

// Client returns a client that sends every request to the fake.
func (f *IletiMerkezi) Client() *http.Client { return RewriteClient(f.Server.URL) }

// Sent returns the messages received.
func (f *IletiMerkezi) Sent() []SMS {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]SMS(nil), f.sent...)
}

func answer(w http.ResponseWriter, code int, msg string, extra map[string]any) {
	resp := map[string]any{"status": map[string]any{"code": code, "message": msg}}
	for k, v := range extra {
		resp[k] = v
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"response": resp})
}

func (f *IletiMerkezi) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var in struct {
		Request struct {
			Authentication struct{ Key, Hash string } `json:"authentication"`
			Order          struct {
				Sender  string `json:"sender"`
				IYS     string `json:"iys"`
				Message struct {
					Text       string `json:"text"`
					Receipents struct {
						Number []string `json:"number"`
					} `json:"receipents"`
				} `json:"message"`
			} `json:"order"`
		} `json:"request"`
	}
	if r.Method != http.MethodPost || json.Unmarshal(body, &in) != nil {
		answer(w, 400, "Hatalı istek", nil)
		return
	}
	if in.Request.Authentication.Key != f.Key || in.Request.Authentication.Hash != f.Hash {
		answer(w, 401, "Üyelik bilgileri hatalı", nil)
		return
	}
	switch r.URL.Path {
	case "/v1/get-balance/json":
		f.mu.Lock()
		f.Balance++
		f.mu.Unlock()
		answer(w, 200, "İşlem başarılı", map[string]any{"balance": map[string]any{"amount": "100.00", "sms": 500}})
	case "/v1/send-sms/json":
		o := in.Request.Order
		if len(o.Message.Receipents.Number) == 0 {
			answer(w, 452, "Alıcı bilgisi hatalı", nil)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.Code != 0 {
			answer(w, f.Code, "forced failure", nil)
			return
		}
		f.seq++
		for _, n := range o.Message.Receipents.Number {
			f.sent = append(f.sent, SMS{Number: n, Text: o.Message.Text, Sender: o.Sender, IYS: o.IYS})
		}
		answer(w, 200, "İşlem başarılı", map[string]any{"order": map[string]any{"id": 312891000 + f.seq}})
	default:
		answer(w, 404, "not found", nil)
	}
}

// SetCode makes the next send calls answer with the given status code (0 clears).
func (f *IletiMerkezi) SetCode(code int) {
	f.mu.Lock()
	f.Code = code
	f.mu.Unlock()
}
