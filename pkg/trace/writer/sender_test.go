// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package writer

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/DataDog/datadog-agent/pkg/trace/config"
	"github.com/DataDog/datadog-agent/pkg/trace/log"
)

const testAPIKey = "123"

func TestMain(m *testing.M) {
	log.SetLogger(log.NoopLogger)
	os.Exit(m.Run())
}

func TestIsRetriable(t *testing.T) {
	for code, want := range map[int]bool{
		400: false,
		404: false,
		408: true,
		409: false,
		500: true,
		503: true,
		505: true,
		200: false,
		204: false,
		306: false,
		101: false,
	} {
		assert.Equal(t, isRetriable(code), want)
	}
}

func TestSender(t *testing.T) {
	const climit = 100
	testSenderConfig := func(serverURL string) *senderConfig {
		url, err := url.Parse(serverURL + "/")
		if err != nil {
			t.Fatal(err)
		}
		cfg := config.New()
		cfg.ConnectionResetInterval = 0
		return &senderConfig{
			client:     cfg.NewHTTPClient(),
			url:        url,
			maxConns:   climit,
			maxQueued:  40,
			maxRetries: 4,
			apiKey:     testAPIKey,
			userAgent:  "testUserAgent",
		}
	}

	t.Run("accept", func(t *testing.T) {
		assert := assert.New(t)
		server := newTestServer()
		defer server.Close()

		s := newSender(testSenderConfig(server.URL))
		for i := 0; i < 20; i++ {
			s.Push(expectResponses(200))
		}
		s.Stop()

		assert.Equal(20, server.Total(), "total")
		assert.Equal(20, server.Accepted(), "accepted")
		assert.Equal(0, server.Retried(), "retry")
		assert.Equal(0, server.Failed(), "failed")
	})

	t.Run("peak", func(t *testing.T) {
		assert := assert.New(t)
		server := newTestServerWithLatency(50 * time.Millisecond)
		defer server.Close()

		s := newSender(testSenderConfig(server.URL))
		for i := 0; i < climit*2; i++ {
			// we have to sleep for a bit to yield to the receiver, otherwise
			// the channel will get immediately full.
			time.Sleep(time.Millisecond)
			s.Push(expectResponses(200))
		}
		s.Stop()

		assert.True(server.Peak() <= climit)
		assert.Equal(climit*2, server.Total(), "total")
		assert.Equal(climit*2, server.Accepted(), "accepted")
		assert.Equal(0, server.Retried(), "retry")
		assert.Equal(0, server.Failed(), "failed")
	})

	t.Run("failed", func(t *testing.T) {
		assert := assert.New(t)
		server := newTestServer()
		defer server.Close()

		s := newSender(testSenderConfig(server.URL))
		for i := 0; i < 20; i++ {
			s.Push(expectResponses(404))
		}
		s.Stop()

		assert.Equal(20, server.Total(), "total")
		assert.Equal(0, server.Accepted(), "accepted")
		assert.Equal(0, server.Retried(), "retry")
		assert.Equal(20, server.Failed(), "failed")
	})

	t.Run("retry", func(t *testing.T) {
		assert := assert.New(t)
		server := newTestServer()
		defer server.Close()
		defer func(old func(int) time.Duration) { backoffDuration = old }(backoffDuration)
		var backoffCalls []int
		backoffDuration = func(d int) time.Duration {
			backoffCalls = append(backoffCalls, d)
			return time.Nanosecond
		}

		s := newSender(testSenderConfig(server.URL))
		s.Push(expectResponses(503, 408, 200))
		s.Stop()

		assert.Equal([]int{0, 1, 2}, backoffCalls)
		assert.Equal(3, server.Total(), "total")
		assert.Equal(2, server.Retried(), "retry")
		assert.Equal(1, server.Accepted(), "accepted")
	})

	t.Run("many", func(t *testing.T) {
		assert := assert.New(t)
		server := newTestServer()
		defer server.Close()
		defer useBackoffDuration(time.Millisecond)()

		s := newSender(testSenderConfig(server.URL))
		s.Push(expectResponses(503, 503, 200))
		for i := 0; i < 20; i++ {
			s.Push(expectResponses(403))
		}

		s.Stop()

		assert.Equal(23, server.Total(), "total")
		assert.Equal(2, server.Retried(), "retry")
		assert.Equal(1, server.Accepted(), "accepted")
		assert.Equal(20, server.Failed(), "failed")
	})

	t.Run("headers", func(t *testing.T) {
		assert := assert.New(t)
		var wg sync.WaitGroup
		wg.Add(1)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			assert.Equal(testAPIKey, req.Header.Get(headerAPIKey))
			assert.Equal("testUserAgent", req.Header.Get(headerUserAgent))
			wg.Done()
		}))
		defer server.Close()
		s := newSender(testSenderConfig(server.URL))
		s.Push(expectResponses(http.StatusOK))
		s.Stop()
		wg.Wait()
	})

	t.Run("events", func(t *testing.T) {
		assert := assert.New(t)
		server := newTestServer()
		defer server.Close()
		defer useBackoffDuration(0)()

		var recorder mockRecorder
		cfg := testSenderConfig(server.URL)
		cfg.recorder = &recorder
		s := newSender(cfg)

		// push a couple of payloads
		start := time.Now()
		s.Push(expectResponses(503, 503, 200))
		s.Push(expectResponses(200))
		s.Push(expectResponses(200))
		for i := 0; i < 4; i++ {
			s.Push(expectResponses(403))
		}
		s.Stop()

		retried := recorder.data(eventTypeRetry)
		assert.Equal(2, len(retried))
		for i := 0; i < 2; i++ {
			assert.True(retried[i].bytes > len("|503,503,200"))
			assert.Equal(`server responded with "503 Service Unavailable"`, retried[i].err.(*retriableError).err.Error())
			assert.Equal(1, retried[i].count)
			assert.True(time.Since(start)-retried[i].duration < time.Second)
		}

		sent := recorder.data(eventTypeSent)
		assert.Equal(3, len(sent))
		for i := 0; i < 3; i++ {
			assert.True(sent[i].bytes > len("|403"))
			assert.NoError(sent[i].err)
			assert.Equal(1, sent[i].count)
			assert.True(time.Since(start)-sent[i].duration < time.Second)
		}

		failed := recorder.data(eventTypeRejected)
		assert.Equal(4, len(failed))
		for i := 0; i < 4; i++ {
			assert.True(failed[i].bytes > len("|403"))
			assert.Equal("403 Forbidden", failed[i].err.Error())
			assert.Equal(1, failed[i].count)
			assert.True(time.Since(start)-failed[i].duration < time.Second)
		}
	})
}

