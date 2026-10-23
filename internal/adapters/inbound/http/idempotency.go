package http

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
)

// IdempotencyKeyHeader is the request header every state changing POST needs.
const IdempotencyKeyHeader = "Idempotency-Key"

// ErrKeyReused is returned by an IdempotencyStore when the key was stored with
// a different request body.
var ErrKeyReused = errors.New("idempotency key reused with a different request")

// IdempotencyStore runs a request at most once per (scope, key) and replays the
// stored 2xx response for a repeat. Run serialises concurrent calls with the
// same scope and key: the second waits and then replays the first's response.
// fn is called only when nothing is stored (or the stored row expired); a
// response is stored only when fn returns a 2xx status. A key stored with
// another requestHash gives ErrKeyReused.
type IdempotencyStore interface {
	Run(ctx context.Context, scope, key, requestHash string, fn func(ctx context.Context) (status int, body []byte)) (status int, body []byte, replayed bool, err error)
}

// RequireIdempotencyKey is the middleware of the state changing POSTs: the
// header is mandatory (400 otherwise), the scope is "METHOD path" so a key is
// only valid on the resource it was first used on.
func RequireIdempotencyKey(store IdempotencyStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.Header.Get(IdempotencyKeyHeader)
			if key == "" || len(key) > 200 {
				writeProblem(w, r, idempotencyKeyRequired, "send an Idempotency-Key header of 1 to 200 characters, unique per logical request")
				return
			}
			body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
			if err != nil {
				writeError(w, r, errMalformedRequest)
				return
			}
			_ = r.Body.Close()
			sum := sha256.Sum256(body)
			status, out, replayed, err := store.Run(r.Context(), r.Method+" "+r.URL.Path, key, hex.EncodeToString(sum[:]),
				func(ctx context.Context) (int, []byte) {
					r.Body = io.NopCloser(bytes.NewReader(body))
					rec := &capture{header: http.Header{}}
					next.ServeHTTP(rec, r.WithContext(ctx))
					if rec.code == 0 {
						rec.code = http.StatusOK
					}
					copyHeader(w.Header(), rec.header)
					return rec.code, rec.body.Bytes()
				})
			switch {
			case errors.Is(err, ErrKeyReused):
				writeProblem(w, r, idempotencyKeyReused, "the Idempotency-Key was used with a different request body")
			case err != nil:
				writeError(w, r, err)
			default:
				if replayed {
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("Idempotent-Replayed", "true")
				}
				w.WriteHeader(status)
				_, _ = w.Write(out) //nolint:gosec // out is a JSON body this adapter produced (or stored), not user markup
			}
		})
	}
}

func copyHeader(dst, src http.Header) {
	for k, vs := range src {
		for _, v := range vs {
			dst.Set(k, v)
		}
	}
}

// capture records a handler's response so the store can persist it.
type capture struct {
	header http.Header
	code   int
	body   bytes.Buffer
}

func (c *capture) Header() http.Header { return c.header }

func (c *capture) WriteHeader(code int) {
	if c.code == 0 {
		c.code = code
	}
}

func (c *capture) Write(b []byte) (int, error) {
	if c.code == 0 {
		c.code = http.StatusOK
	}
	return c.body.Write(b)
}
