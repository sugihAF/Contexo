package sync

import "testing"

// A client with no timeout makes every progress spinner drawn over it
// unbounded, and lets `ctx status` hang forever on a black-holed network.
func TestClientHasTimeout(t *testing.T) {
	c := NewClient("https://example.test", "tok")
	if c.http.Timeout == 0 {
		t.Fatal("sync.Client has no HTTP timeout")
	}
	if c.http.Timeout != requestTimeout {
		t.Errorf("timeout = %v, want %v", c.http.Timeout, requestTimeout)
	}
}
