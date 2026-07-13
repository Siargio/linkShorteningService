package domain

import (
	"errors"
	"testing"
)

func TestValidateLongURL(t *testing.T) {
	tests := []struct {
		name    string
		rawURL  string
		wantErr bool
	}{
		{
			name:    "valid HTTPS URL",
			rawURL:  "https://golang.org",
			wantErr: false,
		},
		{
			name:    "valid HTTP URL with path",
			rawURL:  "http://example.com/page",
			wantErr: false,
		},
		{
			name:    "valid URL with query",
			rawURL:  "https://example.com/page?id=10",
			wantErr: false,
		},
		{
			name:    "empty URL",
			rawURL:  "",
			wantErr: true,
		},
		{
			name:    "URL without scheme",
			rawURL:  "golang.org",
			wantErr: true,
		},
		{
			name:    "URL without host",
			rawURL:  "https://",
			wantErr: true,
		},
		{
			name:    "unsupported FTP scheme",
			rawURL:  "ftp://example.com",
			wantErr: true,
		},
		{
			name:    "javascript scheme",
			rawURL:  "javascript:alert(1)",
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateLongURL(test.rawURL)

			if test.wantErr && err == nil {
				t.Fatal("expected an error, got nil")
			}

			if !test.wantErr && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			if test.wantErr && !errors.Is(err, ErrInvalidURL) {
				t.Fatalf("expected ErrInvalidURL, got %v", err)
			}
		})
	}
}

func TestValidateShortCode(t *testing.T) {
	tests := []struct {
		name    string
		code    string
		wantErr bool
	}{
		{
			name:    "valid code",
			code:    "x7k2mN",
			wantErr: false,
		},
		{
			name:    "empty code",
			code:    "",
			wantErr: true,
		},
		{
			name:    "code contains dash",
			code:    "abc-123",
			wantErr: true,
		},
		{
			name:    "code contains slash",
			code:    "abc/123",
			wantErr: true,
		},
		{
			name:    "code is too long",
			code:    "abcdefghijk",
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateShortCode(test.code)

			if test.wantErr && err == nil {
				t.Fatal("expected an error, got nil")
			}

			if !test.wantErr && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}

func TestGenerateShortCode(t *testing.T) {
	const expectedLength = 6

	code, err := GenerateShortCode(expectedLength)
	if err != nil {
		t.Fatalf("GenerateShortCode returned error: %v", err)
	}

	if len(code) != expectedLength {
		t.Fatalf(
			"expected code length %d, got %d",
			expectedLength,
			len(code),
		)
	}

	if err := ValidateShortCode(code); err != nil {
		t.Fatalf("generated invalid code %q: %v", code, err)
	}
}

func TestGenerateShortCode_InvalidLength(t *testing.T) {
	tests := []int{
		0,
		-1,
		MaxShortCodeLength + 1,
	}

	for _, length := range tests {
		_, err := GenerateShortCode(length)
		if !errors.Is(err, ErrInvalidShortCode) {
			t.Fatalf(
				"length %d: expected ErrInvalidShortCode, got %v",
				length,
				err,
			)
		}
	}
}
