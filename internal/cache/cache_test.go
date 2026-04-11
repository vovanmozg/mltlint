package cache_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vp/mlt3/internal/cache"
)

func TestCache_MISS(t *testing.T) {
	c, err := cache.Open(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	defer c.Close()

	hash, ok := c.Get("/no/such/file", 100, 12345)
	require.False(t, ok)
	require.Equal(t, uint64(0), hash)
}

func TestCache_HIT(t *testing.T) {
	c, err := cache.Open(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	defer c.Close()

	err = c.Set("/some/file.jpg", 100, 12345, 9876543210)
	require.NoError(t, err)

	hash, ok := c.Get("/some/file.jpg", 100, 12345)
	require.True(t, ok)
	require.Equal(t, uint64(9876543210), hash)
}

func TestCache_INV_MTIME(t *testing.T) {
	c, err := cache.Open(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	defer c.Close()

	err = c.Set("/some/file.jpg", 100, 12345, 9876543210)
	require.NoError(t, err)

	hash, ok := c.Get("/some/file.jpg", 200, 12345) // different mtime
	require.False(t, ok)
	require.Equal(t, uint64(0), hash)
}

func TestCache_INV_SIZE(t *testing.T) {
	c, err := cache.Open(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	defer c.Close()

	err = c.Set("/some/file.jpg", 100, 12345, 9876543210)
	require.NoError(t, err)

	hash, ok := c.Get("/some/file.jpg", 100, 99999) // different size
	require.False(t, ok)
	require.Equal(t, uint64(0), hash)
}

func TestCache_UPD(t *testing.T) {
	c, err := cache.Open(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	defer c.Close()

	err = c.Set("/some/file.jpg", 100, 12345, 111)
	require.NoError(t, err)

	err = c.Set("/some/file.jpg", 200, 12345, 222)
	require.NoError(t, err)

	hash, ok := c.Get("/some/file.jpg", 200, 12345)
	require.True(t, ok)
	require.Equal(t, uint64(222), hash)

	// Old key should not exist
	_, ok = c.Get("/some/file.jpg", 100, 12345)
	require.False(t, ok)
}
