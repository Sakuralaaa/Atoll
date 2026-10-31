package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Sakuralaaa/Atoll/internal/server/secrets"
	"github.com/Sakuralaaa/Atoll/internal/server/store"
	"golang.org/x/crypto/bcrypt"
)

// ResetPassword sets a new password for username ("" = the owner) directly in the data dir's
// database and signs that user out everywhere. An empty password generates one. It returns
// the username and the password that was set.
func ResetPassword(dataDir, username, password string) (string, string, error) {
	if _, err := os.Stat(filepath.Join(dataDir, "atoll.db")); err != nil {
		return "", "", fmt.Errorf("no database in %s: %w", dataDir, err)
	}
	if password == "" {
		password = randString(20, tokenAlphabet)
	}
	if len(password) < 8 || len(password) > 72 {
		return "", "", fmt.Errorf("password must be 8-72 characters")
	}
	key, err := secrets.LoadOrCreateKey(filepath.Join(dataDir, "master.key"))
	if err != nil {
		return "", "", err
	}
	box, err := secrets.New(key)
	if err != nil {
		return "", "", err
	}
	st, err := store.Open(filepath.Join(dataDir, "atoll.db"), box)
	if err != nil {
		return "", "", err
	}
	defer st.Close()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", "", err
	}
	u, err := st.SetPassword(context.Background(), username, string(hash))
	if err == store.ErrNotFound {
		if username == "" {
			return "", "", fmt.Errorf("no user exists yet; open the panel to run first-time setup")
		}
		return "", "", fmt.Errorf("user %q not found", username)
	}
	if err != nil {
		return "", "", err
	}
	st.Audit(context.Background(), "cli", "auth.reset_password", u.Username, nil)
	return u.Username, password, nil
}

// openDataStore opens the database in dataDir for offline admin commands.
func openDataStore(dataDir string) (*store.Store, error) {
	if _, err := os.Stat(filepath.Join(dataDir, "atoll.db")); err != nil {
		return nil, fmt.Errorf("no database in %s: %w", dataDir, err)
	}
	key, err := secrets.LoadOrCreateKey(filepath.Join(dataDir, "master.key"))
	if err != nil {
		return nil, err
	}
	box, err := secrets.New(key)
	if err != nil {
		return nil, err
	}
	return store.Open(filepath.Join(dataDir, "atoll.db"), box)
}

// Reset2FA turns two-factor authentication off for username ("" = the owner) directly
// in the data dir's database, deletes its recovery codes and signs the user out
// everywhere. It is the escape hatch for a lost authenticator; run it on the control host.
func Reset2FA(dataDir, username string) (string, error) {
	st, err := openDataStore(dataDir)
	if err != nil {
		return "", err
	}
	defer st.Close()
	u, err := st.ResetTOTPByName(context.Background(), username)
	if err == store.ErrNotFound {
		if username == "" {
			return "", fmt.Errorf("no user exists yet")
		}
		return "", fmt.Errorf("user %q not found", username)
	}
	if err != nil {
		return "", err
	}
	st.Audit(context.Background(), "cli", "auth.reset_2fa", u.Username, nil)
	return u.Username, nil
}
