package e2e

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

type backend struct {
	*http.Server
	name string
	ln   net.Listener
}

func newBackend(t *testing.T, name, addr string) *backend {
	t.Helper()

	ln, err := net.Listen("tcp", addr)
	require.NoError(t, err)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /echo", attachName(name, echoHandler))
	server := &http.Server{
		Addr:    ln.Addr().String(),
		Handler: mux,
	}
	return &backend{
		name:   name,
		Server: server,
		ln:     ln,
	}
}

func attachName(name string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Backend-Name", name)
		next(w, r)
	}
}

func echoHandler(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusInternalServerError)
		return
	}
	defer r.Body.Close()

	headers := map[string]string{}
	for name, values := range r.Header {
		headers[name] = values[0] // Use the first value for simplicity
	}

	response := map[string]interface{}{
		"headers": headers,
		"body":    string(body),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}
