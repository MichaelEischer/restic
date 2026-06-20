package backend

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"testing/synctest"
	"time"

	rtest "github.com/restic/restic/internal/test"
)

func TestRead(t *testing.T) {
	data := []byte("abcdef")
	var ctr int
	kick := func() {
		ctr++
	}
	var closed bool
	onClose := func() {
		closed = true
	}
	isTimeout := func(err error) bool {
		return false
	}

	wd := newWatchdogReadCloser(io.NopCloser(bytes.NewReader(data)), 1, kick, onClose, isTimeout)

	out, err := io.ReadAll(wd)
	rtest.OK(t, err)
	rtest.Equals(t, data, out, "data mismatch")
	// the EOF read also triggers the kick function
	rtest.Equals(t, len(data)*2+2, ctr, "unexpected number of kick calls")

	rtest.Equals(t, false, closed, "close function called too early")
	rtest.OK(t, wd.Close())
	rtest.Equals(t, true, closed, "close function not called")
}

func TestRoundtrip(t *testing.T) {
	t.Parallel()

	// at the higher delay values, it takes longer to transmit the request/response body
	// than the roundTripper timeout
	for _, delay := range []int{0, 1, 10, 20} {
		t.Run(fmt.Sprintf("%v", delay), func(t *testing.T) {
			// synctest.Test runs with a fake clock so time.Sleep calls in the
			// slow reader and server handler complete instantly.
			synctest.Test(t, func(t *testing.T) {
				msg := []byte("ping-pong-data")

				// net.Pipe creates an in-memory connection backed by channels,
				// so goroutines blocked on pipe I/O are durably blocked within
				// the synctest bubble and fake time can advance past time.Sleep.
				srvConn, cliConn := net.Pipe()
				defer func() { _ = srvConn.Close() }()
				defer func() { _ = cliConn.Close() }()

				srv := &http.Server{
					Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						data, err := io.ReadAll(r.Body)
						if err != nil {
							w.WriteHeader(500)
							return
						}
						w.WriteHeader(200)

						// slowly send the reply
						for len(data) >= 2 {
							_, _ = w.Write(data[:2])
							w.(http.Flusher).Flush()
							data = data[2:]
							time.Sleep(time.Duration(delay) * time.Millisecond)
						}
						_, _ = w.Write(data)
					}),
				}
				go srv.Serve(&oneConnListener{conn: srvConn, addr: srvConn.LocalAddr()}) //nolint:errcheck
				defer func() { _ = srv.Close() }()

				transport := &http.Transport{
					DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
						return cliConn, nil
					},
				}
				defer transport.CloseIdleConnections()

				rt := newWatchdogRoundtripper(transport, 100*time.Millisecond, 2)
				req, err := http.NewRequestWithContext(t.Context(), "GET", "http://test.invalid/", io.NopCloser(newSlowReader(bytes.NewReader(msg), time.Duration(delay)*time.Millisecond)))
				rtest.OK(t, err)

				resp, err := rt.RoundTrip(req)
				rtest.OK(t, err)
				rtest.Equals(t, 200, resp.StatusCode, "unexpected status code")

				response, err := io.ReadAll(resp.Body)
				rtest.OK(t, err)
				rtest.Equals(t, msg, response, "unexpected response")

				rtest.OK(t, resp.Body.Close())
			})
		})
	}
}

// oneConnListener is a net.Listener that serves a single pre-established
// connection and returns net.ErrClosed on every subsequent Accept call.
type oneConnListener struct {
	conn net.Conn
	addr net.Addr
}

func (l *oneConnListener) Accept() (net.Conn, error) {
	c := l.conn
	if c == nil {
		return nil, net.ErrClosed
	}
	l.conn = nil
	return c, nil
}

func (l *oneConnListener) Close() error   { return nil }
func (l *oneConnListener) Addr() net.Addr { return l.addr }

func TestCanceledRoundtrip(t *testing.T) {
	rt := newWatchdogRoundtripper(http.DefaultTransport, time.Second, 2)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", "http://some.random.url.dfdgsfg", nil)
	rtest.OK(t, err)

	resp, err := rt.RoundTrip(req)
	rtest.Equals(t, context.Canceled, err)
	// make linter happy
	if resp != nil {
		rtest.OK(t, resp.Body.Close())
	}
}

type slowReader struct {
	data  io.Reader
	delay time.Duration
}

func newSlowReader(data io.Reader, delay time.Duration) *slowReader {
	return &slowReader{
		data:  data,
		delay: delay,
	}
}

func (s *slowReader) Read(p []byte) (n int, err error) {
	time.Sleep(s.delay)
	return s.data.Read(p)
}

