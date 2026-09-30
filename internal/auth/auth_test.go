package auth

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func creds(expiresAt int64) string {
	return fmt.Sprintf(`{"claudeAiOauth":{"accessToken":"t%d","expiresAt":%d}}`, expiresAt, expiresAt)
}

func put(t *testing.T, path, data string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(data), 0o600))
}

func get(t *testing.T, path string) string {
	t.Helper()

	b, err := os.ReadFile(path)
	require.NoError(t, err)

	return string(b)
}

func setup(t *testing.T) (Store, string) {
	t.Helper()

	root := t.TempDir()

	return Store{Dir: filepath.Join(root, "claude-auth")}, filepath.Join(root, "run", "claude")
}

func TestExpiresAt(t *testing.T) {
	got, ok := ExpiresAt([]byte(creds(1700000000000)))
	require.True(t, ok)
	assert.Equal(t, time.UnixMilli(1700000000000), got)

	for _, bad := range []string{``, `{}`, `{"claudeAiOauth":{}}`, `nope`, `{"claudeAiOauth":{"expiresAt":"x"}}`} {
		_, ok := ExpiresAt([]byte(bad))
		assert.False(t, ok, bad)
	}
}

func TestNewer(t *testing.T) {
	tests := []struct {
		name        string
		run, stored string
		want        bool
	}{
		{"later wins", creds(2), creds(1), true},
		{"equal keeps stored", creds(1), creds(1), false},
		{"earlier keeps stored", creds(1), creds(2), false},
		{"no stored", creds(1), "", true},
		{"unreadable stored", creds(1), "junk", true},
		{"unreadable run", "junk", creds(1), false},
		{"both unreadable", "junk", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, Newer([]byte(tt.run), []byte(tt.stored)))
		})
	}
}

func TestWarning(t *testing.T) {
	now := time.UnixMilli(1_000_000_000)
	ms := func(d time.Duration) int64 { return now.Add(d).UnixMilli() }

	assert.Contains(t, Warning([]byte(creds(ms(-time.Minute))), now), "expired")
	assert.Contains(t, Warning([]byte(creds(ms(30*time.Minute))), now), "30 min")
	assert.Empty(t, Warning([]byte(creds(ms(2*time.Hour))), now))
	assert.Empty(t, Warning([]byte("junk"), now))
}

func TestCopyIn(t *testing.T) {
	s, cfg := setup(t)
	put(t, s.File(), creds(5))

	data, err := s.CopyIn(cfg)
	require.NoError(t, err)
	assert.Equal(t, creds(5), string(data))
	assert.Equal(t, creds(5), get(t, filepath.Join(cfg, CredentialsName)))

	info, err := os.Stat(filepath.Join(cfg, CredentialsName))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestCopyInNoLogin(t *testing.T) {
	s, cfg := setup(t)

	_, err := s.CopyIn(cfg)
	assert.ErrorIs(t, err, ErrNoLogin)
}

func TestCopyBack(t *testing.T) {
	tests := []struct {
		name        string
		stored, run string // "" = absent
		want        string // stored afterwards, "" = absent
		copied      bool
	}{
		{"newer run wins", creds(1), creds(2), creds(2), true},
		{"older run loses", creds(2), creds(1), creds(2), false},
		{"no run file", creds(1), "", creds(1), false},
		{"first login", "", creds(1), creds(1), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, cfg := setup(t)
			require.NoError(t, os.MkdirAll(cfg, 0o750))

			if tt.stored != "" {
				put(t, s.File(), tt.stored)
			}

			if tt.run != "" {
				put(t, filepath.Join(cfg, CredentialsName), tt.run)
			}

			copied, err := s.CopyBack(cfg)
			require.NoError(t, err)
			assert.Equal(t, tt.copied, copied)

			if tt.want == "" {
				assert.NoFileExists(t, s.File())

				return
			}

			assert.Equal(t, tt.want, get(t, s.File()))
		})
	}
}

// Two (here: many) runs refreshing at once: the newest token always wins,
// whatever order the copy-backs run in.
func TestCopyBackConcurrentNewestWins(t *testing.T) {
	s, _ := setup(t)
	put(t, s.File(), creds(1))

	const runs = 16

	root := t.TempDir()

	var wg sync.WaitGroup

	for i := range runs {
		cfg := filepath.Join(root, fmt.Sprint(i))
		put(t, filepath.Join(cfg, CredentialsName), creds(int64(100+i)))

		wg.Go(func() {
			_, err := s.CopyBack(cfg)
			assert.NoError(t, err)
		})
	}

	wg.Wait()
	assert.Equal(t, creds(100+runs-1), get(t, s.File()))
}

func TestPutStoresALoginUnconditionally(t *testing.T) {
	s, run := setup(t)
	put(t, s.File(), creds(9_000))
	login := filepath.Join(run, CredentialsName)
	put(t, login, creds(1_000))

	require.NoError(t, s.Put(login))
	assert.Equal(t, creds(1_000), get(t, s.File()), "a fresh login replaces whatever was stored")
}

func TestPutWithoutLogin(t *testing.T) {
	s, run := setup(t)

	err := s.Put(filepath.Join(run, CredentialsName))
	require.ErrorIs(t, err, ErrLoginFailed)

	put(t, filepath.Join(run, CredentialsName), "{}")
	require.ErrorIs(t, s.Put(filepath.Join(run, CredentialsName)), ErrLoginFailed, "no OAuth token in it")
}
