// Package password hashes and verifies user passwords with bcrypt.
package password

import "golang.org/x/crypto/bcrypt"

// MaxLen is the maximum password length supported by bcrypt.
const MaxLen = 72

// Hash returns the bcrypt hash of password.
func Hash(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// Check reports whether password matches the bcrypt hash.
func Check(hash, password string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}
