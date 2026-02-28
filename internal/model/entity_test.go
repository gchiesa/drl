package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEntity_Key_IncludesIP(t *testing.T) {
	e1 := Entity{IP: "10.0.0.1", Path: "/api/v1"}
	e2 := Entity{IP: "10.0.0.2", Path: "/api/v1"}
	assert.NotEqual(t, e1.Key(), e2.Key(),
		"different IPs must produce different keys")
}

func TestEntity_Key_IncludesPath(t *testing.T) {
	e1 := Entity{IP: "10.0.0.1", Path: "/a"}
	e2 := Entity{IP: "10.0.0.1", Path: "/b"}
	assert.NotEqual(t, e1.Key(), e2.Key(),
		"different paths must produce different keys")
}

func TestEntity_Key_IncludesHeaders(t *testing.T) {
	e1 := Entity{IP: "10.0.0.1", Path: "/api", Headers: map[string]string{"User-Agent": "Bot"}}
	e2 := Entity{IP: "10.0.0.1", Path: "/api", Headers: map[string]string{"User-Agent": "Human"}}
	assert.NotEqual(t, e1.Key(), e2.Key())
}

func TestEntity_Key_OrderIndependent(t *testing.T) {
	e1 := Entity{IP: "10.0.0.1", Path: "/api", Headers: map[string]string{"B": "2", "A": "1"}}
	e2 := Entity{IP: "10.0.0.1", Path: "/api", Headers: map[string]string{"A": "1", "B": "2"}}
	assert.Equal(t, e1.Key(), e2.Key(),
		"header iteration order must not affect the key")
}

func TestEntity_Key_Deterministic(t *testing.T) {
	e := Entity{IP: "192.168.1.1", Path: "/api/v1/payments", Headers: map[string]string{"User-Agent": "ScraperBot"}}
	k1 := e.Key()
	k2 := e.Key()
	assert.Equal(t, k1, k2)
}

func TestEntity_Key_Length(t *testing.T) {
	e := Entity{IP: "10.0.0.1", Path: "/api"}
	assert.Len(t, e.Key(), 16, "xxHash-64 produces 16 hex characters")
}

func TestEntity_Key_NoHeaders(t *testing.T) {
	e := Entity{IP: "10.0.0.1", Path: "/api/v1"}
	k := e.Key()
	assert.NotEmpty(t, k)
	assert.Len(t, k, 16)
}

func TestEntity_Key_EmptyHeadersMap(t *testing.T) {
	e1 := Entity{IP: "10.0.0.1", Path: "/api", Headers: nil}
	e2 := Entity{IP: "10.0.0.1", Path: "/api", Headers: map[string]string{}}
	assert.Equal(t, e1.Key(), e2.Key(),
		"nil and empty headers must produce the same key")
}
