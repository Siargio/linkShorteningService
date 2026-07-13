package domain

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"strings"
	"time"
)

const (
	DefaultShortCodeLength = 6
	MaxShortCodeLength     = 10
)

const shortCodeAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

var (
	ErrInvalidURL         = errors.New("invalid URL")
	ErrInvalidShortCode   = errors.New("invalid short code")
	ErrLinkNotFound       = errors.New("link not found")
	ErrCodeGeneration     = errors.New("failed to generate short code")
	ErrShortCodeCollision = errors.New("short code collision")
)

// Доменная модель
type Link struct {
	ID        int32     `json:"id"`
	ShortCode string    `json:"short_code"`
	LongURL   string    `json:"long_url"`
	Clicks    int32     `json:"clicks"`
	CreatedAt time.Time `json:"created_at"`
}

func ValidateLongURL(rawURL string) error {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return ErrInvalidURL
	}

	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidURL, err)
	}

	if parsedURL.Host == "" {
		return ErrInvalidURL
	}

	switch strings.ToLower(parsedURL.Scheme) {
	case "http", "https":
		return nil
	default:
		return ErrInvalidURL
	}
}

func ValidateShortCode(code string) error {
	if len(code) == 0 || len(code) > MaxShortCodeLength {
		return ErrInvalidShortCode
	}

	for i := 0; i < len(code); i++ {
		if !isAllowedShortCodeCharacter(code[i]) {
			return ErrInvalidShortCode
		}
	}

	return nil
}

func GenerateShortCode(length int) (string, error) {
	if length <= 0 || length > MaxShortCodeLength {
		return "", ErrInvalidShortCode
	}

	code := make([]byte, length)
	alphabetLength := big.NewInt(int64(len(shortCodeAlphabet)))

	for i := range code {
		randomIndex, err := rand.Int(rand.Reader, alphabetLength)
		if err != nil {
			return "", fmt.Errorf("%w: %v", ErrCodeGeneration, err)
		}

		code[i] = shortCodeAlphabet[randomIndex.Int64()]
	}

	return string(code), nil
}

func isAllowedShortCodeCharacter(character byte) bool {
	switch {
	case character >= 'a' && character <= 'z':
		return true
	case character >= 'A' && character <= 'Z':
		return true
	case character >= '0' && character <= '9':
		return true
	default:
		return false
	}
}
