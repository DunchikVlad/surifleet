// Package authn — примитивы локальной аутентификации (чанк 28):
// bcrypt-хэширование паролей и непрозрачные сессионные токены
// (32 случайных байта, base64url; в БД — только SHA-256 хэш).
package authn

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

// HashPassword — bcrypt-хэш пароля (cost по умолчанию).
func HashPassword(password string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("bcrypt: %w", err)
	}
	return string(h), nil
}

// CheckPassword сверяет пароль с bcrypt-хэшем.
func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// NewToken генерирует сессионный токен (base64url, 32 байта энтропии).
func NewToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("генерация токена: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// TokenHash — SHA-256 токена (hex); в БД хранится только хэш.
func TokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return fmt.Sprintf("%x", sum)
}