func TestUploadTimeout(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		// After RoundTrip returns, the transport's writeLoop goroutine is still
		// sleeping inside slowReader (100 ms fake). t.Cleanup runs inside the
		// bubble after all defers, so the root goroutine is durably blocked via
		// time.Sleep, which lets fake time advance past that sleep and lets the
		// goroutine exit before the bubble ends.
		t.Cleanup(func() { time.Sleep(200 * time.Millisecond) })

		msg := []byte("ping")
		srvConn, cliConn := net.Pipe()
		defer func() { _ = srvConn.Close() }()
		defer func() { _ = cliConn.Close() }()
		srv := &http.Server{
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, err := io.ReadAll(r.Body)
				if err != nil {
					w.WriteHeader(500)
					return
				}
				t.Error("upload should have been canceled")
			}),
		}
		go srv.Serve(&oneConnListener{conn: srvConn, addr: srvConn.LocalAddr()}) //nolint:errcheck
		defer func() { _ = srv.Close() }()
		transport := &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return cliConn, nil
			},
		}
		defer transport.CloseIdleConnections()

		rt := newWatchdogRoundtripper(transport, 10*time.Millisecond, 1024)
		req, err := http.NewRequestWithContext(t.Context(), "GET", "http://test.invalid/", io.NopCloser(newSlowReader(bytes.NewReader(msg), 100*time.Millisecond)))
		rtest.OK(t, err)

		resp, err := rt.RoundTrip(req)
		rtest.Equals(t, errRequestTimeout, err)
		// make linter happy
		if resp != nil {
			rtest.OK(t, resp.Body.Close())
		}
	})
}

func TestProcessingTimeout(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		// After RoundTrip returns, the server handler goroutine is still sleeping
		// for 100 ms (fake). t.Cleanup runs inside the bubble after all defers, so
		// the root goroutine is durably blocked via time.Sleep, which lets fake time
		// advance past that sleep and lets the goroutine exit before the bubble ends.
		t.Cleanup(func() { time.Sleep(200 * time.Millisecond) })

		msg := []byte("ping")
		srvConn, cliConn := net.Pipe()
		defer func() { _ = srvConn.Close() }()
		defer func() { _ = cliConn.Close() }()
		srv := &http.Server{
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, err := io.ReadAll(r.Body)
				if err != nil {
					w.WriteHeader(500)
					return
				}
				time.Sleep(100 * time.Millisecond)
				w.WriteHeader(200)
			}),
		}
		go srv.Serve(&oneConnListener{conn: srvConn, addr: srvConn.LocalAddr()}) //nolint:errcheck
		defer func() { _ = srv.Close() }()
		transport := &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return cliConn, nil
			},
		}
		defer transport.CloseIdleConnections()

		rt := newWatchdogRoundtripper(transport, 10*time.Millisecond, 1024)
		req, err := http.NewRequestWithContext(t.Context(), "GET", "http://test.invalid/", io.NopCloser(bytes.NewReader(msg)))
		rtest.OK(t, err)

		resp, err := rt.RoundTrip(req)
		rtest.Equals(t, errRequestTimeout, err)
		// make linter happy
		if resp != nil {
			rtest.OK(t, resp.Body.Close())
		}
	})
}

func TestDownloadTimeout(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		// After ReadAll returns, the server handler goroutine is still sleeping for
		// 100 ms (fake). t.Cleanup runs inside the bubble after all defers, so the
		// root goroutine is durably blocked via time.Sleep, which lets fake time
		// advance past that sleep and lets the goroutine exit before the bubble ends.
		t.Cleanup(func() { time.Sleep(200 * time.Millisecond) })

		msg := []byte("ping")
		srvConn, cliConn := net.Pipe()
		defer func() { _ = srvConn.Close() }()
		defer func() { _ = cliConn.Close() }()
		srv := &http.Server{
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				data, err := io.ReadAll(r.Body)
				if err != nil {
					w.WriteHeader(500)
					return
				}
				w.WriteHeader(200)
				_, _ = w.Write(data[:2])
				w.(http.Flusher).Flush()
				data = data[2:]

				time.Sleep(100 * time.Millisecond)
				_, _ = w.Write(data)
			}),
		}
		go srv.Serve(&oneConnListener{conn: srvConn, addr: srvConn.LocalAddr()}) //nolint:errcheck
		defer func() { _ = srv.Close() }()
		transport := &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return cliConn, nil
			},
		}
		defer transport.CloseIdleConnections()

		rt := newWatchdogRoundtripper(transport, 25*time.Millisecond, 1024)
		req, err := http.NewRequestWithContext(t.Context(), "GET", "http://test.invalid/", io.NopCloser(bytes.NewReader(msg)))
		rtest.OK(t, err)

		resp, err := rt.RoundTrip(req)
		rtest.OK(t, err)
		rtest.Equals(t, 200, resp.StatusCode, "unexpected status code")

		_, err = io.ReadAll(resp.Body)
		rtest.Equals(t, errRequestTimeout, err, "response download not canceled")
		rtest.OK(t, resp.Body.Close())
	})
}