func TestPayload(t *testing.T) {
	expectBody := bytes.NewBufferString("body")
	bodyLength := strconv.Itoa(expectBody.Len())

	t.Run("headers", func(t *testing.T) {
		assert := assert.New(t)
		p := newPayload(map[string]string{
			"k1": "v1",
			"k2": "v2",
		})
		p.body = expectBody
		u, err := url.Parse("http://whatever")
		assert.NoError(err)
		req, err := p.httpRequest(u)
		assert.NoError(err)
		assert.Len(req.Header, 3)
		assert.Equal(req.Header.Get("Content-Length"), bodyLength)
		assert.Equal("v1", req.Header.Get("k1"))
		assert.Equal("v2", req.Header.Get("k2"))
	})

	t.Run("httpRequest", func(t *testing.T) {
		assert := assert.New(t)
		p := newPayload(map[string]string{"DD-Api-Key": testAPIKey})
		p.body = expectBody
		url, err := url.Parse("http://localhost/my/path")
		if err != nil {
			t.Fatal(err)
		}
		req, err := p.httpRequest(url)
		assert.NoError(err)
		assert.Equal(http.MethodPost, req.Method)
		assert.Equal("/my/path", req.URL.Path)
		assert.Equal("4", req.Header.Get("Content-Length"))
		assert.Equal(testAPIKey, req.Header.Get("DD-Api-Key"))
		slurp, err := io.ReadAll(req.Body)
		assert.NoError(err)
		req.Body.Close()
		assert.Equal(expectBody.Bytes(), slurp)
	})
}

// useBackoffDuration replaces the current backoff duration with d and returns a
// function which restores it.
func useBackoffDuration(d time.Duration) func() {
	old := backoffDuration
	backoffDuration = func(attempt int) time.Duration { return d }
	return func() { backoffDuration = old }
}

// mockRecorder is a mock eventRecorder which records all calls to recordEvent.
type mockRecorder struct {
	mu                             sync.RWMutex
	retry, sent, dropped, rejected []*eventData
}

// data returns all call data for the given eventType.
func (r *mockRecorder) data(t eventType) []*eventData {
	r.mu.RLock()
	defer r.mu.RUnlock()
	switch t {
	case eventTypeRetry:
		return r.retry
	case eventTypeSent:
		return r.sent
	case eventTypeDropped:
		return r.dropped
	case eventTypeRejected:
		return r.rejected
	default:
		panic("unknown event")
	}
}

// recordEvent implements eventRecorder.
func (r *mockRecorder) recordEvent(t eventType, data *eventData) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch t {
	case eventTypeRetry:
		r.retry = append(r.retry, data)
	case eventTypeSent:
		r.sent = append(r.sent, data)
	case eventTypeDropped:
		r.dropped = append(r.dropped, data)
	case eventTypeRejected:
		r.rejected = append(r.rejected, data)
	}
}
