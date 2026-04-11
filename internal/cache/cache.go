package cache

import (
	"encoding/binary"

	bolt "go.etcd.io/bbolt"
)

var bucketName = []byte("phash-v1")

// Cache is a bbolt-backed perceptual hash cache keyed by (path, mtime, size).
type Cache struct {
	db *bolt.DB
}

// Open opens (or creates) a bbolt database at the given path and ensures
// the phash-v1 bucket exists.
func Open(path string) (*Cache, error) {
	db, err := bolt.Open(path, 0600, nil)
	if err != nil {
		return nil, err
	}

	err = db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(bucketName)
		return err
	})
	if err != nil {
		db.Close()
		return nil, err
	}

	return &Cache{db: db}, nil
}

// Close closes the underlying bbolt database.
func (c *Cache) Close() error {
	return c.db.Close()
}

// makeKey encodes (path, mtime, size) into a composite key.
// Layout: [8 bytes mtime][8 bytes size][path bytes]
func makeKey(path string, mtime, size int64) []byte {
	key := make([]byte, 16+len(path))
	binary.BigEndian.PutUint64(key[0:8], uint64(mtime))
	binary.BigEndian.PutUint64(key[8:16], uint64(size))
	copy(key[16:], path)
	return key
}

// Get retrieves the cached phash for (path, mtime, size).
// Returns (hash, true) on a hit and (0, false) on a miss.
func (c *Cache) Get(path string, mtime, size int64) (uint64, bool) {
	key := makeKey(path, mtime, size)
	var hash uint64
	var found bool

	_ = c.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketName)
		if b == nil {
			return nil
		}
		v := b.Get(key)
		if v == nil {
			return nil
		}
		hash = binary.BigEndian.Uint64(v)
		found = true
		return nil
	})

	return hash, found
}

// Set stores the phash for (path, mtime, size) and removes any stale entries
// for the same path (different mtime or size).
func (c *Cache) Set(path string, mtime, size int64, hash uint64) error {
	newKey := makeKey(path, mtime, size)
	val := make([]byte, 8)
	binary.BigEndian.PutUint64(val, hash)

	pathBytes := []byte(path)

	return c.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketName)

		// Collect stale keys: any key that ends with the path bytes but differs
		// from the new key.
		var staleKeys [][]byte
		cursor := b.Cursor()
		for k, _ := cursor.First(); k != nil; k, _ = cursor.Next() {
			if len(k) >= 16+len(pathBytes) &&
				string(k[16:16+len(pathBytes)]) == path &&
				len(k) == 16+len(pathBytes) &&
				string(k) != string(newKey) {
				cp := make([]byte, len(k))
				copy(cp, k)
				staleKeys = append(staleKeys, cp)
			}
		}

		for _, sk := range staleKeys {
			if err := b.Delete(sk); err != nil {
				return err
			}
		}

		return b.Put(newKey, val)
	})
}
