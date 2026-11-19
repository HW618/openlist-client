package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// FileSecrets is where an earlier version of the app kept the tokens: a file of
// dir that only the user reads, sealed with AES-256-GCM under a key of its own
// file beside it. The app keeps them in the platform's secure store now and
// reads this file only to bring what is in it there (All), and then deletes it
// (Wipe).
type FileSecrets struct {
	Dir string
	mu  sync.Mutex
}

func (f *FileSecrets) file() string    { return filepath.Join(f.Dir, "tokens.enc") }
func (f *FileSecrets) keyFile() string { return filepath.Join(f.Dir, "tokens.key") }

// legacy is where earlier versions kept tokens, as text.
func (f *FileSecrets) legacy() string { return filepath.Join(f.Dir, "tokens.json") }

// key reads the key, making it when create is set and there is none.
func (f *FileSecrets) key(create bool) ([]byte, error) {
	if b, err := os.ReadFile(f.keyFile()); err == nil && len(b) == 32 {
		return b, nil
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if !create {
		return nil, os.ErrNotExist
	}
	if err := os.MkdirAll(f.Dir, 0o700); err != nil {
		return nil, err
	}
	k := make32()
	if err := writeFile(f.keyFile(), k); err != nil {
		return nil, err
	}
	return k, nil
}

func make32() []byte {
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		panic(err)
	}
	return k
}

// writeFile writes a file only its owner reads, whole or not at all.
func writeFile(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (f *FileSecrets) read() map[string]string {
	m := map[string]string{}
	if sealed, err := os.ReadFile(f.file()); err == nil {
		// A file that cannot be opened holds nothing the user can use; the
		// user signs in again.
		if k, err := f.key(false); err == nil {
			if plain, err := open(k, sealed); err == nil {
				_ = json.Unmarshal(plain, &m)
			}
		}
		return m
	}
	if b, err := os.ReadFile(f.legacy()); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

func (f *FileSecrets) write(m map[string]string) error {
	if err := os.MkdirAll(f.Dir, 0o700); err != nil {
		return err
	}
	k, err := f.key(true)
	if err != nil {
		return err
	}
	plain, err := json.Marshal(m)
	if err != nil {
		return err
	}
	sealed, err := seal(k, plain)
	if err != nil {
		return err
	}
	if err := writeFile(f.file(), sealed); err != nil {
		return err
	}
	_ = os.Remove(f.legacy())
	return nil
}

func gcm(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// seal encrypts plain, with a nonce in front of the result.
func seal(key, plain []byte) ([]byte, error) {
	g, err := gcm(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return g.Seal(nonce, nonce, plain, nil), nil
}

// open decrypts what seal made.
func open(key, sealed []byte) ([]byte, error) {
	g, err := gcm(key)
	if err != nil {
		return nil, err
	}
	if len(sealed) < g.NonceSize() {
		return nil, errors.New("sealed data is too short")
	}
	return g.Open(nil, sealed[:g.NonceSize()], sealed[g.NonceSize():], nil)
}

// Get implements Secrets.
func (f *FileSecrets) Get(siteID string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.read()[siteID]
	return t, ok
}

// Set implements Secrets.
func (f *FileSecrets) Set(siteID, token string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := f.read()
	m[siteID] = token
	return f.write(m)
}

// Remove implements Secrets.
func (f *FileSecrets) Remove(siteID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := f.read()
	delete(m, siteID)
	return f.write(m)
}

// All is every token the files hold.
func (f *FileSecrets) All() map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.read()
}

// Wipe deletes the files.
func (f *FileSecrets) Wipe() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, name := range []string{f.file(), f.keyFile(), f.legacy()} {
		_ = os.Remove(name)
	}
}
