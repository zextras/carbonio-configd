// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package localconfig

import (
	"crypto/rand"
	"fmt"
	"math/big"
)

// passwordCharset defines the characters used for random password generation.
const passwordCharset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%^&*()-_=+[]{}|;:,.<>?"

// GeneratePassword creates a cryptographically secure random password
// of the given length using alphanumeric and special characters.
func GeneratePassword(length int) (string, error) {
	if length <= 0 {
		return "", fmt.Errorf("password length must be positive, got %d", length)
	}

	result := make([]byte, length)
	charsetLen := big.NewInt(int64(len(passwordCharset)))

	for i := range length {
		idx, err := rand.Int(rand.Reader, charsetLen)
		if err != nil {
			return "", fmt.Errorf("failed to generate random byte: %w", err)
		}

		result[i] = passwordCharset[idx.Int64()]
	}

	return string(result), nil
}
