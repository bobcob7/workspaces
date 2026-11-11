package loop

import (
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var memoryTestNow = time.Date(2026, 11, 10, 8, 14, 0, 0, time.UTC)

func TestValidateMemoryKey(t *testing.T) {
	cases := map[string]bool{
		"seen":                  true,
		"last_run_2":            true,
		"":                      false,
		"1seen":                 false,
		"has-dash":              false,
		"has space":             false,
		strings.Repeat("k", 65): false,
	}
	for key, ok := range cases {
		err := ValidateMemoryKey(key)
		if ok {
			require.NoError(t, err, key)
			continue
		}
		require.ErrorIs(t, err, ErrMemoryInvalid, key)
	}
}

func TestDataStore_ReadMemory(t *testing.T) {
	t.Parallel()
	d := newTestDataStore(t)
	t.Run("a missing file is an empty memory", func(t *testing.T) {
		m, err := d.ReadMemory("deps")
		require.NoError(t, err)
		assert.Equal(t, MemoryVersion, m.Version)
		assert.NotNil(t, m.Entries)
		assert.Empty(t, m.Entries)
	})
	t.Run("a version newer than this ws is ErrNewerData", func(t *testing.T) {
		require.NoError(t, writeFileAtomic(d.MemoryPath("deps"), []byte(`{"version":2,"entries":{}}`)))
		_, err := d.ReadMemory("deps")
		assert.ErrorIs(t, err, ErrNewerData)
	})
}

func TestDataStore_SetMemory(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	t.Run("a new key is rev 1 with the caller and time", func(t *testing.T) {
		t.Parallel()
		d := newTestDataStore(t)
		entry, err := d.SetMemory(ctx, "deps", "seen", "#412", "loop-deps-1a2b3c4d", 0, memoryTestNow)
		require.NoError(t, err)
		assert.Equal(t, 1, entry.Rev)
		assert.Equal(t, "#412", entry.Value)
		assert.Equal(t, "loop-deps-1a2b3c4d", entry.UpdatedBy)
		assert.True(t, entry.UpdatedAt.Equal(memoryTestNow))
	})
	t.Run("each set increases the rev", func(t *testing.T) {
		t.Parallel()
		d := newTestDataStore(t)
		_, err := d.SetMemory(ctx, "deps", "seen", "#1", "", AnyRev, memoryTestNow)
		require.NoError(t, err)
		entry, err := d.SetMemory(ctx, "deps", "seen", "#2", "", AnyRev, memoryTestNow)
		require.NoError(t, err)
		assert.Equal(t, 2, entry.Rev)
	})
	t.Run("expectRev 0 sets a new key but conflicts with an existing one", func(t *testing.T) {
		t.Parallel()
		d := newTestDataStore(t)
		_, err := d.SetMemory(ctx, "deps", "fresh", "a", "", 0, memoryTestNow)
		require.NoError(t, err)
		_, err = d.SetMemory(ctx, "deps", "fresh", "b", "", 0, memoryTestNow)
		assert.ErrorIs(t, err, ErrMemoryConflict)
	})
	t.Run("a stale expectRev conflicts and changes nothing", func(t *testing.T) {
		t.Parallel()
		d := newTestDataStore(t)
		_, err := d.SetMemory(ctx, "deps", "seen", "#1", "", AnyRev, memoryTestNow)
		require.NoError(t, err)
		_, err = d.SetMemory(ctx, "deps", "seen", "#2", "", 5, memoryTestNow)
		require.ErrorIs(t, err, ErrMemoryConflict)
		m, err := d.ReadMemory("deps")
		require.NoError(t, err)
		assert.Equal(t, "#1", m.Values()["seen"])
	})
	t.Run("a value over 64 KiB is full", func(t *testing.T) {
		t.Parallel()
		d := newTestDataStore(t)
		_, err := d.SetMemory(ctx, "deps", "big", strings.Repeat("x", MaxMemoryValue+1), "", AnyRev, memoryTestNow)
		require.ErrorIs(t, err, ErrMemoryFull)
	})
	t.Run("the 101st key is full", func(t *testing.T) {
		t.Parallel()
		d := newTestDataStore(t)
		for i := range MaxMemoryKeys {
			_, err := d.SetMemory(ctx, "deps", "k"+strconv.Itoa(i), "v", "", AnyRev, memoryTestNow)
			require.NoError(t, err)
		}
		_, err := d.SetMemory(ctx, "deps", "one_more", "v", "", AnyRev, memoryTestNow)
		require.ErrorIs(t, err, ErrMemoryFull)
		_, err = d.SetMemory(ctx, "deps", "k0", "changed", "", AnyRev, memoryTestNow)
		assert.NoError(t, err, "an existing key may still be set")
	})
	t.Run("a total past 256 KiB is full", func(t *testing.T) {
		t.Parallel()
		d := newTestDataStore(t)
		value := strings.Repeat("x", MaxMemoryValue)
		for _, key := range []string{"a", "b", "c", "d"} {
			_, err := d.SetMemory(ctx, "deps", key, value, "", AnyRev, memoryTestNow)
			require.NoError(t, err)
		}
		_, err := d.SetMemory(ctx, "deps", "e", "x", "", AnyRev, memoryTestNow)
		assert.ErrorIs(t, err, ErrMemoryFull)
	})
	t.Run("a bad key is invalid", func(t *testing.T) {
		t.Parallel()
		d := newTestDataStore(t)
		_, err := d.SetMemory(ctx, "deps", "1bad", "x", "", AnyRev, memoryTestNow)
		assert.ErrorIs(t, err, ErrMemoryInvalid)
	})
	t.Run("concurrent sets to different keys both reach rev 20", func(t *testing.T) {
		t.Parallel()
		d := newTestDataStore(t)
		var wg sync.WaitGroup
		for _, key := range []string{"left", "right"} {
			wg.Go(func() {
				for i := range 20 {
					_, err := d.SetMemory(ctx, "deps", key, strconv.Itoa(i), "", AnyRev, memoryTestNow)
					assert.NoError(t, err)
				}
			})
		}
		wg.Wait()
		m, err := d.ReadMemory("deps")
		require.NoError(t, err)
		assert.Equal(t, 20, m.Entries["left"].Rev)
		assert.Equal(t, 20, m.Entries["right"].Rev)
	})
}

func TestDataStore_RemoveMemory(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	d := newTestDataStore(t)
	_, err := d.SetMemory(ctx, "deps", "seen", "#1", "", AnyRev, memoryTestNow)
	require.NoError(t, err)
	t.Run("a stale expectRev conflicts and keeps the key", func(t *testing.T) {
		require.ErrorIs(t, d.RemoveMemory(ctx, "deps", "seen", 9), ErrMemoryConflict)
		m, err := d.ReadMemory("deps")
		require.NoError(t, err)
		assert.Contains(t, m.Entries, "seen")
	})
	t.Run("removes a set key", func(t *testing.T) {
		require.NoError(t, d.RemoveMemory(ctx, "deps", "seen", 1))
		m, err := d.ReadMemory("deps")
		require.NoError(t, err)
		assert.NotContains(t, m.Entries, "seen")
	})
	t.Run("a missing key is not found", func(t *testing.T) {
		assert.ErrorIs(t, d.RemoveMemory(ctx, "deps", "seen", AnyRev), ErrMemoryNotFound)
	})
	t.Run("a bad key is invalid", func(t *testing.T) {
		assert.ErrorIs(t, d.RemoveMemory(ctx, "deps", "1bad", AnyRev), ErrMemoryInvalid)
	})
}

func TestDataStore_ClearMemory(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	t.Run("removes every key and counts them", func(t *testing.T) {
		t.Parallel()
		d := newTestDataStore(t)
		for _, key := range []string{"a", "b", "c"} {
			_, err := d.SetMemory(ctx, "deps", key, "v", "", AnyRev, memoryTestNow)
			require.NoError(t, err)
		}
		removed, err := d.ClearMemory(ctx, "deps")
		require.NoError(t, err)
		assert.Equal(t, 3, removed)
		m, err := d.ReadMemory("deps")
		require.NoError(t, err)
		assert.Empty(t, m.Entries)
	})
	t.Run("an empty memory clears to zero", func(t *testing.T) {
		t.Parallel()
		d := newTestDataStore(t)
		removed, err := d.ClearMemory(ctx, "deps")
		require.NoError(t, err)
		assert.Equal(t, 0, removed)
	})
}

func TestMemory_Values(t *testing.T) {
	m := Memory{Entries: map[string]MemoryEntry{"a": {Value: "1"}, "b": {Value: "2"}}}
	assert.Equal(t, map[string]string{"a": "1", "b": "2"}, m.Values())
	assert.NotNil(t, (&Memory{}).Values())
}

func TestMemory_Bytes(t *testing.T) {
	m := Memory{Entries: map[string]MemoryEntry{"a": {Value: "abc"}, "b": {Value: "de"}}}
	assert.Equal(t, 5, m.Bytes())
	assert.Equal(t, 0, (&Memory{}).Bytes())
}

func TestMemory_Keys(t *testing.T) {
	m := Memory{Entries: map[string]MemoryEntry{"zeta": {}, "alpha": {}, "mid": {}}}
	assert.Equal(t, []string{"alpha", "mid", "zeta"}, m.Keys())
}
